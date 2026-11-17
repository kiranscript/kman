package e2e_test

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/policy/geoip/geoiptest"
)

// dbipMirror serves DB-IP Lite's files, as this month's, from a local server:
// no test reaches the real DB-IP. Every address on this machine is in the United
// States and in AS 64500, as far as the databases know.
func dbipMirror(t *testing.T) (url string, requests *atomic.Int64) {
	t.Helper()
	dir := t.TempDir()
	country := geoiptest.Write(t, dir, "c.mmdb", "DBIP-Country-Lite",
		geoiptest.Range{Network: "127.0.0.0/8", Country: "US"}, geoiptest.Range{Network: "::1/128", Country: "US"})
	asn := geoiptest.Write(t, dir, "a.mmdb", "DBIP-ASN-Lite",
		geoiptest.Range{Network: "127.0.0.0/8", ASN: 64500}, geoiptest.Range{Network: "::1/128", ASN: 64500})
	files := map[string][]byte{}
	for name, path := range map[string]string{"dbip-country-lite": country, "dbip-asn-lite": asn} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var packed bytes.Buffer
		zw := gzip.NewWriter(&packed)
		zw.Write(raw)
		zw.Close()
		for _, month := range []time.Time{time.Now().UTC(), time.Now().UTC().AddDate(0, -1, 0)} {
			files["/free/"+name+"-"+month.Format("2006-01")+".mmdb.gz"] = packed.Bytes()
		}
	}
	requests = new(atomic.Int64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if body, ok := files[r.URL.Path]; ok {
			w.Write(body)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/free", requests
}

// geoipConfig is what kman geoip update tells the operator to add to kman.yaml,
// for an install that set nothing.
func geoipConfig(h *home) string {
	dir := filepath.Join(h.dataDir(), "geoip")
	return "\ngeoip:\n  country_db: " + filepath.Join(dir, "dbip-country-lite.mmdb") + "\n  asn_db: " + filepath.Join(dir, "dbip-asn-lite.mmdb") + "\n"
}

// geoipProxied is kman serving with GeoIP databases that kman geoip update
// downloaded, in front of a mock upstream.
func geoipProxied(t *testing.T) (*proxied, *home) {
	t.Helper()
	h := installed(t)
	source, _ := dbipMirror(t)
	stdout, _ := h.mustRun(t, "geoip", "update", "--source", source)
	if !strings.Contains(stdout, "country_db:") {
		t.Fatalf("update did not say what to set in kman.yaml:\n%s", stdout)
	}
	h.appendConfig(t, geoipConfig(h))
	s := h.serve(t)
	h.login(t, s)
	mock := newMock(t, "generic-json")
	h.addUpstream(t, "mock", mock.host, mock.port)
	return &proxied{t: t, h: h, s: s, mock: mock, secret: h.createKey(t, "my-app", "mock")}, h
}

func TestAKeyLockedToNetworksFailsFromAnywhereElseThroughTheRealBinary(t *testing.T) {
	p := behindKman(t, "generic-json")
	// The test client is 127.0.0.1.
	p.setPolicy(`{"allowed_cidrs":["203.0.113.0/24"]}`)
	resp, body := p.send("POST", "/v1/things", strings.NewReader(`{"name":"x"}`))
	wantError(t, resp, body, http.StatusForbidden, "address_not_allowed")
	if n := len(p.mock.requests()); n != 0 {
		t.Errorf("the upstream got %d requests from a locked-out address", n)
	}

	p.setPolicy(`{"allowed_cidrs":["203.0.113.0/24","127.0.0.0/8"]}`)
	if resp, body = p.send("POST", "/v1/things", strings.NewReader(`{"name":"x"}`)); resp.StatusCode != 201 {
		t.Fatalf("from an allowed network = %d %s", resp.StatusCode, body)
	}
}

func TestAKeyLockedToCountriesFailsFromAnywhereElseThroughTheRealBinary(t *testing.T) {
	p, _ := geoipProxied(t)

	p.setPolicy(`{"allowed_countries":["de"]}`)
	resp, body := p.send("POST", "/v1/things", strings.NewReader(`{"name":"x"}`))
	wantError(t, resp, body, http.StatusForbidden, "address_not_allowed")
	if n := len(p.mock.requests()); n != 0 {
		t.Errorf("the upstream got %d requests from a locked-out country", n)
	}

	p.setPolicy(`{"allowed_countries":["DE","us"]}`)
	if resp, body = p.send("POST", "/v1/things", strings.NewReader(`{"name":"x"}`)); resp.StatusCode != 201 {
		t.Fatalf("from an allowed country = %d %s", resp.StatusCode, body)
	}
}

// With no database, a country lock is refused where it is set, with what to do.
func TestACountryLockIsRefusedAtConfigurationTimeWithNoDatabase(t *testing.T) {
	p := behindKman(t, "generic-json")
	stdout, stderr, code := p.h.run(t, "key", "update", "my-app", "--policy", `{"allowed_countries":["US"]}`)
	if code == 0 {
		t.Fatalf("the lock was accepted:\n%s", stdout)
	}
	for _, want := range []string{"country", "geoip", "kman geoip update"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the message does not mention %q:\n%s", want, stderr)
		}
	}
	// A key can still be created with it: the same refusal.
	if _, _, code := p.h.run(t, "key", "create", "other", "--upstream", "mock", "--policy", `{"allowed_countries":["US"]}`); code == 0 {
		t.Error("a key was created with a country lock and no database")
	}
	if resp, body := p.send("POST", "/v1/things", strings.NewReader(`{"name":"x"}`)); resp.StatusCode != 201 {
		t.Errorf("the key kept working with its old policy: %d %s", resp.StatusCode, body)
	}
}

// Taking the databases out of kman.yaml while a key is locked to countries must
// not leave the key unlocked: kman will not start.
func TestKmanWillNotStartWithCountryLockedKeysAndNoDatabase(t *testing.T) {
	p, h := geoipProxied(t)
	p.setPolicy(`{"allowed_countries":["US"]}`)
	if resp, body := p.send("POST", "/v1/things", strings.NewReader(`{"name":"x"}`)); resp.StatusCode != 201 {
		t.Fatalf("= %d %s", resp.StatusCode, body)
	}
	p.s.cmd.Process.Kill()
	<-p.s.exited

	h.editConfig(t, geoipConfig(h), "\n")
	stdout, stderr, code := h.run(t, "serve")
	if code == 0 {
		t.Fatalf("kman started:\n%s", stdout)
	}
	for _, want := range []string{"locked to countries", "country_db"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the message does not mention %q:\n%s", want, stderr)
		}
	}
}

func TestKmanWillNotStartWithAGeoIPDatabaseThatIsNotThere(t *testing.T) {
	h := installed(t)
	h.appendConfig(t, "\ngeoip:\n  country_db: "+filepath.Join(h.dataDir(), "nowhere.mmdb")+"\n")
	_, stderr, code := h.run(t, "serve")
	if code == 0 || !strings.Contains(stderr, "kman geoip update") {
		t.Errorf("exit %d:\n%s", code, stderr)
	}
}

func TestGeoIPUpdateGoesWhereItIsToldAndNoWhereElse(t *testing.T) {
	h := installed(t)
	source, requests := dbipMirror(t)
	if n := requests.Load(); n != 0 {
		t.Fatalf("%d requests before the command ran", n)
	}
	// kman serve, with a GeoIP database configured, asks the network for nothing:
	// only this command downloads (ADR 0008).
	h.mustRun(t, "geoip", "update", "--source", source)
	n := requests.Load()
	h.appendConfig(t, geoipConfig(h))
	s := h.serve(t)
	h.login(t, s)
	time.Sleep(200 * time.Millisecond)
	if got := requests.Load(); got != n {
		t.Errorf("serve made %d download requests", got-n)
	}
}
