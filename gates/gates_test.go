package gates_test

import (
	"slices"
	"testing"

	"github.com/kirank55/kman/gates"
)

// planted loads the fixture module under testdata/planted, whose packages
// each break one gate on purpose.
func planted(t *testing.T) []gates.Package {
	t.Helper()
	pkgs, err := gates.Load("testdata/planted")
	if err != nil {
		t.Fatal(err)
	}
	return pkgs
}

// repo loads this repository's own packages.
func repo(t *testing.T) []gates.Package {
	t.Helper()
	pkgs, err := gates.Load("..")
	if err != nil {
		t.Fatal(err)
	}
	return pkgs
}

func assertViolations(t *testing.T, got []gates.Violation, want []string) {
	t.Helper()
	var msgs []string
	for _, v := range got {
		msgs = append(msgs, v.String())
	}
	slices.Sort(msgs)
	slices.Sort(want)
	if !slices.Equal(msgs, want) {
		t.Errorf("violations:\n got  %q\n want %q", msgs, want)
	}
}

func TestImportRuleCatchesPlantedViolations(t *testing.T) {
	assertViolations(t, gates.CheckImports(planted(t)), []string{
		"internal/cli: imports internal/store (a transport may import only admin and token)",
		"internal/gateway: imports internal/admin (gateway may import only registry, policy, limiter, meter, vault, profiles, events, sessions and token)",
		"internal/policy: imports internal/cli (nothing may import a transport)",
		"internal/token: imports internal/store (token is a leaf and imports no internal package)",
	})
}

func TestRepoKeepsTheImportRule(t *testing.T) {
	assertViolations(t, gates.CheckImports(repo(t)), nil)
}

func TestModuleMapCatchesPlantedViolations(t *testing.T) {
	assertViolations(t, gates.CheckModules(planted(t)), []string{
		"internal/extra: is not a module in SPEC §2",
		"internal/meter: has no package doc comment",
		"internal/profiles: is missing",
		"internal/pricing: is missing",
		"internal/registry: is missing",
		"internal/vault: is missing",
		"internal/detect: is missing",
		"internal/alerts: is missing",
		"internal/sessions: is missing",
		"internal/adminapi: is missing",
		"internal/dashboard: is missing",
	})
}

func TestRepoHasEveryModule(t *testing.T) {
	assertViolations(t, gates.CheckModules(repo(t)), nil)
}

func TestTestedGateCatchesPlantedViolations(t *testing.T) {
	assertViolations(t, gates.CheckTested(planted(t)), []string{
		"internal/events: has no Test, Fuzz, Benchmark or Example function",
		"internal/limiter: has no tests",
	})
}

func TestRepoHasNoUntestedPackage(t *testing.T) {
	assertViolations(t, gates.CheckTested(repo(t)), nil)
}

func TestClockGateCatchesPlantedViolations(t *testing.T) {
	assertViolations(t, gates.CheckClock(planted(t)), []string{
		"internal/cli: clock.go:5 uses time.Since (only main reads the clock; take the time as an argument)",
		"internal/gateway: clock.go:5 uses time.Now (only main reads the clock; take the time as an argument)",
		"internal/gateway: dot.go:3 dot-imports time, which hides clock reads from this gate",
		"internal/admin: sleep_test.go:8 uses time.Sleep outside testing/synctest (timer-driven tests run in a synctest bubble)",
	})
}

func TestRepoKeepsTheClockSeam(t *testing.T) {
	assertViolations(t, gates.CheckClock(repo(t)), nil)
}

func TestOfflineGateCatchesPlantedViolations(t *testing.T) {
	assertViolations(t, gates.CheckOffline("internal/pricing", []string{"encoding/json", "net", "net/http", "os/exec"}), []string{
		"internal/pricing: depends on net; price data is never fetched at runtime (ADR 0007)",
		"internal/pricing: depends on net/http; price data is never fetched at runtime (ADR 0007)",
		"internal/pricing: depends on os/exec; price data is never fetched at runtime (ADR 0007)",
	})
}

// TestPricingCannotReachTheNetwork is the invariant of ADR 0007 at its root: the
// module holding the price table links no code that could fetch one.
func TestPricingCannotReachTheNetwork(t *testing.T) {
	deps, err := gates.Deps("..", "./internal/pricing")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(deps, "encoding/json") {
		t.Fatalf("the dependency list looks wrong: %q", deps)
	}
	assertViolations(t, gates.CheckOffline("internal/pricing", deps), nil)
}

func TestOfflineGateGivesTheReasonItWasAskedFor(t *testing.T) {
	assertViolations(t, gates.CheckOfflineBecause("internal/policy/geoip", []string{"encoding/json", "net/http"}, "why"), []string{
		"internal/policy/geoip: depends on net/http; why",
	})
}

// TestGeoIPCannotReachTheNetwork is ADR 0008 at its root: the code that reads
// GeoIP databases links nothing that could download one. kman geoip update, in
// cmd/kman, is the one place that does.
func TestGeoIPCannotReachTheNetwork(t *testing.T) {
	deps, err := gates.Deps("..", "./internal/policy/geoip")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(deps, "net/netip") {
		t.Fatalf("the dependency list looks wrong: %q", deps)
	}
	assertViolations(t, gates.CheckOfflineBecause("internal/policy/geoip", deps, "GeoIP databases are downloaded only by kman geoip update (ADR 0008)"), nil)
}
