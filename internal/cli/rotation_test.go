package cli_test

import (
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/admin"
)

func TestKeyRotatePrintsTheNewSecretOnceForTheSameKey(t *testing.T) {
	in := withOpenAI(t)
	var k admin.CreatedKey
	in.kmanJSON(t, &k, "key", "create", "my-app", "--upstream", "openai")

	out := in.kman(t, "key", "rotate", "my-app")

	fresh := printedKey.FindString(out)
	if fresh == "" || fresh == string(k.Secret) || !strings.HasPrefix(fresh, "kmn_"+k.Key.ID+"_") {
		t.Fatalf("want a new secret for key %s in:\n%s", k.Key.ID, out)
	}
	for _, want := range []string{"Rotated key my-app (" + k.Key.ID + ")", "only this once", "export OPENAI_API_KEY=" + fresh, "old secret stopped working"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, string(k.Secret)) {
		t.Errorf("the old secret is printed:\n%s", out)
	}
}

func TestKeyRotateWithAGraceSaysWhenTheOldSecretStops(t *testing.T) {
	in := withOpenAI(t)
	var k admin.CreatedKey
	in.kmanJSON(t, &k, "key", "create", "my-app", "--upstream", "openai")

	out := in.kman(t, "key", "rotate", k.Key.ID, "--grace", "2h")
	if !strings.Contains(out, "old secret keeps working until 2026-11-04T14:00Z") {
		t.Errorf("output:\n%s", out)
	}
	var got admin.RotatedKey
	in.kmanJSON(t, &got, "key", "rotate", k.Key.ID, "--grace", "1d")
	if got.Key.ID != k.Key.ID || got.PreviousValidUntil == nil || got.Secret == "" {
		t.Errorf("rotate --json = %+v", got)
	}
	in.kman(t, "key", "rotate", k.Key.ID, "--grace", "0") // 0 is allowed: it is the default
}

func TestKeyRotateRefusesWhatItCannotDo(t *testing.T) {
	in := withOpenAI(t)
	var k admin.CreatedKey
	in.kmanJSON(t, &k, "key", "create", "my-app", "--upstream", "openai")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"key", "rotate"}, "key rotate"},
		{[]string{"key", "rotate", "nobody"}, "nobody"},
		{[]string{"key", "rotate", "my-app", "--grace", "soon"}, "--grace"},
		{[]string{"key", "rotate", "my-app", "--grace", "-1h"}, "--grace"},
		{[]string{"key", "rotate", "my-app", "--grace", "31d"}, "30 days"},
	} {
		if r := in.run(t, nil, "", tc.args...); r.code != 1 || !strings.Contains(r.stdout+r.stderr, tc.want) {
			t.Errorf("%v: exit %d, %s%s; want exit 1 and %q", tc.args, r.code, r.stdout, r.stderr, tc.want)
		}
	}
	in.kman(t, "key", "revoke", "my-app")
	if r := in.run(t, nil, "", "key", "rotate", k.Key.ID); r.code != 1 || !strings.Contains(r.stderr, "revoked") {
		t.Errorf("rotating a revoked key: exit %d, %s%s", r.code, r.stdout, r.stderr)
	}
}

func TestCredentialCompromiseReplacesItAndListsTheAppsThatNeedNewKeys(t *testing.T) {
	in := withOpenAI(t)
	var a, b admin.CreatedKey
	in.kmanJSON(t, &a, "key", "create", "my-app", "--upstream", "openai", "--note", "the billing app")
	in.kmanJSON(t, &b, "key", "create", "other-app", "--upstream", "openai")

	out := in.kmanIn(t, "sk-live-replacement-0123456789abcdefQRST\n", "credential", "compromise", "openai").stdout

	for _, want := range []string{"credential openai", "my-app (" + a.Key.ID + ")", "other-app (" + b.Key.ID + ")", "the billing app", "need new keys", "only this once"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if got := printedKey.FindAllString(out, -1); len(got) != 2 || got[0] == string(a.Secret) || got[1] == string(b.Secret) {
		t.Errorf("want two new secrets in:\n%s", out)
	}
	if strings.Contains(out, "sk-live-replacement") {
		t.Errorf("the credential is echoed:\n%s", out)
	}
	if r := in.run(t, nil, "", "key", "show", "my-app"); r.code != 0 {
		t.Errorf("the key is gone after a compromise: %s", r.stderr)
	}
}

func TestCredentialCompromiseReadsTheSecretFromTheEnvironmentAndSaysWhenNoKeyUsesIt(t *testing.T) {
	in := withOpenAI(t)
	r := in.run(t, map[string]string{"NEW_CRED": "sk-live-replacement-0123456789abcdefQRST"}, "", "credential", "compromise", "openai", "--credential-env", "NEW_CRED")
	if r.code != 0 || !strings.Contains(r.stdout, "No virtual key used it") {
		t.Errorf("exit %d, %s%s", r.code, r.stdout, r.stderr)
	}
}

func TestCredentialCompromiseNeedsASecretAndACredential(t *testing.T) {
	in := withOpenAI(t)
	if r := in.run(t, nil, "", "credential", "compromise", "openai"); r.code != 1 || !strings.Contains(r.stderr, "no secret given") {
		t.Errorf("no secret: exit %d, %s%s", r.code, r.stdout, r.stderr)
	}
	if r := in.run(t, nil, "sk-live-replacement-0123456789abcdefQRST\n", "credential", "compromise", "nope"); r.code != 1 || !strings.Contains(r.stderr, "nope") {
		t.Errorf("no credential: exit %d, %s%s", r.code, r.stdout, r.stderr)
	}
	if r := in.run(t, nil, "", "credential", "compromise"); r.code != 1 {
		t.Errorf("no name: exit %d", r.code)
	}
}
