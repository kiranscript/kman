package e2e_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// refused runs kman serve, which must exit 1 naming why.
func (h *home) refused(t *testing.T, why string) {
	t.Helper()
	_, stderr, code := h.run(t, "serve")
	if code != 1 {
		t.Fatalf("kman serve exited %d, want 1:\n%s", code, stderr)
	}
	if !strings.Contains(stderr, why) {
		t.Errorf("kman serve did not say %q:\n%s", why, stderr)
	}
}

// moveKeyIntoDataDir moves master.key into the data directory and points
// kman.yaml at it.
func (h *home) moveKeyIntoDataDir(t *testing.T) {
	t.Helper()
	from, to := filepath.Join(h.configDir(), "master.key"), filepath.Join(h.dataDir(), "master.key")
	if err := os.Rename(from, to); err != nil {
		t.Fatal(err)
	}
	h.editConfig(t, "master_key_file: "+from, "master_key_file: "+to)
}

func TestServeRefusesAMasterKeyInsideTheDataDirectory(t *testing.T) {
	h := installed(t)
	h.moveKeyIntoDataDir(t)
	h.refused(t, "inside the data directory")
}

func TestServeRefusesAMasterKeyReachedThroughASymlinkIntoTheDataDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	h := installed(t)
	h.moveKeyIntoDataDir(t)
	link := filepath.Join(h.configDir(), "link.key")
	if err := os.Symlink(filepath.Join(h.dataDir(), "master.key"), link); err != nil {
		t.Fatal(err)
	}
	h.editConfig(t, "master_key_file: "+filepath.Join(h.dataDir(), "master.key"), "master_key_file: "+link)
	h.refused(t, "inside the data directory")
}

func TestServeRefusesAKeyFileFromTheEnvironmentInsideTheDataDirectory(t *testing.T) {
	h := installed(t)
	key := filepath.Join(h.dataDir(), "master.key")
	if err := os.Rename(filepath.Join(h.configDir(), "master.key"), key); err != nil {
		t.Fatal(err)
	}
	h.with("KMAN_MASTER_KEY_FILE="+key).refused(t, "inside the data directory")
}

func TestServeStartsWithTheKeyInTheDataDirectoryWhenAllowed(t *testing.T) {
	h := installed(t)
	h.moveKeyIntoDataDir(t)
	h.appendConfig(t, "allow_key_in_data_dir: true\n")
	s := h.serve(t)
	if !strings.Contains(s.logs.String(), "inside the data directory") {
		t.Errorf("kman serve did not warn about the key in the data directory:\n%s", s.logs)
	}
}

func TestServeNeverInitialisesAnEmptyDataDirectory(t *testing.T) {
	for name, prepare := range map[string]func(dir string) error{
		"missing": os.RemoveAll,
		"empty": func(dir string) error {
			if err := os.RemoveAll(dir); err != nil {
				return err
			}
			return os.Mkdir(dir, 0o700)
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := installed(t)
			if err := prepare(h.dataDir()); err != nil {
				t.Fatal(err)
			}
			h.refused(t, "kman init")
			if _, err := os.Stat(filepath.Join(h.dataDir(), "kman.db")); err == nil {
				t.Error("kman serve created a database")
			}
		})
	}
}

func TestServeRefusesPlainHTTPOnAPublicAdminAddress(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:0", ":0", `"[::]:0"`, "example.com:0"} {
		t.Run(addr, func(t *testing.T) {
			h := installed(t)
			h.editConfig(t, "admin_listen: 127.0.0.1:0", "admin_listen: "+addr)
			h.refused(t, "not a loopback address")
		})
	}
}

func TestServeAllowsPlainHTTPOnAPublicAdminAddressWhenEnabled(t *testing.T) {
	h := installed(t)
	h.editConfig(t, "admin_listen: 127.0.0.1:0", "admin_listen: 0.0.0.0:0")
	h.appendConfig(t, "allow_plain_http: true\n")
	s := h.serve(t)
	_, port, _ := net.SplitHostPort(s.admin)
	if code := get(t, "127.0.0.1:"+port, "/"); code == 0 {
		t.Error("the admin listener did not answer")
	}
}

func TestServeTerminatesTLSOnAPublicAdminAddress(t *testing.T) {
	h := installed(t)
	cert, key := selfSigned(t, h.root)
	h.editConfig(t, "admin_listen: 127.0.0.1:0", "admin_listen: 0.0.0.0:0")
	h.appendConfig(t, "tls:\n  admin:\n    cert_file: "+cert+"\n    key_file: "+key+"\n  proxy:\n    cert_file: "+cert+"\n    key_file: "+key+"\n")
	s := h.serve(t)
	for name, addr := range map[string]string{"proxy": s.proxy, "admin": s.admin} {
		_, port, _ := net.SplitHostPort(addr)
		if code := getTLS(t, "127.0.0.1:"+port, cert); code == 0 {
			t.Errorf("the %s listener did not answer over TLS", name)
		}
	}
}

func TestServeBindsAdminToAPrivateUnixSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix socket permissions are POSIX")
	}
	h := installed(t)
	// Socket paths are limited to about 100 bytes, which t.TempDir can pass.
	dir, err := os.MkdirTemp("", "kman")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "admin.sock")
	h.editConfig(t, "admin_listen: 127.0.0.1:0", "admin_listen: unix:"+sock)
	s := h.serve(t)
	if s.admin != "unix:"+sock {
		t.Fatalf("admin listens on %q, want unix:%s", s.admin, sock)
	}
	mode(t, sock, 0o600)
	if code := get(t, s.admin, "/"); code == 0 {
		t.Error("the admin socket did not answer")
	}
}

// selfSigned writes a certificate for 127.0.0.1 and its key under dir.
func selfSigned(t *testing.T, dir string) (certFile, keyFile string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "kman test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)}, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	pk, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	certFile, keyFile = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}
