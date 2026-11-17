package main

import (
	"strings"
	"testing"
)

func TestDashboardURLIsOptionalAndMustBeAnHTTPAddress(t *testing.T) {
	c, err := loadConfig(writeConfig(t, goodConfig), noEnv)
	if err != nil || c.Dashboard.URL != "" {
		t.Fatalf("with no dashboard.url: %v, %q; want none, which is the default", err, c.Dashboard.URL)
	}
	c, err = loadConfig(writeConfig(t, goodConfig+"dashboard:\n  url: https://kman.example.com\n"), noEnv)
	if err != nil || c.Dashboard.URL != "https://kman.example.com" {
		t.Fatalf("with dashboard.url: %v, %q", err, c.Dashboard.URL)
	}
	for name, v := range map[string]string{"no scheme": "kman.example.com", "another scheme": "ftp://kman.example.com", "a login": "https://u:p@kman.example.com"} {
		_, err := loadConfig(writeConfig(t, goodConfig+"dashboard:\n  url: "+v+"\n"), noEnv)
		if err == nil || !strings.Contains(err.Error(), "dashboard.url") {
			t.Errorf("%s: loadConfig = %v, want an error naming dashboard.url", name, err)
		}
	}
}
