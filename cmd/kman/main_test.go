package main

import (
	"strings"
	"testing"
)

func TestAnUnknownCommandIsAUsageErrorNamingEveryCommand(t *testing.T) {
	s, _, stderr := testSys(t, nil)
	if code := run(t.Context(), []string{"nonsense"}, s); code != 1 {
		t.Errorf("kman nonsense exited %d, want 1 (SPEC §12: usage errors are errors)", code)
	}
	for _, name := range []string{"init", "serve", "keygen", "version", "login", "logout", "admin-token", "settings", "audit"} {
		if !strings.Contains(stderr.String(), name) {
			t.Errorf("the usage line lacks %s: %s", name, stderr)
		}
	}
}

func TestOnlineCommandsReadTheCredentialsFileInTheDefaultConfigDir(t *testing.T) {
	home := t.TempDir()
	s, _, stderr := testSys(t, map[string]string{"HOME": home})
	if code := run(t.Context(), []string{"admin-token", "list"}, s); code != 1 {
		t.Fatalf("admin-token list with no credentials exited %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "kman login") {
		t.Errorf("stderr %q does not say to log in", stderr)
	}
}
