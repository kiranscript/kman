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
		"max_key_lifetime         365d",
		"default_key_ttl          90d",
		"spend_timezone           UTC",
		"expose_client_addresses  off",
		"learning_period          7d",
		"soft_budget_defaults     50,80,100",
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
	for _, line := range []string{"max_key_lifetime         30d", "default_key_ttl          36h", "spend_timezone           Europe/Berlin", "soft_budget_defaults     75,100"} {
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

func TestSettingsSetTurnsClientAddressesOnAndOff(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	if got := in.kman(t, "settings", "get", "expose_client_addresses"); got != "off\n" {
		t.Errorf("client addresses are %q by default, want off", got)
	}
	in.kman(t, "settings", "set", "expose_client_addresses", "on")
	if got := in.kman(t, "settings", "get", "expose_client_addresses"); got != "on\n" {
		t.Errorf("after turning them on: %q", got)
	}
	in.kman(t, "settings", "set", "expose_client_addresses", "false")
	if got := in.kman(t, "settings", "get", "expose_client_addresses"); got != "off\n" {
		t.Errorf("after turning them off: %q", got)
	}
	if r := in.run(t, nil, "", "settings", "set", "expose_client_addresses", "maybe"); r.code != 1 || !strings.Contains(r.stderr, "on or off") {
		t.Errorf("a bad value: exit %d, %s", r.code, r.stderr)
	}
}

func TestTheLearningPeriodIsASettingThatTakesDaysHoursOrNone(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	for set, want := range map[string]string{"3d": "3d\n", "36h": "36h\n", "none": "none\n", "0": "none\n", "0s": "none\n", "0d": "none\n", "90d": "90d\n"} {
		in.kman(t, "settings", "set", "learning_period", set)
		if got := in.kman(t, "settings", "get", "learning_period"); got != want {
			t.Errorf("learning_period set to %s reads back as %q, want %q", set, got, want)
		}
	}
	for _, bad := range []string{"soon", "-1d", "91d", "never", "500ms", "-5m"} {
		if r := in.run(t, nil, "", "settings", "set", "learning_period", bad); r.code != 1 || r.stderr == "" {
			t.Errorf("learning_period %s = %d %q, want 1 and a reason", bad, r.code, r.stderr)
		}
	}
	var s admin.Settings
	in.kman(t, "settings", "set", "learning_period", "2d")
	in.kmanJSON(t, &s, "settings", "get")
	if s.LearningPeriodSeconds != 2*86400 {
		t.Errorf("settings get --json = %+v, want learning_period_seconds of 2 days", s)
	}
}
