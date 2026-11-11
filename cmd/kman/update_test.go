package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// releasesServer stands in for api.github.com. The client is rewritten to reach it,
// so the code under test asks for the real URL and nothing leaves the machine.
type releasesServer struct {
	hits   atomic.Int32
	mu     sync.Mutex
	last   *http.Request
	status int
	body   string
	client *http.Client
}

func newReleasesServer(t *testing.T, status int, body string) *releasesServer {
	t.Helper()
	rs := &releasesServer{status: status, body: body}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs.hits.Add(1)
		rs.mu.Lock()
		rs.last = r.Clone(context.Background())
		status, body := rs.status, rs.body
		rs.mu.Unlock()
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)
	u, _ := url.Parse(ts.URL)
	rs.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r = r.Clone(r.Context())
		r.URL.Scheme, r.URL.Host = u.Scheme, u.Host
		return http.DefaultTransport.RoundTrip(r)
	})}
	return rs
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const latestV02 = `{"tag_name":"v0.2.0","html_url":"https://github.com/kirank55/kman/releases/tag/v0.2.0","published_at":"2026-11-10T09:00:00Z","draft":false,"prerelease":false}`

// withVersion makes the binary say it is version v for the test.
func withVersion(t *testing.T, v string) {
	t.Helper()
	old := version
	t.Cleanup(func() { version = old })
	version = v
}

func TestVersionCheckAsksGitHubOnlyWhenRunAndReportsANewerRelease(t *testing.T) {
	withVersion(t, "v0.1.0")
	rs := newReleasesServer(t, 200, latestV02)
	s, stdout, stderr := testSys(t, nil)
	s.http = rs.client

	if code := run(t.Context(), []string{"version"}, s); code != 0 {
		t.Fatalf("kman version exited %d: %s", code, stderr)
	}
	if n := rs.hits.Load(); n != 0 {
		t.Fatalf("plain kman version made %d requests, want none (ADR 0008)", n)
	}

	if code := run(t.Context(), []string{"version", "--check"}, s); code != 0 {
		t.Fatalf("kman version --check exited %d: %s", code, stderr)
	}
	if n := rs.hits.Load(); n != 1 {
		t.Fatalf("version --check made %d requests, want 1", n)
	}
	out := stdout.String()
	for _, want := range []string{"kman v0.1.0", "v0.2.0", "newer", "https://github.com/kirank55/kman/releases/tag/v0.2.0"} {
		if !strings.Contains(out, want) {
			t.Errorf("version --check printed %q, which lacks %q", out, want)
		}
	}

	req := rs.last
	if req.URL.Path != "/repos/kirank55/kman/releases/latest" || req.Method != "GET" {
		t.Errorf("asked %s %s, want GET /repos/kirank55/kman/releases/latest", req.Method, req.URL.Path)
	}
	if ua := req.Header.Get("User-Agent"); ua != "kman/v0.1.0" {
		t.Errorf("User-Agent = %q, want only the program and its version", ua)
	}
	if req.Header.Get("Authorization") != "" || req.Header.Get("Cookie") != "" || req.URL.RawQuery != "" {
		t.Errorf("the request carries more than it needs: %v ?%s", req.Header, req.URL.RawQuery)
	}
}

func TestVersionCheckSaysWhenThereIsNothingNewer(t *testing.T) {
	for _, current := range []string{"v0.2.0", "v0.3.0"} {
		withVersion(t, current)
		s, stdout, _ := testSys(t, nil)
		s.http = newReleasesServer(t, 200, latestV02).client
		if code := run(t.Context(), []string{"version", "--check"}, s); code != 0 {
			t.Fatalf("%s: exit %d", current, code)
		}
		if out := stdout.String(); !strings.Contains(out, "up to date") || strings.Contains(out, "newer kman") {
			t.Errorf("%s printed %q, want that it is up to date", current, out)
		}
	}
}

func TestVersionCheckOnABuildThatIsNotAReleaseSaysItCannotCompare(t *testing.T) {
	for _, v := range []string{"", "(devel)", "v0.2.0-5-gabcdef-dirty"} {
		withVersion(t, v)
		s, stdout, _ := testSys(t, nil)
		s.http = newReleasesServer(t, 200, latestV02).client
		if code := run(t.Context(), []string{"version", "--check"}, s); code != 0 {
			t.Fatalf("%q: exit %d", v, code)
		}
		if out := stdout.String(); !strings.Contains(out, "not a release") || !strings.Contains(out, "v0.2.0") {
			t.Errorf("%q printed %q, want that it is not a release, and the latest", v, out)
		}
	}
}

func TestVersionCheckFailsWithTheReasonWhenGitHubCannotBeReadAndStillPrintsTheVersion(t *testing.T) {
	withVersion(t, "v0.1.0")
	for name, tc := range map[string]struct {
		status int
		body   string
		want   string
	}{
		"an error status": {500, `{"message":"boom"}`, "500"},
		"not found":       {404, `{"message":"Not Found"}`, "404"},
		"not JSON":        {200, `<html>`, "releases"},
		"no tag":          {200, `{}`, "no release"},
		"an odd tag":      {200, `{"tag_name":"nightly"}`, "nightly"},
		"rate limited":    {403, `{"message":"API rate limit exceeded"}`, "403"},
	} {
		s, stdout, stderr := testSys(t, nil)
		s.http = newReleasesServer(t, tc.status, tc.body).client
		if code := run(t.Context(), []string{"version", "--check"}, s); code != 1 {
			t.Errorf("%s: exit %d, want 1", name, code)
		}
		if !strings.Contains(stderr.String(), tc.want) {
			t.Errorf("%s: stderr %q lacks %q", name, stderr, tc.want)
		}
		if !strings.Contains(stdout.String(), "kman v0.1.0") {
			t.Errorf("%s: the version was not printed first: %q", name, stdout)
		}
	}
}

func TestVersionCheckFailsWhenThereIsNoNetworkWithoutHanging(t *testing.T) {
	withVersion(t, "v0.1.0")
	s, _, stderr := testSys(t, nil)
	s.http = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("dial tcp: no route to host")
	})}
	if code := run(t.Context(), []string{"version", "--check"}, s); code != 1 || !strings.Contains(stderr.String(), "no route to host") {
		t.Errorf("exit %d, stderr %q, want 1 and the reason", code, stderr)
	}
}

func TestVersionCheckJSONHasTheVersionAndWhatIsLatest(t *testing.T) {
	withVersion(t, "v0.1.0")
	s, stdout, _ := testSys(t, nil)
	s.http = newReleasesServer(t, 200, latestV02).client
	if code := run(t.Context(), []string{"version", "--check", "--json"}, s); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var got struct {
		Version         string `json:"version"`
		Latest          string `json:"latest"`
		UpdateAvailable bool   `json:"update_available"`
		URL             string `json:"url"`
	}
	if err := json.Unmarshal([]byte(stdout.String()), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
	if got.Version != "v0.1.0" || got.Latest != "v0.2.0" || !got.UpdateAvailable || !strings.HasSuffix(got.URL, "/v0.2.0") {
		t.Errorf("got %+v", got)
	}
}

func TestVersionTakesOnlyItsFlags(t *testing.T) {
	s, _, stderr := testSys(t, nil)
	if code := run(t.Context(), []string{"version", "extra"}, s); code != 1 || !strings.Contains(stderr.String(), "usage: kman version") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
}

func TestReleaseVersionsCompareByNumberNotByText(t *testing.T) {
	for _, tc := range []struct {
		current, latest string
		newer, ok       bool
	}{
		{"v0.1.0", "v0.2.0", true, true},
		{"v0.9.0", "v0.10.0", true, true},
		{"v0.10.0", "v0.9.0", false, true},
		{"v1.0.0", "v1.0.0", false, true},
		{"v1.0.0-rc.1", "v1.0.0", true, true}, // a prerelease is older than its release
		{"v1.0.0", "v1.0.0-rc.1", false, true},
		{"0.1.0", "v0.1.1", true, true}, // a missing v is still a version
		{"(devel)", "v0.1.0", false, false},
		{"", "v0.1.0", false, false},
		{"v0.2.0-5-gabcdef", "v0.2.0", false, false}, // a build between releases
		{"v1.2", "v1.3.0", false, false},
	} {
		newer, ok := newerRelease(tc.current, tc.latest)
		if newer != tc.newer || ok != tc.ok {
			t.Errorf("newerRelease(%q, %q) = %v, %v, want %v, %v", tc.current, tc.latest, newer, ok, tc.newer, tc.ok)
		}
	}
}

// The daily check: once at startup, then every 24 hours, until stopped.
func TestTheDailyUpdateCheckRunsAtStartupThenEveryDayAndNeverBefore(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		var latest atomic.Value
		latest.Store("v0.2.0")
		u := newUpdateChecker("v0.1.0", time.Now, func(context.Context) (release, error) {
			calls.Add(1)
			tag := latest.Load().(string)
			return release{Tag: tag, URL: "https://example.test/" + tag}, nil
		})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { u.Run(ctx); close(done) }()
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatalf("%d checks at startup, want 1", calls.Load())
		}
		if st := u.Status(); !st.Enabled || !st.Available || st.Latest != "v0.2.0" || st.Current != "v0.1.0" || st.CheckedAt == nil {
			t.Errorf("Status = %+v, want an update available", st)
		}

		time.Sleep(24*time.Hour - time.Second)
		synctest.Wait()
		if calls.Load() != 1 {
			t.Errorf("%d checks just under a day later, want still 1", calls.Load())
		}
		latest.Store("v0.1.0") // the next check finds that the install is current
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if calls.Load() != 2 || u.Status().Available {
			t.Errorf("%d checks after a day, available %v, want 2 and none", calls.Load(), u.Status().Available)
		}
		cancel()
		<-done
		time.Sleep(72 * time.Hour)
		synctest.Wait()
		if calls.Load() != 2 {
			t.Errorf("%d checks, want the loop to stay stopped at 2", calls.Load())
		}
	})
}

func TestAFailedUpdateCheckIsRememberedWithoutForgettingWhatWasLearned(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var fail atomic.Bool
		u := newUpdateChecker("v0.1.0", time.Now, func(context.Context) (release, error) {
			if fail.Load() {
				return release{}, errors.New("no route to host")
			}
			return release{Tag: "v0.2.0"}, nil
		})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { u.Run(ctx); close(done) }()
		synctest.Wait()
		fail.Store(true)
		time.Sleep(24*time.Hour + time.Second)
		synctest.Wait()
		st := u.Status()
		if !st.Available || st.Latest != "v0.2.0" || st.Error != "no route to host" {
			t.Errorf("Status = %+v, want the earlier answer kept and the failure noted", st)
		}
		cancel()
		<-done
	})
}

func TestTheUpdateMetricIsOneOnlyWhenAnUpdateIsAvailable(t *testing.T) {
	u := newUpdateChecker("v0.1.0", time.Now, func(context.Context) (release, error) { return release{Tag: "v0.2.0"}, nil })
	m := updateMetric(u)
	if m.Name != "kman_update_available" || m.Samples()[0].Value != 0 {
		t.Errorf("before any check: %s = %v, want 0", m.Name, m.Samples())
	}
	u.check(t.Context())
	if got := m.Samples()[0].Value; got != 1 {
		t.Errorf("with a newer release: %v, want 1", got)
	}
}
