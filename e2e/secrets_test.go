package e2e_test

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The planted-secrets session of the v0.1.0 security review (docs/security-review-v0.1.0.md,
// SPEC §1 invariant 3). Every secret kman handles is a distinctive string, one
// session creates, uses, lists, shows, audits and breaks things with all of
// them, and then everything kman wrote or said is searched. Each secret may
// appear only where it is meant to: the creation response, the file that holds
// it by design, and the one request that needs it.
type planted struct {
	name   string
	secret string
	// bodies are fragments that would give the secret away too: a token's secret
	// without its id and checksum, a Basic password without its user.
	fragments []string
	// allowedIn are files (relative to the home) that hold it by design.
	allowedIn []string
}

// searchSurfaces collects what a session produced, by where it came from.
type searchSurfaces struct {
	mu    sync.Mutex
	items []surface
}

type surface struct{ from, text string }

func (s *searchSurfaces) add(from, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = append(s.items, surface{from, text})
}

// leakyUpstream answers every request, and remembers what it was sent.
type leakyUpstream struct {
	srv  *httptest.Server
	mu   sync.Mutex
	seen []received
}

func newLeakyUpstream(t *testing.T) *leakyUpstream {
	t.Helper()
	u := &leakyUpstream{}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		u.mu.Lock()
		u.seen = append(u.seen, received{r.Method, r.RequestURI, r.Header.Clone(), string(body)})
		u.mu.Unlock()
		if r.URL.Path == "/v1/denied" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"error":{"message":"denied"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(u.srv.Close)
	return u
}

func (u *leakyUpstream) requests() []received {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]received(nil), u.seen...)
}

const (
	plantedCredential = "sk-planted-credential-Zq7Lw3Vx9TnB5RhK2MdF8YcA4SgJ6PeU1XoN0IvC"
	plantedBasicUser  = "svc-planted-user"
	plantedBasicPass  = "planted-basic-password-Hy4Rk8Wm2Tq6Nb0Jc9Fd3Ls5Va7Xe1Zo"
)

// secretBody is what a token holds after its prefix and id, up to its checksum.
func secretBody(token string) string {
	i := strings.LastIndex(token, "_")
	return token[i+1:][:43]
}

func TestPlantedSecretsAppearOnlyWhereTheyAreMeantTo(t *testing.T) {
	h := installed(t)
	up := newLeakyUpstream(t)
	down := httptest.NewServer(nil)
	downAddr := down.Listener.Addr().String()
	down.Close() // a port nothing listens on

	said := &searchSurfaces{}
	run := func(args ...string) string {
		t.Helper()
		out, errOut, _ := h.run(t, args...)
		said.add("kman "+strings.Join(args[:min(len(args), 2)], " "), out+errOut)
		return out
	}
	runIn := func(input string, args ...string) string {
		t.Helper()
		out, errOut, _ := h.runWithInput(t, input, args...)
		said.add("kman "+strings.Join(args[:min(len(args), 2)], " "), out+errOut)
		return out
	}

	// The master key, as init wrote it, and the owner token, as init saved it.
	masterKeyText, err := os.ReadFile(filepath.Join(h.configDir(), "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	masterKey := strings.TrimSpace(string(masterKeyText))
	raw, err := base64.StdEncoding.DecodeString(masterKey)
	if err != nil || len(raw) != 32 {
		t.Fatalf("the master key is not 32 bytes of base64: %v", err)
	}
	ownerToken := h.savedToken(t)

	s := h.serve(t)
	h.login(t, s)
	host, port := splitHostPort(t, up.srv.Listener.Addr().String())

	// Credentials: one bearer, one Basic.
	runIn(plantedCredential+"\n", "upstream", "add", "mock", "--host", host, "--port", port, "--no-tls", "--path", "* /**")
	runIn(plantedBasicUser+":"+plantedBasicPass+"\n", "upstream", "add", "basic", "--host", host, "--port", port, "--no-tls", "--path", "* /**", "--basic")
	dh, dp := splitHostPort(t, downAddr)
	runIn(plantedCredential+"\n", "upstream", "add", "down", "--host", dh, "--port", dp, "--no-tls", "--path", "* /**", "--credential-name", "down-cred")

	// Virtual keys and admin tokens, each shown once, at creation.
	vk1 := h.createKey(t, "app-one", "mock")
	vk2 := h.createKey(t, "app-two", "basic")
	vk3 := h.createKey(t, "app-down", "down")
	var created struct{ Secret string }
	shown, _ := h.mustRun(t, "admin-token", "create", "ci", "--json")
	if err := json.Unmarshal([]byte(shown), &created); err != nil || created.Secret == "" {
		t.Fatalf("admin-token create: %v", err)
	}
	ci1 := created.Secret
	created.Secret = ""
	shown, _ = h.mustRun(t, "admin-token", "rotate", "ci", "--json")
	if err := json.Unmarshal([]byte(shown), &created); err != nil || created.Secret == "" {
		t.Fatalf("admin-token rotate: %v", err)
	}
	ci2 := created.Secret

	// Traffic: good requests, an upstream's refusal, a key that does not exist,
	// a forged key, a request that must fail, and an upstream that is down.
	badKey := "kmn_AAAAAAAA_" + strings.Repeat("B", 49)
	for _, c := range []struct {
		key, method, path, body string
	}{
		{vk1, "POST", "/v1/things?api_key=" + plantedCredential[:8], `{"model":"m"}`},
		{vk2, "GET", "/v1/things", ""},
		{vk1, "GET", "/v1/denied", ""},
		{badKey, "GET", "/v1/things", ""},
		{vk3, "GET", "/v1/things", ""},
	} {
		var rd io.Reader
		if c.body != "" {
			rd = strings.NewReader(c.body)
		}
		resp, body := sendThrough(t, s, c.method, c.path, rd, http.Header{"Authorization": {"Bearer " + c.key}})
		said.add("proxy response to "+c.method+" "+c.path+" ("+resp.Status+")", resp.Header.Get("X-Kman-Error")+"\n"+headerText(resp.Header)+body)
	}
	// The key in every other place an SDK might put it.
	for name, v := range map[string]string{"X-Api-Key": vk1, "Api-Key": vk1, "X-Goog-Api-Key": vk1, "X-Postmark-Server-Token": vk1} {
		resp, body := sendThrough(t, s, "GET", "/v1/other", nil, http.Header{name: {v}})
		said.add("proxy response ("+name+")", headerText(resp.Header)+body)
	}

	// Every read the CLI has, in both forms, and every GET of the admin API.
	for _, args := range [][]string{
		{"upstream", "list"}, {"upstream", "list", "--json"}, {"upstream", "show", "mock", "--json"}, {"upstream", "show", "basic"},
		{"key", "list"}, {"key", "list", "--json"}, {"key", "show", "app-one"}, {"key", "show", "app-one", "--json"},
		{"admin-token", "list"}, {"admin-token", "list", "--json"}, {"settings", "get", "--json"},
		{"price", "list", "--json"}, {"audit", "--json"}, {"audit", "export"}, {"audit", "--latest"}, {},
	} {
		run(args...)
	}
	// Failures print errors: make some that mention secrets' neighbours.
	runIn(plantedCredential+"\n", "credential", "add", "Bad Name", "--upstream", "mock")
	runIn(plantedCredential+"\n", "credential", "add", "second", "--upstream", "nowhere")
	runIn("not-a-token\n", "login", "--admin-url", "http://"+s.admin)
	runIn(ownerToken+"\n", "login", "--admin-url", "http://"+s.admin)
	runIn(plantedCredential+"\n", "credential", "update", "second", "--secret")
	adminGETs := adminGetAll(t, s, ownerToken)
	for from, body := range adminGETs {
		said.add("admin API "+from, body)
	}
	run("key", "revoke", "app-two")
	run("key", "disable", "app-down")
	run("settings", "set", "default_key_ttl_seconds=86400")

	stopServer(t, s)
	said.add("kman serve log", s.logs.String())

	// A second start, with the master key from the environment: kman warns about it
	// and must not print it.
	envKeyHome := h.with("KMAN_MASTER_KEY=" + masterKey)
	envKeyHome.editConfig(t, "key_source: file", "key_source: env")
	again := envKeyHome.serve(t)
	envKeyHome.login(t, again)
	stopServer(t, again)
	said.add("kman serve log, key from the environment", again.logs.String())

	// What the upstream was sent: the credential where it belongs, no virtual key anywhere.
	for _, r := range up.requests() {
		for name, values := range r.Header {
			for _, v := range values {
				for _, vk := range []string{vk1, vk2, vk3} {
					if strings.Contains(v, secretBody(vk)) {
						t.Errorf("the upstream got a virtual key in header %s of %s %s", name, r.Method, r.URI)
					}
				}
			}
		}
		if strings.Contains(r.Body, plantedCredential) {
			t.Errorf("the upstream got the credential in a body")
		}
	}
	gotCredential, gotBasic := false, false
	for _, r := range up.requests() {
		a := r.Header.Get("Authorization")
		gotCredential = gotCredential || a == "Bearer "+plantedCredential
		gotBasic = gotBasic || a == "Basic "+base64.StdEncoding.EncodeToString([]byte(plantedBasicUser+":"+plantedBasicPass))
	}
	if !gotCredential || !gotBasic {
		t.Fatalf("the upstream did not get both credentials (bearer %v, basic %v): %+v", gotCredential, gotBasic, up.requests())
	}

	// The files: the raw database and everything beside it, the config directory,
	// the credentials file. Each secret may be in the files that hold it by design.
	files := map[string]string{}
	for _, dir := range []string{h.dataDir(), h.configDir()} {
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			files[path] = string(b)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(files) < 4 {
		t.Fatalf("expected the database, the config, the key and the credentials file: %v", keysOf(files))
	}
	for path, text := range files {
		said.add("file "+strings.TrimPrefix(path, h.root+string(filepath.Separator)), text)
	}

	secrets := []planted{
		{name: "credential", secret: plantedCredential},
		{name: "basic credential", secret: plantedBasicUser + ":" + plantedBasicPass, fragments: []string{plantedBasicPass}},
		{name: "virtual key app-one", secret: vk1, fragments: []string{secretBody(vk1)}},
		{name: "virtual key app-two", secret: vk2, fragments: []string{secretBody(vk2)}},
		{name: "virtual key app-down", secret: vk3, fragments: []string{secretBody(vk3)}},
		{name: "owner admin token", secret: ownerToken, fragments: []string{secretBody(ownerToken)}, allowedIn: []string{"credentials.json"}},
		{name: "admin token ci (before rotation)", secret: ci1, fragments: []string{secretBody(ci1)}},
		{name: "admin token ci (after rotation)", secret: ci2, fragments: []string{secretBody(ci2)}},
		{name: "master key", secret: masterKey, fragments: []string{masterKey[:40]}, allowedIn: []string{"master.key"}},
	}
	for _, p := range secrets {
		for _, sf := range said.items {
			if allowed(sf.from, p.allowedIn) {
				continue
			}
			for _, needle := range append([]string{p.secret}, p.fragments...) {
				if strings.Contains(sf.text, needle) {
					t.Errorf("the %s is in %s", p.name, sf.from)
					break
				}
			}
		}
		// A raw copy of the master key's bytes is as bad as its text.
		for _, sf := range said.items {
			if !allowed(sf.from, p.allowedIn) && p.name == "master key" && strings.Contains(sf.text, string(raw)) {
				t.Errorf("the raw master key is in %s", sf.from)
			}
		}
	}
}

// allowed reports whether a surface is one of the files that hold a secret by design.
func allowed(from string, files []string) bool {
	for _, f := range files {
		if strings.HasPrefix(from, "file ") && filepath.Base(strings.TrimPrefix(from, "file ")) == f {
			return true
		}
	}
	return false
}

func keysOf(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func headerText(h http.Header) string {
	var b strings.Builder
	for name, values := range h {
		for _, v := range values {
			b.WriteString(name + ": " + v + "\n")
		}
	}
	return b.String()
}

func splitHostPort(t *testing.T, addr string) (string, string) {
	t.Helper()
	i := strings.LastIndex(addr, ":")
	return addr[:i], addr[i+1:]
}

// adminGetAll reads every GET route of the admin API with the owner token, and
// the object routes for each id the lists name.
func adminGetAll(t *testing.T, s *server, token string) map[string]string {
	t.Helper()
	got := map[string]string{}
	fetch := func(path string) string {
		req, err := http.NewRequestWithContext(t.Context(), "GET", "http://"+s.admin+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := (&http.Client{Transport: &http.Transport{Proxy: nil}}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		got[path] = headerText(resp.Header) + string(b)
		return string(b)
	}
	for _, p := range []string{"/admin/v1/whoami", "/admin/v1/admin-tokens", "/admin/v1/settings", "/admin/v1/audit?limit=1000",
		"/admin/v1/audit?latest=true", "/admin/v1/prices", "/admin/v1/prices/status", "/admin/v1/openapi.json"} {
		fetch(p)
	}
	var ups []struct{ ID, Name string }
	json.Unmarshal([]byte(fetch("/admin/v1/upstreams")), &ups)
	for _, u := range ups {
		fetch("/admin/v1/upstreams/" + u.ID)
		fetch("/admin/v1/upstreams/" + u.Name + "/sync")
	}
	var ks []struct{ ID string }
	json.Unmarshal([]byte(fetch("/admin/v1/keys")), &ks)
	for _, k := range ks {
		fetch("/admin/v1/keys/" + k.ID)
	}
	return got
}
