package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// initListeners runs init with a fresh key file and returns the kman.yaml it
// wrote, or the exit code and stderr when it refused.
func initListeners(t *testing.T, env map[string]string, flags ...string) (Config, int, string) {
	t.Helper()
	dir := t.TempDir()
	config := filepath.Join(dir, "config")
	s, stdout, stderr := testSys(t, env)
	args := append([]string{"init", "--config-dir", config, "--data-dir", filepath.Join(dir, "data"), "--allow-debug"}, flags...)
	code := run(t.Context(), args, s)
	if code != 0 {
		if _, err := os.Stat(config); err == nil {
			t.Errorf("init exited %d but left %s behind", code, config)
		}
		return Config{}, code, stderr.String()
	}
	c, err := loadConfig(filepath.Join(config, configFile), s.getenv)
	if err != nil {
		t.Fatalf("serve could not load what init wrote: %v\n%s", err, stdout)
	}
	return c, 0, stderr.String()
}

func TestInitListensOnLoopbackByDefault(t *testing.T) {
	c, code, stderr := initListeners(t, nil)
	if code != 0 {
		t.Fatalf("init exited %d:\n%s", code, stderr)
	}
	if c.ProxyListen != "127.0.0.1:8080" || c.AdminListen != "127.0.0.1:8081" || c.AllowPlainHTTP {
		t.Errorf("listeners = %s, %s, allow_plain_http %v; want 127.0.0.1:8080, 127.0.0.1:8081, false",
			c.ProxyListen, c.AdminListen, c.AllowPlainHTTP)
	}
}

// In a container kman has to listen on every interface for a published port
// to reach it, and Compose's port mapping decides what the host exposes.
// Each listener choice is a flag, so the operator enables that explicitly.
func TestInitWritesTheListenersItIsGiven(t *testing.T) {
	c, code, stderr := initListeners(t, nil,
		"--proxy-listen", "0.0.0.0:8080", "--admin-listen", "0.0.0.0:8081", "--allow-plain-http")
	if code != 0 {
		t.Fatalf("init exited %d:\n%s", code, stderr)
	}
	if c.ProxyListen != "0.0.0.0:8080" || c.AdminListen != "0.0.0.0:8081" || !c.AllowPlainHTTP {
		t.Errorf("listeners = %s, %s, allow_plain_http %v; want 0.0.0.0:8080, 0.0.0.0:8081, true",
			c.ProxyListen, c.AdminListen, c.AllowPlainHTTP)
	}
}

// docker compose run --rm kman init takes no flags, so the Compose file sets
// the same choices in the environment.
func TestInitTakesTheListenersFromTheEnvironment(t *testing.T) {
	c, code, stderr := initListeners(t, map[string]string{
		"KMAN_PROXY_LISTEN": "0.0.0.0:8080", "KMAN_ADMIN_LISTEN": "0.0.0.0:8081", "KMAN_ALLOW_PLAIN_HTTP": "true",
	})
	if code != 0 {
		t.Fatalf("init exited %d:\n%s", code, stderr)
	}
	if c.ProxyListen != "0.0.0.0:8080" || c.AdminListen != "0.0.0.0:8081" || !c.AllowPlainHTTP {
		t.Errorf("listeners = %s, %s, allow_plain_http %v; want 0.0.0.0:8080, 0.0.0.0:8081, true",
			c.ProxyListen, c.AdminListen, c.AllowPlainHTTP)
	}
}

func TestInitFlagsWinOverTheEnvironment(t *testing.T) {
	c, code, stderr := initListeners(t, map[string]string{"KMAN_PROXY_LISTEN": "0.0.0.0:8080"},
		"--proxy-listen", "127.0.0.1:9090")
	if code != 0 {
		t.Fatalf("init exited %d:\n%s", code, stderr)
	}
	if c.ProxyListen != "127.0.0.1:9090" {
		t.Errorf("proxy_listen = %s, want the flag's 127.0.0.1:9090", c.ProxyListen)
	}
}

// Invariant 8: plain HTTP on a non-loopback admin address is refused unless
// explicitly enabled. init refuses what serve would, before writing anything.
func TestInitRefusesAPlainHTTPAdminOffLoopback(t *testing.T) {
	_, code, stderr := initListeners(t, nil, "--admin-listen", "0.0.0.0:8081")
	if code != 1 {
		t.Fatalf("init exited %d, want 1:\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "allow_plain_http") {
		t.Errorf("stderr does not say how to enable it:\n%s", stderr)
	}
}

func TestInitRefusesABadListenerSetting(t *testing.T) {
	for name, tc := range map[string]struct {
		env   map[string]string
		flags []string
	}{
		"proxy not host:port":   {flags: []string{"--proxy-listen", "8080"}},
		"plain HTTP not a bool": {env: map[string]string{"KMAN_ALLOW_PLAIN_HTTP": "maybe"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, code, stderr := initListeners(t, tc.env, tc.flags...); code == 0 {
				t.Errorf("init accepted it:\n%s", stderr)
			}
		})
	}
}
