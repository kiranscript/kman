package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/events"
)

// releasesURL is the one address kman asks about updates (ADR 0008). Only
// `kman version --check` and, when the operator opted in with update_check,
// the daily check in `kman serve` ever request it.
const releasesURL = "https://api.github.com/repos/kirank55/kman/releases/latest"

// checkEvery is how often the opt-in check asks.
const checkEvery = 24 * time.Hour

// checkTimeout bounds one ask.
const checkTimeout = 15 * time.Second

// maxReleaseBody is the most of the answer kman reads.
const maxReleaseBody = 1 << 20

// release is a published kman release.
type release struct {
	Tag string
	URL string
}

// releaseClient is the client the checks use: the operator's proxy settings
// apply, and nothing is sent but the request.
func releaseClient(s *sys) *http.Client {
	if s.http != nil {
		return s.http
	}
	return &http.Client{Timeout: checkTimeout}
}

// fetchLatest asks GitHub for the latest release. The request carries the program
// and its version as the User-Agent, which GitHub requires, and nothing else: no
// install id, no key, no token, no query.
func fetchLatest(ctx context.Context, hc *http.Client, current string) (release, error) {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releasesURL, nil)
	if err != nil {
		return release{}, err
	}
	ua := "kman"
	if current != "" {
		ua += "/" + current
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := hc.Do(req)
	if err != nil {
		return release{}, fmt.Errorf("ask GitHub for the latest release: %w", unwrapURL(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return release{}, fmt.Errorf("GitHub answered %s for the latest release", resp.Status)
	}
	var body struct {
		Tag string `json:"tag_name"`
		URL string `json:"html_url"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxReleaseBody)).Decode(&body); err != nil {
		return release{}, fmt.Errorf("GitHub's answer is not the releases JSON: %w", err)
	}
	if body.Tag == "" {
		return release{}, errors.New("GitHub's answer names no release")
	}
	if _, ok := parseRelease(body.Tag); !ok {
		return release{}, fmt.Errorf("GitHub's latest release %q is not a version", body.Tag)
	}
	return release{Tag: body.Tag, URL: body.URL}, nil
}

func unwrapURL(err error) error {
	if ue, ok := errors.AsType[*url.Error](err); ok {
		return ue.Err
	}
	return err
}

// semver is a release version, such as v1.2.3 or v1.0.0-rc.1.
var semver = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z.]+))?$`)

type releaseVersion struct {
	major, minor, patch int
	pre                 string
}

func parseRelease(v string) (releaseVersion, bool) {
	m := semver.FindStringSubmatch(v)
	if m == nil {
		return releaseVersion{}, false
	}
	n := func(s string) int { i, _ := strconv.Atoi(s); return i }
	return releaseVersion{n(m[1]), n(m[2]), n(m[3]), m[4]}, true
}

// newerRelease reports whether latest is a newer release than current. It is not
// ok when they cannot be compared: a build that is not a release, such as one
// from `go build` ("(devel)") or one between tags (v0.2.0-5-gabcdef), where
// "newer" would be a guess.
func newerRelease(current, latest string) (newer, ok bool) {
	c, cok := parseRelease(current)
	l, lok := parseRelease(latest)
	if !cok || !lok || isBetweenTags(c.pre) {
		return false, false
	}
	for _, d := range [][2]int{{l.major, c.major}, {l.minor, c.minor}, {l.patch, c.patch}} {
		if d[0] != d[1] {
			return d[0] > d[1], true
		}
	}
	switch {
	case c.pre == l.pre:
		return false, true
	case c.pre == "": // current is the release itself
		return false, true
	case l.pre == "": // latest is the release a prerelease leads to
		return true, true
	}
	return l.pre > c.pre, true
}

// isBetweenTags reports whether a prerelease part is `git describe`'s count of
// commits after a tag, such as 5-gabcdef.
var describeSuffix = regexp.MustCompile(`^[0-9]+-g[0-9a-f]+`)

func isBetweenTags(pre string) bool { return describeSuffix.MatchString(pre) }

const versionUsage = "usage: kman version [--check] [--json]"

// cmdVersion prints the version, the commit and the build date. With --check it
// then asks GitHub for the latest release, once, which is the only network call
// the command makes and only when asked.
func cmdVersion(ctx context.Context, args []string, s *sys) int {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(s.stderr)
	fs.Usage = func() {
		fmt.Fprintf(s.stderr, "%s\n\nPrints the version, the commit and the build date. With --check it also asks the GitHub releases API for the latest release, "+
			"once: the only time this command uses the network. kman never checks by itself unless update_check: true is set in kman.yaml.\n", versionUsage)
		fs.PrintDefaults()
	}
	check := fs.Bool("check", false, "ask GitHub whether a newer release exists")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(s.stderr, versionUsage)
		return 1
	}
	v, c, d, _ := stamp()
	out := map[string]any{"version": v, "commit": c, "built": d}
	if !*asJSON {
		printVersion(s.stdout)
	}
	if !*check {
		if *asJSON {
			return printJSONTo(s, out)
		}
		return 0
	}
	latest, err := fetchLatest(ctx, releaseClient(s), v)
	if err != nil {
		if *asJSON {
			printJSONTo(s, out)
		}
		fmt.Fprintln(s.stderr, "kman version --check:", err)
		return 1
	}
	newer, comparable := newerRelease(v, latest.Tag)
	out["latest"], out["url"], out["update_available"] = latest.Tag, latest.URL, newer
	if *asJSON {
		return printJSONTo(s, out)
	}
	switch {
	case !comparable:
		fmt.Fprintf(s.stdout, "\nThis build is not a release, so it cannot be compared. The latest release is %s: %s\n", latest.Tag, latest.URL)
	case newer:
		fmt.Fprintf(s.stdout, "\nA newer kman is available: %s (you have %s). %s\n", latest.Tag, v, latest.URL)
	default:
		fmt.Fprintf(s.stdout, "\nkman is up to date: %s is the latest release.\n", latest.Tag)
	}
	return 0
}

func printJSONTo(s *sys, v any) int {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintln(s.stderr, "kman version:", err)
		return 1
	}
	fmt.Fprintf(s.stdout, "%s\n", b)
	return 0
}

// updateChecker is the opt-in daily check (update_check: true): it asks once
// at startup and then every day, and remembers the answer for the status summary,
// the dashboard and kman_update_available. It never runs unless kman serve
// starts it, and kman serve starts it only when the operator turned it on.
type updateChecker struct {
	current string
	now     func() time.Time
	fetch   func(ctx context.Context) (release, error)

	mu     sync.Mutex
	latest release
	at     time.Time
	err    error
}

func newUpdateChecker(current string, now func() time.Time, fetch func(context.Context) (release, error)) *updateChecker {
	return &updateChecker{current: current, now: now, fetch: fetch}
}

// Run checks at startup and then daily until ctx ends.
func (u *updateChecker) Run(ctx context.Context) {
	for {
		u.check(ctx)
		t := time.NewTimer(checkEvery)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}

// check asks once. A failure is remembered, and what an earlier check found
// is kept.
func (u *updateChecker) check(ctx context.Context) {
	r, err := u.fetch(ctx)
	if ctx.Err() != nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.at, u.err = u.now(), err
	if err == nil {
		u.latest = r
	}
}

// Status is what the checks have learned, as the admin API reports it.
func (u *updateChecker) Status() admin.UpdateStatus {
	u.mu.Lock()
	defer u.mu.Unlock()
	st := admin.UpdateStatus{Enabled: true, Current: u.current, Latest: u.latest.Tag, URL: u.latest.URL}
	if !u.at.IsZero() {
		at := u.at
		st.CheckedAt = &at
	}
	if u.err != nil {
		st.Error = strings.TrimSpace(u.err.Error())
	}
	if u.latest.Tag != "" {
		st.Available, _ = newerRelease(u.current, u.latest.Tag)
	}
	return st
}

// updateMetric is kman_update_available: 1 when a newer release is known.
func updateMetric(u *updateChecker) events.Metric {
	return events.Metric{
		Name: "kman_update_available", Type: events.Gauge,
		Help: "1 when the opt-in update check has found a newer kman release, else 0.",
		Samples: func() []events.Sample {
			v := 0.0
			if u.Status().Available {
				v = 1
			}
			return []events.Sample{{Value: v}}
		},
	}
}
