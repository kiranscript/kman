package cli_test

import (
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/admin"
)

func TestExposureListsAnOpenKeyWithItsFixAndExitsZeroWithoutStrict(t *testing.T) {
	in := withOpenAI(t)
	in.kman(t, "key", "create", "my-app", "--upstream", "openai")
	in.kman(t, "key", "create", "locked", "--upstream", "openai", "--policy", `{"allowed_cidrs":["203.0.113.0/24"]}`)

	r := in.run(t, nil, "", "exposure")
	if r.code != 0 {
		t.Fatalf("kman exposure exited %d, want 0 without --strict:\n%s%s", r.code, r.stdout, r.stderr)
	}
	for _, want := range []string{"my-app", "open to every address", "Fix: kman key update my-app --policy '", "1 key to fix"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, r.stdout)
		}
	}
	if strings.Contains(r.stdout, "locked") {
		t.Errorf("a locked key is listed:\n%s", r.stdout)
	}
}

func TestExposureStrictExitsThreeOnFindingsAndZeroOtherwise(t *testing.T) {
	in := withOpenAI(t)
	if r := in.run(t, nil, "", "exposure", "--strict"); r.code != 0 || !strings.Contains(r.stdout, "No exposed keys") {
		t.Errorf("no keys: exit %d, %q", r.code, r.stdout)
	}
	in.kman(t, "key", "create", "locked", "--upstream", "openai", "--policy", `{"allowed_cidrs":["203.0.113.0/24"]}`)
	if r := in.run(t, nil, "", "exposure", "--strict"); r.code != 0 {
		t.Errorf("a locked key: exit %d, want 0:\n%s%s", r.code, r.stdout, r.stderr)
	}
	in.kman(t, "key", "create", "my-app", "--upstream", "openai")
	if r := in.run(t, nil, "", "exposure", "--strict"); r.code != 3 || !strings.Contains(r.stdout, "my-app") {
		t.Errorf("an open key: exit %d, want 3:\n%s%s", r.code, r.stdout, r.stderr)
	}
	// --strict before or after nothing else changes what is printed.
	if r := in.run(t, nil, "", "exposure"); r.code != 0 {
		t.Errorf("without --strict: exit %d", r.code)
	}
}

func TestExposureJSONPrintsTheAPIsResponseAndStrictStillExitsThree(t *testing.T) {
	in := withOpenAI(t)
	in.kman(t, "key", "create", "my-app", "--upstream", "openai")
	var got admin.Exposure
	in.kmanJSON(t, &got, "exposure")
	if got.KeysChecked != 1 || len(got.Findings) != 1 || got.Findings[0].Rule != admin.ExposureOpenToEveryAddress {
		t.Errorf("exposure --json = %+v", got)
	}
	r := in.run(t, nil, "", "exposure", "--json", "--strict")
	if r.code != 3 || !strings.HasPrefix(r.stdout, "{") {
		t.Errorf("--json --strict: exit %d, output %q; want 3 with the JSON printed", r.code, r.stdout)
	}
}

func TestExposureTakesNoArgumentsAndNeedsALogin(t *testing.T) {
	in := withOpenAI(t)
	if r := in.run(t, nil, "", "exposure", "extra"); r.code != 1 {
		t.Errorf("a stray argument: exit %d, want 1", r.code)
	}
	in.configDir = t.TempDir()
	if r := in.run(t, nil, "", "exposure"); r.code != 1 || !strings.Contains(r.stderr, "kman login") {
		t.Errorf("not logged in: exit %d, %q", r.code, r.stderr)
	}
}
