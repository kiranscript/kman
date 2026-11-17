package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/store"
	"github.com/kirank55/kman/internal/vault"
)

// served is a kman serve running in this process on free loopback ports.
type served struct {
	home  rekeyHome
	admin string // http://host:port
	token string
	http  *http.Client
}

// serveHome runs serve over a fresh install whose kman.yaml has extra appended,
// with s as the process. It stops when the test ends.
func serveHome(t *testing.T, s *sys, extra string, before func(rekeyHome)) *served {
	t.Helper()
	h := newRekeyHome(t, nil, nil)
	if before != nil {
		before(h)
	}
	cfgPath := filepath.Join(h.config, configFile)
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	yaml := strings.NewReplacer("proxy_listen: 127.0.0.1:8080", "proxy_listen: 127.0.0.1:0", "admin_listen: 127.0.0.1:8081", "admin_listen: 127.0.0.1:0").Replace(string(raw))
	if err := os.WriteFile(cfgPath, []byte(yaml+extra), 0o600); err != nil {
		t.Fatal(err)
	}
	var token string
	h.withService(t, vault.FileSource(h.keyFile()), func(ctx context.Context, svc *admin.Service) {
		created, err := svc.CreateAdminToken(ctx, admin.CreateAdminTokenRequest{Name: "tester"})
		if err != nil {
			t.Fatal(err)
		}
		token = created.Secret
	})

	var logs syncBuffer
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, s, slog.New(slog.NewTextHandler(&logs, nil)), h.config, vault.Options{AllowDebug: true})
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("serve: %v\n%s", err, logs.String())
		}
	})
	addr := regexp.MustCompile(`msg="admin listening" addr=(\S+)`)
	var m []string
	for range 200 {
		if m = addr.FindStringSubmatch(logs.String()); m != nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("serve returned early: %v\n%s", err, logs.String())
		case <-time.After(25 * time.Millisecond):
		}
	}
	if m == nil {
		t.Fatalf("serve never listened:\n%s", logs.String())
	}
	return &served{home: h, admin: "http://" + m[1], token: token, http: &http.Client{Timeout: 5 * time.Second}}
}

func (sv *served) get(t *testing.T, path string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", sv.admin+path, nil)
	req.Header.Set("Authorization", "Bearer "+sv.token)
	resp, err := sv.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// updateStatus polls the admin API until ok says the status is what is wanted.
func (sv *served) updateStatus(t *testing.T, ok func(admin.UpdateStatus) bool) admin.UpdateStatus {
	t.Helper()
	var st admin.UpdateStatus
	for range 200 {
		code, body := sv.get(t, "/admin/v1/update")
		if code != 200 {
			t.Fatalf("GET /admin/v1/update: %d %s", code, body)
		}
		if err := json.Unmarshal([]byte(body), &st); err != nil {
			t.Fatal(err)
		}
		if ok(st) {
			return st
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("the update status never became what was wanted: %+v", st)
	return st
}

func TestServeMakesNoNetworkCallUnlessUpdateCheckIsOn(t *testing.T) {
	withVersion(t, "v0.1.0")
	rs := newReleasesServer(t, 200, latestV02)
	s, _, _ := testSys(t, nil)
	s.http = rs.client
	sv := serveHome(t, s, "", nil)

	st := sv.updateStatus(t, func(admin.UpdateStatus) bool { return true })
	if st.Enabled || st.Available {
		t.Errorf("update status with update_check off = %+v", st)
	}
	time.Sleep(300 * time.Millisecond) // a check that is going to happen has happened by now
	if n := rs.hits.Load(); n != 0 {
		t.Errorf("serve made %d requests to the releases API with update_check off (ADR 0008)", n)
	}
	if _, body := sv.get(t, "/metrics"); strings.Contains(body, "kman_update_available") {
		t.Error("/metrics has kman_update_available with update_check off")
	}
}

func TestServeWithUpdateCheckOnChecksAtStartupAndReportsItInTheStatusAndTheMetric(t *testing.T) {
	withVersion(t, "v0.1.0")
	rs := newReleasesServer(t, 200, latestV02)
	s, _, _ := testSys(t, nil)
	s.http = rs.client
	sv := serveHome(t, s, "update_check: true\n", nil)

	st := sv.updateStatus(t, func(st admin.UpdateStatus) bool { return st.Available })
	if !st.Enabled || st.Current != "v0.1.0" || st.Latest != "v0.2.0" || st.CheckedAt == nil {
		t.Errorf("update status = %+v", st)
	}
	if _, body := sv.get(t, "/metrics"); !regexp.MustCompile(`(?m)^kman_update_available 1$`).MatchString(body) {
		t.Errorf("/metrics lacks kman_update_available 1:\n%s", grepLines(body, "update"))
	}
	if n := rs.hits.Load(); n != 1 {
		t.Errorf("serve asked %d times at startup, want once", n)
	}
}

func grepLines(s, sub string) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, sub) {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// seedEvents puts one event 60 days old and one a day old in the install.
func seedEvents(now time.Time) func(rekeyHome) {
	return func(h rekeyHome) {
		st, err := store.Open(context.Background(), store.Options{Dir: h.data, Now: now})
		if err != nil {
			panic(err)
		}
		defer st.Close()
		err = st.Do(context.Background(), func(tx *store.Tx) error {
			return tx.AddEvents([]store.EventRow{
				{Time: now.Add(-60 * 24 * time.Hour), Kind: "request", KeyID: "kkkk1111", Status: 200},
				{Time: now.Add(-24 * time.Hour), Kind: "request", KeyID: "kkkk1111", Status: 200},
			})
		})
		if err != nil {
			panic(err)
		}
	}
}

func eventCount(t *testing.T, h rekeyHome, now time.Time) int {
	t.Helper()
	st, err := store.Open(t.Context(), store.Options{Dir: h.data, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var n int
	if err := st.Do(t.Context(), func(tx *store.Tx) error {
		es, err := tx.EventsSince(time.Time{})
		n = len(es)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestServePrunesToTheDefaultRetentionAtStartupAndKeepEverythingDoesNot(t *testing.T) {
	for name, tc := range map[string]struct {
		extra string
		want  int
	}{
		"the default":     {"", 1},
		"keep_everything": {"retention: keep_everything\n", 2},
	} {
		t.Run(name, func(t *testing.T) {
			s, _, _ := testSys(t, nil)
			sv := serveHome(t, s, tc.extra, seedEvents(s.now()))
			var code int
			var body string
			for range 200 { // retention runs at startup, soon after the listeners are up
				code, body = sv.get(t, "/admin/v1/audit?limit=1000")
				if tc.want == 2 || strings.Contains(body, "system:retention") {
					break
				}
				time.Sleep(25 * time.Millisecond)
			}
			if code != 200 {
				t.Fatalf("audit: %d", code)
			}
			if got := strings.Contains(body, "system:retention"); got != (tc.want == 1) {
				t.Errorf("a retention entry in the audit log = %v, want %v", got, tc.want == 1)
			}
			time.Sleep(200 * time.Millisecond) // a pass that is going to run has run
			if n := eventCount(t, sv.home, s.now()); n != tc.want {
				t.Errorf("%d events left, want %d", n, tc.want)
			}
		})
	}
}
