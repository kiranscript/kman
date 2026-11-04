package cli_test

import (
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/admin"
)

func TestSettingsGetShowsTheDefaults(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	out := in.kman(t, "settings", "get")
	for _, line := range []string{
		"max_key_lifetime      365d",
		"default_key_ttl       90d",
		"spend_timezone        UTC",
		"soft_budget_defaults  50,80,100",
	} {
		if !strings.Contains(out, line+"\n") {
			t.Errorf("settings get lacks %q:\n%s", line, out)
		}
	}
	if got := in.kman(t, "settings", "get", "spend_timezone"); got != "UTC\n" {
		t.Errorf("settings get spend_timezone = %q, want UTC", got)
	}
	var s admin.Settings
	in.kmanJSON(t, &s, "settings", "get")
	if s.DefaultKeyTTLSeconds != 90*86400 || s.SpendTimezone != "UTC" {
		t.Errorf("settings get --json = %+v", s)
	}
}

func TestSettingsSetChangesSeveralAtOnce(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	in.kman(t, "settings", "set", "max_key_lifetime", "30d", "default_key_ttl", "36h",
		"spend_timezone", "Europe/Berlin", "soft_budget_defaults", "75,100")
	out := in.kman(t, "settings", "get")
	for _, line := range []string{"max_key_lifetime      30d", "default_key_ttl       36h", "spend_timezone        Europe/Berlin", "soft_budget_defaults  75,100"} {
		if !strings.Contains(out, line+"\n") {
			t.Errorf("settings get lacks %q:\n%s", line, out)
		}
	}
	in.kman(t, "settings", "set", "soft_budget_defaults", "none")
	if got := in.kman(t, "settings", "get", "soft_budget_defaults"); got != "none\n" {
		t.Errorf("cleared soft budgets read back as %q", got)
	}
}

func TestSettingsSetRefusesBadInput(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	for _, args := range [][]string{
		{"settings", "set"},
		{"settings", "set", "spend_timezone"},
		{"settings", "set", "colour", "blue"},
		{"settings", "set", "default_key_ttl", "soon"},
		{"settings", "set", "default_key_ttl", "never"},
		{"settings", "set", "default_key_ttl", "400d"},
		{"settings", "set", "soft_budget_defaults", "50,eighty"},
		{"settings", "set", "spend_timezone", "Mars/Olympus"},
		{"settings", "get", "colour"},
		{"settings", "frobnicate"},
	} {
		if r := in.run(t, nil, "", args...); r.code != 1 || r.stderr == "" {
			t.Errorf("kman %s = %d %q, want 1 and a reason", strings.Join(args, " "), r.code, r.stderr)
		}
	}
	if got := in.kman(t, "settings", "get", "default_key_ttl"); got != "90d\n" {
		t.Errorf("default_key_ttl is %q after refused sets, want 90d", got)
	}
}
