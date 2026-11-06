package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/admin"
)

// addAcme adds a custom upstream with no credential.
func (in *install) addAcme(t *testing.T, name string) {
	t.Helper()
	in.kman(t, "upstream", "add", name, "--host", "api.acme.test", "--path", "* /**", "--no-credential")
}

func (in *install) overrides(t *testing.T) []admin.PriceOverride {
	t.Helper()
	var got admin.PriceList
	in.kmanJSON(t, &got, "price", "list")
	return got.Overrides
}

func TestPriceSetInstallWide(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	out := in.kman(t, "price", "set", "new-model", "--input", "2.50", "--output", "$15", "--max-output", "128000")
	if !strings.Contains(out, "new-model") || !strings.Contains(out, "install") {
		t.Errorf("said:\n%s", out)
	}
	got := in.overrides(t)
	if len(got) != 1 || got[0].Upstream != "" || got[0].Model != "new-model" || got[0].InputMicro != 2500000 || got[0].OutputMicro != 15000000 || got[0].MaxOutput != 128000 {
		t.Errorf("overrides = %+v", got)
	}
	if got[0].Actor != "owner" {
		t.Errorf("actor = %q", got[0].Actor)
	}
}

func TestPriceSetOnAnUpstream(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	in.addAcme(t, "acme")
	out := in.kman(t, "price", "set", "m", "--upstream", "acme", "--input", "1", "--output", "2", "--max-output", "10")
	if !strings.Contains(out, "acme") {
		t.Errorf("said:\n%s", out)
	}
	got := in.overrides(t)
	if len(got) != 1 || got[0].Upstream != "acme" || got[0].UpstreamID == "" {
		t.Errorf("overrides = %+v", got)
	}
}

func TestPriceSetTakesTheOptionalPrices(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	in.kman(t, "price", "set", "m", "--input", "4", "--output", "8", "--max-output", "10", "--cached", "0.5", "--cache-write", "5",
		"--cache-write-1h", "8", "--audio-input", "32", "--audio-output", "64", "--audio-cached", "0")
	got := in.overrides(t)[0]
	for name, tt := range map[string]struct {
		p    *int64
		want int64
	}{
		"cached":         {got.CachedMicro, 500000},
		"cache write":    {got.CacheWriteMicro, 5000000},
		"cache write 1h": {got.CacheWrite1hMicro, 8000000},
		"audio input":    {got.AudioInputMicro, 32000000},
		"audio output":   {got.AudioOutputMicro, 64000000},
		"audio cached":   {got.AudioCachedMicro, 0},
	} {
		if tt.p == nil || *tt.p != tt.want {
			t.Errorf("%s = %v, want %d", name, tt.p, tt.want)
		}
	}
}

func TestPriceSetRequiresInputOutputAndMaxOutput(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{"no input", []string{"--output", "1", "--max-output", "1"}, "--input"},
		{"no output", []string{"--input", "1", "--max-output", "1"}, "--output"},
		{"no max output", []string{"--input", "1", "--output", "1"}, "--max-output"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := in.run(t, nil, "", append([]string{"price", "set", "m"}, tt.args...)...)
			if r.code != 1 || !strings.Contains(r.stderr, tt.want) {
				t.Errorf("exit %d, stderr %q", r.code, r.stderr)
			}
		})
	}
	if got := in.overrides(t); len(got) != 0 {
		t.Errorf("a refused set left %+v", got)
	}
}

func TestPriceSetRefusesWhatIsNotAPrice(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	for _, bad := range []string{"", "abc", "-1", "1.2.3", "1.", ".5x", "0.0000001", "1e3", "$", "99999999999999999999", "9223372036854"} {
		r := in.run(t, nil, "", "price", "set", "m", "--input", bad, "--output", "1", "--max-output", "1")
		if r.code != 1 || !strings.Contains(r.stderr, "--input") {
			t.Errorf("--input %q: exit %d, stderr %q", bad, r.code, r.stderr)
		}
	}
	if r := in.run(t, nil, "", "price", "set", "m", "--input", "1", "--output", "1", "--max-output", "many"); r.code != 1 {
		t.Errorf("--max-output many: exit %d", r.code)
	}
	if r := in.run(t, nil, "", "price", "set", "--input", "1", "--output", "1", "--max-output", "1"); r.code != 1 || !strings.Contains(r.stderr, "usage: kman price set") {
		t.Errorf("no model: exit %d, stderr %q", r.code, r.stderr)
	}
}

func TestPriceSetSaysWhatItHides(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	out := in.kman(t, "price", "set", "gpt-5.4", "--input", "1", "--output", "2", "--max-output", "10")
	if !strings.Contains(out, "hides the built-in entry in global") {
		t.Errorf("said:\n%s", out)
	}
	if out := in.kman(t, "price", "set", "fresh", "--input", "1", "--output", "2", "--max-output", "10"); strings.Contains(out, "hides") {
		t.Errorf("said:\n%s", out)
	}
}

func TestPriceListMarksOverridesThatHideABuiltinEntry(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	in.addAcme(t, "acme")
	in.kman(t, "price", "set", "gpt-5.4", "--input", "3", "--output", "9", "--max-output", "2000", "--cached", "1")
	in.kman(t, "price", "set", "fresh", "--upstream", "acme", "--input", "1", "--output", "2", "--max-output", "10")
	out := in.kman(t, "price", "list")
	for _, line := range []string{
		"install        gpt-5.4  $3     $9      $1      2000        hides built-in: global\n",
		"upstream acme  fresh    $1     $2              10", // and no note
	} {
		if !strings.Contains(out, line) {
			t.Errorf("lacks %q in:\n%s", line, out)
		}
	}
	if strings.Contains(out, "10          hides") {
		t.Errorf("an override with nothing to hide is marked:\n%s", out)
	}
	// The built-in table still shows what is hidden.
	if !strings.Contains(out, "global   gpt-5.4              $2.50") {
		t.Errorf("the built-in row went:\n%s", out)
	}
}

func TestPriceListWithNoOverridesDoesNotMentionThem(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	if out := in.kman(t, "price", "list"); strings.Contains(strings.ToLower(out), "override") {
		t.Errorf("list:\n%s", out)
	}
}

func TestPriceListFiltersOverrides(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	in.kman(t, "price", "set", "alpha", "--input", "1", "--output", "2", "--max-output", "10")
	in.kman(t, "price", "set", "beta", "--input", "1", "--output", "2", "--max-output", "10")
	out := in.kman(t, "price", "list", "--model", "alp")
	if !strings.Contains(out, "alpha") || strings.Contains(out, "beta") {
		t.Errorf("--model alp:\n%s", out)
	}
}

func TestPriceRm(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	in.addAcme(t, "acme")
	in.kman(t, "price", "set", "m", "--input", "1", "--output", "2", "--max-output", "10")
	in.kman(t, "price", "set", "m", "--upstream", "acme", "--input", "5", "--output", "6", "--max-output", "10")
	if out := in.kman(t, "price", "rm", "m", "--upstream", "acme"); !strings.Contains(out, "acme") {
		t.Errorf("said:\n%s", out)
	}
	got := in.overrides(t)
	if len(got) != 1 || got[0].Upstream != "" {
		t.Errorf("after rm --upstream = %+v", got)
	}
	in.kman(t, "price", "rm", "m")
	if got := in.overrides(t); len(got) != 0 {
		t.Errorf("after rm = %+v", got)
	}
	if r := in.run(t, nil, "", "price", "rm", "m"); r.code != 1 || !strings.Contains(r.stderr, "does not exist") {
		t.Errorf("rm again: exit %d, stderr %q", r.code, r.stderr)
	}
	if r := in.run(t, nil, "", "price", "rm"); r.code != 1 || !strings.Contains(r.stderr, "usage: kman price rm") {
		t.Errorf("rm with no model: exit %d, stderr %q", r.code, r.stderr)
	}
}

func TestPriceChangesReachTheAuditLog(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	in.kman(t, "price", "set", "m", "--input", "1", "--output", "2", "--max-output", "10")
	in.kman(t, "price", "rm", "m")
	var page admin.AuditPage
	in.kmanJSON(t, &page, "audit", "--limit", "2")
	if len(page.Entries) != 2 || page.Entries[0].Action != "price_override.set" || page.Entries[1].Action != "price_override.delete" ||
		page.Entries[0].Actor != "owner" || page.Entries[0].TargetID != "install/m" {
		t.Errorf("audit = %+v", page.Entries)
	}
}

func TestPriceImportFromAFile(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	in.addAcme(t, "acme")
	path := filepath.Join(t.TempDir(), "prices.json")
	body := `{"overrides": [
		{"model": "a", "input_micro": 1000000, "output_micro": 2000000, "max_output": 10, "cached_micro": 0},
		{"upstream": "acme", "model": "b", "input_micro": 3000000, "output_micro": 4000000, "max_output": 20}
	]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if out := in.kman(t, "price", "import", path); !strings.Contains(out, "2 changed, 0 unchanged") {
		t.Errorf("said:\n%s", out)
	}
	got := in.overrides(t)
	if len(got) != 2 || got[0].Model != "a" || got[0].CachedMicro == nil || got[1].Upstream != "acme" || got[1].MaxOutput != 20 {
		t.Errorf("overrides = %+v", got)
	}
	if out := in.kman(t, "price", "import", path); !strings.Contains(out, "0 changed, 2 unchanged") {
		t.Errorf("said the second time:\n%s", out)
	}
}

func TestPriceListJSONImportsBackUnchanged(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	in.addAcme(t, "acme")
	in.kman(t, "price", "set", "gpt-5.4", "--input", "3", "--output", "9", "--max-output", "2000", "--cached", "0")
	in.kman(t, "price", "set", "fresh", "--upstream", "acme", "--input", "1", "--output", "2", "--max-output", "10")
	exported := in.kman(t, "price", "list", "--json")
	var result admin.PriceOverrideImportResult
	r := in.run(t, nil, exported, "price", "import", "-", "--json")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	if err := json.Unmarshal([]byte(r.stdout), &result); err != nil {
		t.Fatalf("%v: %s", err, r.stdout)
	}
	if result.Changed != 0 || result.Unchanged != 2 {
		t.Errorf("result = %+v", result)
	}
}

func TestPriceImportRefuses(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	for _, tt := range []struct {
		name, body, want string
	}{
		{"not JSON", "nope", "JSON"},
		{"a misspelt price, which would silently bill at another", `{"overrides": [{"model": "a", "input_micro": 1, "output_micro": 1, "max_output": 1, "cached_micor": 1}]}`, "cached_micor"},
		{"a price in dollars", `{"overrides": [{"model": "a", "input_micro": 2.5, "output_micro": 1, "max_output": 1}]}`, "input_micro"},
		{"a missing price", `{"overrides": [{"model": "a", "input_micro": 1, "output_micro": 1}]}`, "max_output"},
		{"nothing to import", `{"overrides": []}`, "no overrides"},
		{"two documents", `{"overrides": []} {"overrides": []}`, "more than one"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := in.run(t, nil, tt.body, "price", "import", "-")
			if r.code != 1 || !strings.Contains(r.stderr, tt.want) {
				t.Errorf("exit %d, stderr %q", r.code, r.stderr)
			}
		})
	}
	if r := in.run(t, nil, "", "price", "import", filepath.Join(t.TempDir(), "gone.json")); r.code != 1 {
		t.Errorf("a missing file: exit %d", r.code)
	}
	if r := in.run(t, nil, "", "price", "import"); r.code != 1 || !strings.Contains(r.stderr, "usage: kman price import") {
		t.Errorf("no file: exit %d, stderr %q", r.code, r.stderr)
	}
	if got := in.overrides(t); len(got) != 0 {
		t.Errorf("a refused import left %+v", got)
	}
}

func TestUpstreamPriceMultiplier(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	in.kman(t, "upstream", "add", "eu", "--host", "api.acme.test", "--path", "* /**", "--no-credential", "--price-multiplier", "1.1")
	var u admin.Upstream
	in.kmanJSON(t, &u, "upstream", "show", "eu")
	if u.PriceMultiplierPPM != 1_100_000 {
		t.Errorf("after add = %d", u.PriceMultiplierPPM)
	}
	in.kman(t, "upstream", "update", "eu", "--price-multiplier", "1.25")
	out := in.kman(t, "upstream", "show", "eu")
	if !strings.Contains(out, "price multiplier:") || !strings.Contains(out, "x1.25") {
		t.Errorf("show:\n%s", out)
	}
	for _, bad := range []string{"0", "-1", "101", "abc", "1.0000001"} {
		if r := in.run(t, nil, "", "upstream", "update", "eu", "--price-multiplier", bad); r.code != 1 || !strings.Contains(r.stderr, "multiplier") {
			t.Errorf("--price-multiplier %s: exit %d, stderr %q", bad, r.code, r.stderr)
		}
	}
	in.addAcme(t, "plain")
	in.kmanJSON(t, &u, "upstream", "show", "plain")
	if u.PriceMultiplierPPM != 1_000_000 {
		t.Errorf("default = %d", u.PriceMultiplierPPM)
	}
}

// An unset shell variable must not turn an upstream's override into the whole
// install's.
func TestPriceRefusesAnEmptyUpstream(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	in.kman(t, "price", "set", "m", "--input", "1", "--output", "2", "--max-output", "10")
	if r := in.run(t, nil, "", "price", "set", "n", "--upstream", "", "--input", "1", "--output", "2", "--max-output", "10"); r.code != 1 || !strings.Contains(r.stderr, "--upstream") {
		t.Errorf("set: exit %d, stderr %q", r.code, r.stderr)
	}
	if r := in.run(t, nil, "", "price", "rm", "m", "--upstream", ""); r.code != 1 || !strings.Contains(r.stderr, "--upstream") {
		t.Errorf("rm: exit %d, stderr %q", r.code, r.stderr)
	}
	if got := in.overrides(t); len(got) != 1 || got[0].Model != "m" {
		t.Errorf("overrides = %+v", got)
	}
}
