package main

import (
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/admin"
)

func TestRetentionDefaultsToTheDocumentedWindowsAndKeepEverythingTurnsPruningOff(t *testing.T) {
	c, err := loadConfig(writeConfig(t, goodConfig), noEnv)
	if err != nil {
		t.Fatal(err)
	}
	got, prune := c.retention()
	if !prune || got != admin.DefaultRetention {
		t.Errorf("no retention setting gave %+v (pruning %v), want the defaults", got, prune)
	}
	for _, yaml := range []string{"retention: default\n", "retention: \"\"\n"} {
		if c, err := loadConfig(writeConfig(t, goodConfig+yaml), noEnv); err != nil {
			t.Errorf("%q: %v", yaml, err)
		} else if _, prune := c.retention(); !prune {
			t.Errorf("%q turned pruning off", yaml)
		}
	}
	c, err = loadConfig(writeConfig(t, goodConfig+"retention: keep_everything\n"), noEnv)
	if err != nil {
		t.Fatal(err)
	}
	if _, prune := c.retention(); prune {
		t.Error("retention: keep_everything still prunes")
	}
}

func TestAnUnknownRetentionIsRefusedBeforeAnythingIsDeleted(t *testing.T) {
	for _, v := range []string{"forever", "30d", "false", "keep-everything"} {
		_, err := loadConfig(writeConfig(t, goodConfig+"retention: "+v+"\n"), noEnv)
		if err == nil || !strings.Contains(err.Error(), "keep_everything") {
			t.Errorf("retention: %s: %v, want an error naming keep_everything", v, err)
		}
	}
}

func TestUpdateCheckIsOffUnlessTheOperatorTurnsItOn(t *testing.T) {
	c, err := loadConfig(writeConfig(t, goodConfig), noEnv)
	if err != nil || c.UpdateCheck {
		t.Fatalf("no update_check: %v, on = %v, want off (ADR 0008)", err, c.UpdateCheck)
	}
	c, err = loadConfig(writeConfig(t, goodConfig+"update_check: true\n"), noEnv)
	if err != nil || !c.UpdateCheck {
		t.Fatalf("update_check: true: %v, on = %v", err, c.UpdateCheck)
	}
	// What init writes carries neither key, so a fresh install asks nothing of the network.
	raw, err := Config{DataDir: dataDir, KeySource: keySourceFile, MasterKeyFile: keyFile,
		ProxyListen: defaultProxyListen, AdminListen: defaultAdminListen}.marshal()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "update_check") || strings.Contains(string(raw), "retention") {
		t.Errorf("init's config names a day-two setting:\n%s", raw)
	}
}
