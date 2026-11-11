package main

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/kirank55/kman/internal/policy/geoip"
	"github.com/kirank55/kman/internal/policy/geoip/geoiptest"
)

// dbipFake serves DB-IP's download layout from memory and records the requests.
type dbipFake struct {
	*httptest.Server
	mu    sync.Mutex
	files map[string][]byte // by path, as served
	asked []string
}

func newDBIPFake(t *testing.T) *dbipFake {
	t.Helper()
	f := &dbipFake{files: map[string][]byte{}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.asked = append(f.asked, r.URL.Path)
		body, ok := f.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(body)
	}))
	t.Cleanup(f.Close)
	return f
}

func gz(t *testing.T, b []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	w := gzip.NewWriter(&out)
	w.Write(b)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// serveBoth publishes a country and an ASN database for a month, as DB-IP does.
func (f *dbipFake) serveBoth(t *testing.T, month string) (country, asn []byte) {
	t.Helper()
	dir := t.TempDir()
	country = readFile(t, geoiptest.Write(t, dir, "c.mmdb", "DBIP-Country-Lite", geoiptest.Range{Network: "203.0.113.0/24", Country: "US"}))
	asn = readFile(t, geoiptest.Write(t, dir, "a.mmdb", "DBIP-ASN-Lite", geoiptest.Range{Network: "203.0.113.0/24", ASN: 64500}))
	f.files["/free/dbip-country-lite-"+month+".mmdb.gz"] = gz(t, country)
	f.files["/free/dbip-asn-lite-"+month+".mmdb.gz"] = gz(t, asn)
	return country, asn
}

// geoipInstall is a config directory and a data directory, with kman.yaml
// holding extra.
func geoipInstall(t *testing.T, extra string) (configDir, dataDir string) {
	t.Helper()
	dir := t.TempDir()
	configDir, dataDir = filepath.Join(dir, "config"), filepath.Join(dir, "data")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	yaml := "data_dir: " + dataDir + "\nkey_source: env\nproxy_listen: 127.0.0.1:8080\nadmin_listen: 127.0.0.1:8081\n" + extra
	if err := os.WriteFile(filepath.Join(configDir, configFile), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	return configDir, dataDir
}

func runGeoIP(t *testing.T, configDir string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	s, out, errOut := testSys(t, nil) // the clock is 2026-11-04
	full := append([]string{"geoip"}, args...)
	if len(args) > 0 && args[0] == "update" {
		full = append([]string{"geoip", "update", "--config-dir", configDir}, args[1:]...)
	}
	code = run(t.Context(), full, s)
	return code, out.String(), errOut.String()
}

func TestGeoIPUpdateDownloadsTheDatabasesToTheConfiguredPaths(t *testing.T) {
	fake := newDBIPFake(t)
	country, asn := fake.serveBoth(t, "2026-11")
	dir := t.TempDir()
	cpath, apath := filepath.Join(dir, "geo", "c.mmdb"), filepath.Join(dir, "geo", "a.mmdb")
	configDir, _ := geoipInstall(t, "geoip:\n  country_db: "+cpath+"\n  asn_db: "+apath+"\n")

	code, stdout, stderr := runGeoIP(t, configDir, "update", "--source", fake.URL+"/free")
	if code != 0 {
		t.Fatalf("exit %d:\n%s%s", code, stdout, stderr)
	}
	if !bytes.Equal(readFile(t, cpath), country) || !bytes.Equal(readFile(t, apath), asn) {
		t.Error("the databases written are not the ones served, unpacked")
	}
	db, err := geoip.Open(cpath, apath)
	if err != nil {
		t.Fatalf("what was written does not open: %v", err)
	}
	defer db.Close()
	for _, want := range []string{cpath, apath, "DB-IP", "CC BY 4.0", "restart"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the output does not mention %q:\n%s", want, stdout)
		}
	}
	if fi, err := os.Stat(cpath); err == nil && fi.Mode().Perm()&0o022 != 0 && os.PathSeparator == '/' {
		t.Errorf("the database is writable by others: %v", fi.Mode())
	}
}

// Where nothing is configured, update puts the files in the data directory and
// says what to add to kman.yaml, which is the only place kman reads a path from.
func TestGeoIPUpdateWithNothingConfiguredUsesTheDataDirAndSaysWhatToSet(t *testing.T) {
	fake := newDBIPFake(t)
	fake.serveBoth(t, "2026-11")
	configDir, dataDir := geoipInstall(t, "")
	code, stdout, stderr := runGeoIP(t, configDir, "update", "--source", fake.URL+"/free")
	if code != 0 {
		t.Fatalf("exit %d:\n%s%s", code, stdout, stderr)
	}
	cpath := filepath.Join(dataDir, "geoip", "dbip-country-lite.mmdb")
	apath := filepath.Join(dataDir, "geoip", "dbip-asn-lite.mmdb")
	for _, p := range []string{cpath, apath} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%v", err)
		}
	}
	for _, want := range []string{"geoip:", "country_db: " + cpath, "asn_db: " + apath} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the output does not give %q to add to kman.yaml:\n%s", want, stdout)
		}
	}
}

func TestGeoIPUpdateFallsBackToLastMonthBeforeThisMonthsIsOut(t *testing.T) {
	fake := newDBIPFake(t)
	country, _ := fake.serveBoth(t, "2026-10") // and nothing for 2026-11, the clock's month
	configDir, dataDir := geoipInstall(t, "")
	if code, stdout, stderr := runGeoIP(t, configDir, "update", "--source", fake.URL+"/free"); code != 0 {
		t.Fatalf("exit %d:\n%s%s", code, stdout, stderr)
	}
	if !bytes.Equal(readFile(t, filepath.Join(dataDir, "geoip", "dbip-country-lite.mmdb")), country) {
		t.Error("the database is not last month's")
	}
}

func TestGeoIPUpdateAcceptsAnUncompressedDatabase(t *testing.T) {
	fake := newDBIPFake(t)
	country, _ := fake.serveBoth(t, "2026-11")
	fake.files["/free/dbip-country-lite-2026-11.mmdb.gz"] = country // a mirror that unpacked it
	configDir, dataDir := geoipInstall(t, "")
	if code, stdout, stderr := runGeoIP(t, configDir, "update", "--source", fake.URL+"/free"); code != 0 {
		t.Fatalf("exit %d:\n%s%s", code, stdout, stderr)
	}
	if !bytes.Equal(readFile(t, filepath.Join(dataDir, "geoip", "dbip-country-lite.mmdb")), country) {
		t.Error("the database is not the one served")
	}
}

// A download that fails, or is not a database, leaves what was there alone.
func TestGeoIPUpdateKeepsTheOldDatabasesWhenADownloadFails(t *testing.T) {
	for name, tamper := range map[string]func(f *dbipFake){
		"the ASN database is missing": func(f *dbipFake) { delete(f.files, "/free/dbip-asn-lite-2026-11.mmdb.gz") },
		"a page, not a database": func(f *dbipFake) {
			f.files["/free/dbip-asn-lite-2026-11.mmdb.gz"] = gz(t, []byte("<html>rate limited</html>"))
		},
		"not even gzip": func(f *dbipFake) {
			f.files["/free/dbip-asn-lite-2026-11.mmdb.gz"] = []byte("<html>rate limited</html>")
		},
		"a cut-off download": func(f *dbipFake) {
			b := f.files["/free/dbip-asn-lite-2026-11.mmdb.gz"]
			f.files["/free/dbip-asn-lite-2026-11.mmdb.gz"] = b[:len(b)/2]
		},
	} {
		t.Run(name, func(t *testing.T) {
			fake := newDBIPFake(t)
			fake.serveBoth(t, "2026-11")
			tamper(fake)
			dir := t.TempDir()
			cpath, apath := filepath.Join(dir, "c.mmdb"), filepath.Join(dir, "a.mmdb")
			os.WriteFile(cpath, []byte("old country"), 0o600)
			os.WriteFile(apath, []byte("old asn"), 0o600)
			configDir, _ := geoipInstall(t, "geoip:\n  country_db: "+cpath+"\n  asn_db: "+apath+"\n")

			code, stdout, stderr := runGeoIP(t, configDir, "update", "--source", fake.URL+"/free")
			if code != 1 {
				t.Fatalf("exit %d, want 1:\n%s%s", code, stdout, stderr)
			}
			if string(readFile(t, cpath)) != "old country" || string(readFile(t, apath)) != "old asn" {
				t.Error("a failed update changed the databases in use")
			}
			if left, _ := filepath.Glob(filepath.Join(dir, "*.tmp*")); len(left) > 0 {
				t.Errorf("left behind: %v", left)
			}
			if !strings.Contains(stderr, "kman geoip update") {
				t.Errorf("the error does not say what failed:\n%s", stderr)
			}
		})
	}
}

func TestGeoIPUpdateNamesInitWhenThereIsNoConfig(t *testing.T) {
	code, _, stderr := runGeoIP(t, t.TempDir(), "update")
	if code != 1 || !strings.Contains(stderr, "kman init") {
		t.Errorf("exit %d:\n%s", code, stderr)
	}
}

func TestGeoIPUsage(t *testing.T) {
	configDir, _ := geoipInstall(t, "")
	for _, args := range [][]string{{}, {"nope"}, {"update", "extra"}} {
		code, _, stderr := runGeoIP(t, configDir, args...)
		if code != 1 || !strings.Contains(stderr, "usage: kman geoip") {
			t.Errorf("%v: exit %d:\n%s", args, code, stderr)
		}
	}
}

// ADR 0008: the one thing that reaches the DB-IP site is this command, and only
// when the operator runs it. Without --source it is DB-IP's own HTTPS address.
func TestGeoIPUpdateGoesToDBIPUnlessToldOtherwise(t *testing.T) {
	if !strings.HasPrefix(defaultGeoIPSource, "https://download.db-ip.com/") {
		t.Errorf("defaultGeoIPSource = %q", defaultGeoIPSource)
	}
}
