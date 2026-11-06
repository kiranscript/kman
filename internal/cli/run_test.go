package cli_test

import (
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/cli"
)

func TestExitCodes(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	for _, tc := range []struct {
		args []string
		code int
	}{
		{[]string{"admin-token", "list"}, cli.ExitOK},
		{[]string{"admin-token", "list", "--help"}, cli.ExitOK},
		{[]string{"admin-token", "list", "--bogus"}, cli.ExitError},
		{[]string{"nonsense"}, cli.ExitError},
		{[]string{"admin-token", "create", "owner"}, cli.ExitError},
	} {
		if r := in.run(t, nil, "", tc.args...); r.code != tc.code {
			t.Errorf("kman %s = %d, want %d: %s", strings.Join(tc.args, " "), r.code, tc.code, r.stderr)
		}
	}
	if cli.ExitFindings != 3 {
		t.Errorf("ExitFindings = %d, want 3 (SPEC §12)", cli.ExitFindings)
	}
}

func TestFlagsMayFollowArguments(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	in.kman(t, "admin-token", "create", "--json", "ci")
	in.kman(t, "admin-token", "create", "deploy", "--json")
}

func TestCommandsListsTheOnlineCommands(t *testing.T) {
	got := strings.Join(cli.Commands(), " ")
	if got != "login logout admin-token settings audit price" {
		t.Errorf("Commands = %q", got)
	}
}
