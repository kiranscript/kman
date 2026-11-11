package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Key-source names, the values of key_source and --key-source.
const (
	keySourceFile       = "file"
	keySourceSystemd    = "systemd-creds"
	keySourcePassphrase = "passphrase"
	keySourceEnv        = "env"
)

// keyFileEnv overrides master_key_file for the file key source, such as a
// Docker or Kubernetes secret mount.
const keyFileEnv = "KMAN_MASTER_KEY_FILE"

// Default listeners: loopback only (invariant 8).
const (
	defaultProxyListen = "127.0.0.1:8080"
	defaultAdminListen = "127.0.0.1:8081"
)

// The environment variables init takes the listener choices from when no
// flag gives them.
const (
	proxyListenEnv    = "KMAN_PROXY_LISTEN"
	adminListenEnv    = "KMAN_ADMIN_LISTEN"
	allowPlainHTTPEnv = "KMAN_ALLOW_PLAIN_HTTP"
)

// unixPrefix marks a listen address as a Unix socket path.
const unixPrefix = "unix:"

// Config is kman.yaml. kman serve reads it once, at startup; what changes
// while kman runs lives in the settings table instead.
type Config struct {
	DataDir   string `yaml:"data_dir"`
	KeySource string `yaml:"key_source"`
	// MasterKeyFile is the key file of the file and passphrase sources.
	MasterKeyFile string `yaml:"master_key_file,omitempty"`
	// AllowKeyInDataDir lets serve start with the master key file inside
	// the data directory, which invariant 1 otherwise refuses.
	AllowKeyInDataDir bool `yaml:"allow_key_in_data_dir,omitempty"`

	ProxyListen string `yaml:"proxy_listen"`
	// AdminListen is host:port, or unix:<path> for a 0600 Unix socket.
	AdminListen string `yaml:"admin_listen"`
	// AllowPlainHTTP lets the admin listener serve plain HTTP on an address
	// that is not loopback, which is otherwise refused.
	AllowPlainHTTP bool      `yaml:"allow_plain_http,omitempty"`
	TLS            TLSConfig `yaml:"tls,omitempty"`

	// MetricsListen, when set, is a host:port that serves /metrics with no
	// authentication, for a scraper that cannot send an admin token. It is off by
	// default: the admin listener serves /metrics behind the admin token.
	MetricsListen string `yaml:"metrics_listen,omitempty"`

	// TrustedProxies are the addresses or networks (CIDR) of reverse proxies in
	// front of the proxy listener, whose X-Forwarded-For kman believes. With none,
	// a request's address is its connection's.
	TrustedProxies []string `yaml:"trusted_proxies,omitempty"`

	// GeoIP names the GeoIP databases kman reads, if any (see GeoIPConfig).
	GeoIP GeoIPConfig `yaml:"geoip,omitempty"`
}

// trustedProxies is TrustedProxies as networks. A bare address is a network of
// one. check has already refused one that isn't either.
func (c Config) trustedProxies() []netip.Prefix {
	out := make([]netip.Prefix, 0, len(c.TrustedProxies))
	for _, s := range c.TrustedProxies {
		if p, err := parseProxy(s); err == nil {
			out = append(out, p)
		}
	}
	return out
}

func parseProxy(s string) (netip.Prefix, error) {
	if p, err := netip.ParsePrefix(s); err == nil {
		return p.Masked(), nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("trusted_proxies %q: use an address or a network such as 10.0.0.0/8", s)
	}
	a = a.Unmap()
	return netip.PrefixFrom(a, a.BitLen()), nil
}

// TLSConfig turns on TLS per listener.
type TLSConfig struct {
	Proxy ListenerTLS `yaml:"proxy,omitempty"`
	Admin ListenerTLS `yaml:"admin,omitempty"`
}

// ListenerTLS is a certificate and its private key, both PEM files. Both
// empty means plain HTTP.
type ListenerTLS struct {
	CertFile string `yaml:"cert_file,omitempty"`
	KeyFile  string `yaml:"key_file,omitempty"`
}

func (t ListenerTLS) enabled() bool { return t.CertFile != "" || t.KeyFile != "" }

// loadConfig reads and checks kman.yaml. Unknown keys are errors, so a typo
// does not silently fall back to a default. $KMAN_MASTER_KEY_FILE, read
// through getenv, overrides master_key_file for the file key source.
func loadConfig(path string, getenv func(string) string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, fmt.Errorf("%s does not exist: run kman init first", path)
		}
		return Config{}, err
	}
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	if f := getenv(keyFileEnv); f != "" && c.KeySource == keySourceFile {
		c.MasterKeyFile = f
	}
	if err := c.check(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// check refuses a config serve cannot run safely.
func (c Config) check() error {
	if c.DataDir == "" {
		return errors.New("data_dir is not set")
	}
	if !filepath.IsAbs(c.DataDir) {
		return fmt.Errorf("data_dir %q is not an absolute path", c.DataDir)
	}
	switch c.KeySource {
	case keySourceFile, keySourcePassphrase:
		if c.MasterKeyFile == "" {
			return fmt.Errorf("key_source %s needs master_key_file", c.KeySource)
		}
	case keySourceSystemd, keySourceEnv:
	case "":
		return errors.New("key_source is not set")
	default:
		return fmt.Errorf("key_source %q is not one of file, systemd-creds, passphrase, env", c.KeySource)
	}
	for _, s := range c.TrustedProxies {
		if _, err := parseProxy(s); err != nil {
			return err
		}
	}
	if err := c.GeoIP.check(); err != nil {
		return err
	}
	for _, l := range []struct {
		name string
		tls  ListenerTLS
	}{{"proxy", c.TLS.Proxy}, {"admin", c.TLS.Admin}} {
		if l.tls.enabled() && (l.tls.CertFile == "" || l.tls.KeyFile == "") {
			return fmt.Errorf("tls.%s needs both cert_file and key_file", l.name)
		}
	}
	if c.MetricsListen != "" {
		if strings.HasPrefix(c.MetricsListen, unixPrefix) {
			return errors.New("metrics_listen must be host:port")
		}
		if _, err := tcpAddr("metrics_listen", c.MetricsListen); err != nil {
			return err
		}
	}
	if strings.HasPrefix(c.ProxyListen, unixPrefix) {
		return errors.New("proxy_listen must be host:port; only the admin listener can be a Unix socket")
	}
	if _, err := tcpAddr("proxy_listen", c.ProxyListen); err != nil {
		return err
	}
	if path, ok := strings.CutPrefix(c.AdminListen, unixPrefix); ok {
		if !filepath.IsAbs(path) {
			return fmt.Errorf("admin_listen %q: the socket path is not absolute", c.AdminListen)
		}
		return nil
	}
	host, err := tcpAddr("admin_listen", c.AdminListen)
	if err != nil {
		return err
	}
	if !isLoopback(host) && !c.TLS.Admin.enabled() && !c.AllowPlainHTTP {
		return fmt.Errorf("admin_listen %s is not a loopback address, and plain HTTP there would send admin tokens in the clear: "+
			"set tls.admin, or set allow_plain_http: true if something in front of kman terminates TLS", c.AdminListen)
	}
	return nil
}

// tcpAddr checks a host:port listen address and returns its host.
func tcpAddr(key, addr string) (string, error) {
	if addr == "" {
		return "", fmt.Errorf("%s is not set", key)
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("%s %q: %w", key, addr, err)
	}
	return host, nil
}

// isLoopback reports whether host is a loopback address. An empty host or a
// wildcard means every interface, and a name other than localhost could
// resolve anywhere, so neither counts.
func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

// marshal renders c as kman.yaml.
func (c Config) marshal() ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("# kman configuration. kman serve reads it once, at startup.\n")
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(c); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
