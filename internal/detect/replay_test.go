package detect_test

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/detect"
	"github.com/kirank55/kman/internal/events"
	"github.com/kirank55/kman/internal/policy"
	"github.com/kirank55/kman/internal/store"
)

// The replay harness feeds a scripted run of events through the real event
// sink and the detection engine, on a fake clock, and compares what came out
// (the detections, the incidents, the alerts that were queued) with a golden
// file (SPEC §17). A scenario is testdata/replay/<name>.json and its golden is
// <name>.golden next to it; `go test ./internal/detect -run Replay -update`
// rewrites the goldens, and the diff is what to review.
var update = flag.Bool("update", false, "rewrite the golden files of the replay scenarios")

// scenario is a scripted run: the keys and settings it starts with, and the
// events that follow, each written to the store as a batch of its own, as the
// event writer would flush them.
type scenario struct {
	Description string `json:"description"`
	// Start is when the run begins; each step's At is an offset from it.
	Start time.Time `json:"start"`
	Keys  []struct {
		Name   string `json:"name"`
		Canary bool   `json:"canary,omitempty"`
		Policy string `json:"policy,omitempty"`
	} `json:"keys"`
	// LearningPeriod is the install-wide setting, as a period; empty leaves the default.
	LearningPeriod string `json:"learning_period,omitempty"`
	KnownSources   []struct {
		Key   string `json:"key,omitempty"` // empty is install-wide
		Kind  string `json:"kind"`
		Value string `json:"value"`
		Label string `json:"label"`
	} `json:"known_sources,omitempty"`
	Steps []step `json:"steps"`
}

// step is one event. Repeat writes it n times, a second apart, with {i} in the
// client replaced by the count.
type step struct {
	At      string `json:"at"`
	Key     string `json:"key"`
	Kind    string `json:"kind,omitempty"` // request (the default), blocked or session
	IP      string `json:"ip,omitempty"`
	Country string `json:"country,omitempty"`
	ASN     int64  `json:"asn,omitempty"`
	ASOrg   string `json:"as_org,omitempty"`
	Client  string `json:"client,omitempty"`
	Code    string `json:"code,omitempty"` // a refusal's code
	Repeat  int    `json:"repeat,omitempty"`
}

func (s step) describe() string {
	kind := s.Kind
	if kind == "" {
		kind = "request"
	}
	out := fmt.Sprintf("%-8s %-7s %-8s", s.At, s.Key, kind)
	if s.Code != "" {
		out += " (" + s.Code + ")"
	}
	if s.IP != "" {
		out += " " + s.IP
	}
	if s.Country != "" {
		out += " " + s.Country
	}
	if s.ASN != 0 {
		out += fmt.Sprintf(" AS%d", s.ASN)
	}
	if s.Client != "" {
		out += " " + s.Client
	}
	if s.Repeat > 1 {
		out += fmt.Sprintf(" x%d", s.Repeat)
	}
	return out
}

func TestReplayScenariosMatchTheirGoldenFiles(t *testing.T) {
	files, err := filepath.Glob("testdata/replay/*.json")
	if err != nil || len(files) == 0 {
		t.Fatalf("no scenarios: %v", err)
	}
	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".json")
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var sc scenario
			dec := json.NewDecoder(strings.NewReader(string(raw)))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&sc); err != nil {
				t.Fatalf("%s: %v", file, err)
			}
			got := replay(t, name, sc)
			golden := strings.TrimSuffix(file, ".json") + ".golden"
			if *update {
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run with -update to write it)", err)
			}
			if got != string(want) {
				t.Errorf("%s differs from its golden file; run with -update and review the diff.\n--- got\n%s\n--- want\n%s", name, got, want)
			}
		})
	}
}

// replay runs sc and returns what it did, as the golden file holds it.
func replay(t *testing.T, name string, sc scenario) string {
	t.Helper()
	ctx := context.Background()
	keyID := map[string]string{}
	keyName := map[string]string{}
	var keys []store.VirtualKey
	for i, k := range sc.Keys {
		id := fmt.Sprintf("%08x", 0xa0000001+i)
		keyID[k.Name], keyName[id] = id, k.Name
		vk := store.VirtualKey{ID: id, Name: k.Name, CredentialID: "cr_1", PolicyJSON: k.Policy}
		if k.Canary {
			vk.Kind, vk.CredentialID = store.KeyCanary, ""
		}
		keys = append(keys, vk)
	}
	st := seedStore(t, keys...)
	var now time.Time
	alerts := &alerter{}
	engine := detect.New(detect.Options{Store: st, Now: func() time.Time { return now }, Alerts: alerts})
	sink := events.SQLite(st, engine)

	err := st.Do(ctx, func(tx *store.Tx) error {
		if sc.LearningPeriod != "" {
			d, err := policy.ParsePeriod(sc.LearningPeriod)
			if err != nil {
				return err
			}
			if _, err := tx.PutSetting(store.Setting{Key: store.SettingLearningPeriod, ValueJSON: fmt.Sprint(int64(d / time.Second)), UpdatedAt: sc.Start}); err != nil {
				return err
			}
		}
		for _, ks := range sc.KnownSources {
			if _, err := tx.CreateKnownSource(store.KnownSource{Scope: keyID[ks.Key], Kind: ks.Kind, Value: ks.Value, Label: ks.Label, CreatedBy: "owner", CreatedAt: sc.Start}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	fmt.Fprintf(&out, "scenario: %s\n%s\n", name, sc.Description)
	seenDetections, seenAlerts := int64(0), 0
	for i, s := range sc.Steps {
		offset, err := parseOffset(s.At)
		if err != nil {
			t.Fatalf("step %d: at %q: %v", i+1, s.At, err)
		}
		id, ok := keyID[s.Key]
		if !ok {
			t.Fatalf("step %d: no key %q", i+1, s.Key)
		}
		n := max(s.Repeat, 1)
		for j := range n {
			e := events.Event{Time: sc.Start.Add(offset + time.Duration(j)*time.Second), Kind: events.Kind(s.Kind), KeyID: id, Country: s.Country, ASN: s.ASN, ASOrg: s.ASOrg,
				Client: strings.ReplaceAll(s.Client, "{i}", fmt.Sprint(j)), BlockedCode: s.Code, Status: 200}
			if e.Kind == "" {
				e.Kind = events.KindRequest
			}
			if e.Kind == events.KindBlocked {
				e.Status = 403
			}
			if s.IP != "" {
				e.ClientIP = netip.MustParseAddr(s.IP)
			}
			now = e.Time
			if err := sink.AddEvents(ctx, []events.Event{e}); err != nil {
				t.Fatalf("step %d: %v", i+1, err)
			}
		}
		fmt.Fprintf(&out, "\nstep %d  %s\n", i+1, s.describe())
		var ds []store.Detection
		err = st.Do(ctx, func(tx *store.Tx) error {
			incs, err := tx.Incidents(store.IncidentFilter{})
			if err != nil {
				return err
			}
			for _, inc := range incs {
				all, err := tx.Detections(inc.ID, 1000)
				if err != nil {
					return err
				}
				for _, d := range all {
					if d.ID > seenDetections {
						ds = append(ds, d)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		slices.SortFunc(ds, func(a, b store.Detection) int { return int(a.ID - b.ID) })
		for _, d := range ds {
			seenDetections = max(seenDetections, d.ID)
			var ev store.Evidence
			_ = json.Unmarshal([]byte(d.EvidenceJSON), &ev)
			fmt.Fprintf(&out, "  detection #%d incident %d: %s %s\n", d.ID, d.IncidentID, d.Rule, d.Severity)
			for _, f := range []struct{ label, v string }{{"observed", ev.Observed}, {"baseline", ev.Baseline}, {"usual", ev.Usual}, {"detail", ev.Detail},
				{"from", ev.Address}, {"network", ev.Network}, {"country", ev.Country}, {"as", asText(ev)}, {"client", ev.Client}} {
				if f.v != "" {
					fmt.Fprintf(&out, "      %-8s %s\n", f.label, f.v)
				}
			}
		}
		for _, a := range alerts.got()[seenAlerts:] {
			fmt.Fprintf(&out, "  alert: incident %d %s, %s, rules %v, about %s\n", a.inc.ID, a.change, a.inc.Severity, a.inc.Rules(), a.det.Rule)
		}
		seenAlerts = len(alerts.got())
		if len(ds) == 0 {
			fmt.Fprintln(&out, "  nothing raised")
		}
	}

	fmt.Fprintf(&out, "\nincidents at the end:\n")
	err = st.Do(ctx, func(tx *store.Tx) error {
		incs, err := tx.Incidents(store.IncidentFilter{})
		if err != nil {
			return err
		}
		if len(incs) == 0 {
			fmt.Fprintln(&out, "  none")
		}
		for i := len(incs) - 1; i >= 0; i-- {
			inc := incs[i]
			fmt.Fprintf(&out, "  #%d %s %s %s rules %v, %d detections, opened %s, last fired %s\n", inc.ID, keyName[inc.KeyID], inc.State, inc.Severity, inc.Rules(),
				inc.Detections, inc.OpenedAt.Sub(sc.Start), inc.LastFiredAt.Sub(sc.Start))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func asText(ev store.Evidence) string {
	if ev.ASN == 0 {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("AS%d %s", ev.ASN, ev.ASOrg))
}

// parseOffset reads an offset from the scenario's start: days, then a Go
// duration, either optional, such as 8d, 8d1m or 191h59m.
func parseOffset(s string) (time.Duration, error) {
	var d time.Duration
	if days, rest, ok := strings.Cut(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil {
			return 0, fmt.Errorf("%q: %w", s, err)
		}
		d, s = time.Duration(n)*24*time.Hour, rest
	}
	if s == "" {
		return d, nil
	}
	rest, err := time.ParseDuration(s)
	return d + rest, err
}
