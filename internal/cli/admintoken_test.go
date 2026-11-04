package cli_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/admin"
)

var secretRE = regexp.MustCompile(`kma_[0-9A-Za-z]{49}`)

func TestAdminTokenCreatePrintsTheSecretOnce(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	out := in.kman(t, "admin-token", "create", "ci")
	secrets := secretRE.FindAllString(out, -1)
	if len(secrets) != 1 || !strings.Contains(out, "ci") {
		t.Fatalf("create printed:\n%s\nwant ci's secret once", out)
	}
	r := in.run(t, map[string]string{"KMAN_ADMIN_TOKEN": secrets[0]}, "", "admin-token", "list")
	if r.code != 0 {
		t.Errorf("the new token does not work: %d %s", r.code, r.stderr)
	}
	if secretRE.MatchString(r.stdout) {
		t.Errorf("list printed a secret:\n%s", r.stdout)
	}
}

func TestAdminTokenListShowsWhatIsLiveAndRevoked(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	in.kman(t, "admin-token", "create", "ci")
	in.kman(t, "admin-token", "revoke", "ci")
	out := in.kman(t, "admin-token", "list")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("list printed:\n%s\nwant a header and two tokens", out)
	}
	if f := strings.Fields(lines[1]); f[0] != "ci" || f[len(f)-1] != "revoked" {
		t.Errorf("ci's line is %q, want it revoked", lines[1])
	}
	if f := strings.Fields(lines[2]); f[0] != "owner" || f[len(f)-1] != "live" {
		t.Errorf("owner's line is %q, want it live", lines[2])
	}
	var list admin.AdminTokenList
	in.kmanJSON(t, &list, "admin-token", "list")
	if len(list.Tokens) != 2 || list.Tokens[0].RevokedAt == nil {
		t.Errorf("list --json = %+v", list)
	}
}

func TestRotatingTheCLIsOwnTokenUpdatesTheCredentialsFile(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	out := in.kman(t, "admin-token", "rotate", "owner")
	secrets := secretRE.FindAllString(out, -1)
	if len(secrets) != 1 || secrets[0] == in.owner {
		t.Fatalf("rotate printed:\n%s\nwant a new secret", out)
	}
	if !strings.Contains(readFile(t, in.credentials()), secrets[0]) {
		t.Error("the credentials file still holds the old secret")
	}
	in.kman(t, "admin-token", "list") // still logged in
}

func TestRotatingAnotherTokenLeavesTheCredentialsFile(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	in.kman(t, "admin-token", "create", "ci")
	before := readFile(t, in.credentials())
	var rotated admin.CreatedAdminToken
	in.kmanJSON(t, &rotated, "admin-token", "rotate", "ci")
	if rotated.Token.Name != "ci" || !secretRE.MatchString(rotated.Secret) {
		t.Errorf("rotate --json = %+v", rotated)
	}
	if readFile(t, in.credentials()) != before {
		t.Error("rotating ci changed the credentials file")
	}
}

func TestAdminTokenErrorsExitOne(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"admin-token", "create", "owner"}, "already exists"},
		{[]string{"admin-token", "create", "Bad Name"}, "admin token name"},
		{[]string{"admin-token", "revoke", "owner"}, "last live admin token"},
		{[]string{"admin-token", "rotate", "nope"}, "does not exist"},
		{[]string{"admin-token", "create"}, "usage"},
		{[]string{"admin-token", "create", "a", "b"}, "usage"},
		{[]string{"admin-token", "frobnicate"}, "usage"},
		{[]string{"admin-token"}, "usage"},
	} {
		r := in.run(t, nil, "", tc.args...)
		if r.code != 1 || !strings.Contains(r.stderr, tc.want) {
			t.Errorf("kman %s = %d, stderr %q; want 1 and %q", strings.Join(tc.args, " "), r.code, r.stderr, tc.want)
		}
	}
}
