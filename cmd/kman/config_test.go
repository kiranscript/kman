package main

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// root makes the fixture paths absolute on the OS running the tests: with
// no drive, /var/lib/kman is relative on Windows.
var root = func() string {
	if runtime.GOOS == "windows" {
		return "C:"
	}
	return ""
}()

var (
	dataDir    = root + "/var/lib/kman"
	keyFile    = root + "/etc/kman/master.key"
	adminSock  = "unix:" + root + "/run/kman/admin.sock"
	goodConfig = "data_dir: " + dataDir + `
key_source: file
master_key_file: ` + keyFile + `
proxy_listen: 127.0.0.1:8080
admin_listen: 127.0.0.1:8081
`
)

func writeConfig(t *testing.T, yaml string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), configFile)
	if err := os.WriteFile(path, []byte(yaml), fileMode); err != nil {
		t.Fatal(err)
	}
	return path
}

func noEnv(string) string { return "" }

func TestLoadConfigRefuses(t *testing.T) {
	for _, tc := range []struct {
		name, yaml, want string
	}{
		{"an unknown key", goodConfig + "data_directory: /x\n", "data_directory"},
		{"no data_dir", strings.Replace(goodConfig, "data_dir: "+dataDir+"\n", "", 1), "data_dir is not set"},
		{"a relative data_dir", strings.Replace(goodConfig, dataDir, "data", 1), "not an absolute path"},
		{"an unknown key source", strings.Replace(goodConfig, "key_source: file", "key_source: vault", 1), `"vault" is not one of`},
		{"a key file source with no file", strings.Replace(goodConfig, "master_key_file: "+keyFile+"\n", "", 1), "needs master_key_file"},
		{"half a TLS pair", goodConfig + "tls:\n  admin:\n    cert_file: /c.pem\n", "needs both cert_file and key_file"},
		{"a proxy on a Unix socket", strings.Replace(goodConfig, "proxy_listen: 127.0.0.1:8080", "proxy_listen: unix:/run/kman.sock", 1), "only the admin listener"},
		{"a relative admin socket", strings.Replace(goodConfig, "admin_listen: 127.0.0.1:8081", "admin_listen: unix:kman.sock", 1), "not absolute"},
		{"an admin address with no port", strings.Replace(goodConfig, "admin_listen: 127.0.0.1:8081", "admin_listen: 127.0.0.1", 1), "admin_listen"},
		{"a trusted proxy that isn't an address", goodConfig + "trusted_proxies:\n  - proxy.example.com\n", "trusted_proxies"},
		{"a trusted proxy with a bad prefix", goodConfig + "trusted_proxies:\n  - 10.0.0.0/33\n", "trusted_proxies"},
		{"plain HTTP admin on all interfaces", strings.Replace(goodConfig, "admin_listen: 127.0.0.1:8081", "admin_listen: 0.0.0.0:8081", 1), "not a loopback address"},
		{"plain HTTP admin on a LAN address", strings.Replace(goodConfig, "admin_listen: 127.0.0.1:8081", "admin_listen: 192.168.1.10:8081", 1), "not a loopback address"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadConfig(writeConfig(t, tc.yaml), noEnv)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("loadConfig = %v, want an error mentioning %q", err, tc.want)
			}
		})
	}
}

func TestLoadConfigAccepts(t *testing.T) {
	for _, tc := range []struct{ name, yaml string }{
		{"the defaults", goodConfig},
		{"IPv6 loopback admin", strings.Replace(goodConfig, "admin_listen: 127.0.0.1:8081", `admin_listen: "[::1]:8081"`, 1)},
		{"localhost admin", strings.Replace(goodConfig, "admin_listen: 127.0.0.1:8081", "admin_listen: localhost:8081", 1)},
		{"an admin Unix socket", strings.Replace(goodConfig, "admin_listen: 127.0.0.1:8081", "admin_listen: "+adminSock, 1)},
		{"public admin with TLS", strings.Replace(goodConfig, "admin_listen: 127.0.0.1:8081", "admin_listen: 0.0.0.0:8081", 1) +
			"tls:\n  admin:\n    cert_file: /c.pem\n    key_file: /k.pem\n"},
		{"public plain admin, explicitly enabled", strings.Replace(goodConfig, "admin_listen: 127.0.0.1:8081", "admin_listen: 0.0.0.0:8081", 1) +
			"allow_plain_http: true\n"},
		{"trusted proxies", goodConfig + "trusted_proxies:\n  - 10.0.0.0/8\n  - 192.0.2.7\n  - \"fd00::/8\"\n"},
		{"a public proxy", strings.Replace(goodConfig, "proxy_listen: 127.0.0.1:8080", "proxy_listen: 0.0.0.0:8080", 1)},
		{"the env key source", strings.Replace(strings.Replace(goodConfig, "key_source: file", "key_source: env", 1), "master_key_file: "+keyFile+"\n", "", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := loadConfig(writeConfig(t, tc.yaml), noEnv); err != nil {
				t.Fatalf("loadConfig: %v", err)
			}
		})
	}
}

func TestTheKeyFileVariableOverridesTheFileSourcesKeyFile(t *testing.T) {
	env := func(k string) string {
		if k == keyFileEnv {
			return "/run/secrets/master.key"
		}
		return ""
	}
	c, err := loadConfig(writeConfig(t, strings.Replace(goodConfig, "master_key_file: "+keyFile+"\n", "", 1)), env)
	if err != nil {
		t.Fatal(err)
	}
	if c.MasterKeyFile != "/run/secrets/master.key" {
		t.Errorf("MasterKeyFile = %q, want the one from %s", c.MasterKeyFile, keyFileEnv)
	}
}

func TestAMissingConfigNamesInit(t *testing.T) {
	_, err := loadConfig(filepath.Join(t.TempDir(), configFile), noEnv)
	if err == nil || !strings.Contains(err.Error(), "kman init") {
		t.Fatalf("loadConfig of a missing file = %v, want it to name kman init", err)
	}
}

func TestInitWritesAConfigServeAccepts(t *testing.T) {
	c := Config{DataDir: dataDir, KeySource: keySourceFile, MasterKeyFile: keyFile,
		ProxyListen: defaultProxyListen, AdminListen: defaultAdminListen}
	raw, err := c.marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := loadConfig(writeConfig(t, string(raw)), noEnv)
	if err != nil {
		t.Fatalf("loadConfig of what init writes: %v\n%s", err, raw)
	}
	if !reflect.DeepEqual(got, c) {
		t.Errorf("round trip = %+v, want %+v", got, c)
	}
}

func TestTrustedProxiesAreNetworksAndBareAddressesAreNetworksOfOne(t *testing.T) {
	c, err := loadConfig(writeConfig(t, goodConfig+"trusted_proxies:\n  - 10.1.2.3/8\n  - 192.0.2.7\n  - \"::ffff:192.0.2.9\"\n  - \"fd00::1\"\n"), noEnv)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range c.trustedProxies() {
		got = append(got, p.String())
	}
	want := []string{"10.0.0.0/8", "192.0.2.7/32", "192.0.2.9/32", "fd00::1/128"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("trustedProxies = %v, want %v", got, want)
	}
	if c, _ := loadConfig(writeConfig(t, goodConfig), noEnv); len(c.trustedProxies()) != 0 {
		t.Error("no trusted_proxies should trust nothing")
	}
}
