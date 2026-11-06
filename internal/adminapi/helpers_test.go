package adminapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/adminapi"
	"github.com/kirank55/kman/internal/profiles"
	"github.com/kirank55/kman/internal/store"
	"github.com/kirank55/kman/internal/vault"
)

var t0 = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

// server is the admin API over a fresh install, served by httptest.
type server struct {
	url   string
	owner string // the owner token's secret
	logs  *bytes.Buffer
}

func newServer(t *testing.T) *server {
	t.Helper()
	st, err := store.Open(context.Background(), store.Options{Dir: t.TempDir(), Now: t0})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	svc := admin.New(admin.Options{Store: st, Now: func() time.Time { return t0 }, Vault: newVault(t, st), Presets: profiles.Presets()})
	owner, err := svc.CreateAdminToken(admin.WithCaller(context.Background(), admin.Caller{Actor: "system:init"}),
		admin.CreateAdminTokenRequest{Name: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	ts := httptest.NewServer(adminapi.Handler(svc, slog.New(slog.NewTextHandler(&logs, nil))))
	t.Cleanup(ts.Close)
	return &server{url: ts.URL, owner: owner.Secret, logs: &logs}
}

// masterKey is a key source holding a fixed master key.
type masterKey struct{}

func (masterKey) MasterKey() ([]byte, error) { return make([]byte, vault.KeySize), nil }
func (masterKey) String() string             { return "the test's master key" }

// newVault makes a data key in st and a vault holding it.
func newVault(t *testing.T, st *store.Store) *vault.Vault {
	t.Helper()
	v, wk, err := vault.Create(masterKey{}, "dek_test", vault.Options{AllowDebug: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(v.Close)
	err = st.Do(context.Background(), func(tx *store.Tx) error {
		return tx.CreateDataKey(store.DataKey{ID: wk.ID, Wrapped: wk.Wrapped, Nonce: wk.Nonce, KEKFingerprint: wk.Fingerprint, CreatedAt: t0})
	})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// response is one answer from the server.
type response struct {
	status int
	header http.Header
	body   []byte
}

// do sends a request with the admin token tok ("" for none) and a JSON
// body ("" for none).
func (s *server) do(t *testing.T, tok, method, path, body string) response {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, s.url+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response{status: resp.StatusCode, header: resp.Header, body: b}
}

// decode unmarshals the body into v, failing the test unless the status is
// want.
func (r response) decode(t *testing.T, want int, v any) {
	t.Helper()
	if r.status != want {
		t.Fatalf("status %d, want %d: %s", r.status, want, r.body)
	}
	if err := json.Unmarshal(r.body, v); err != nil {
		t.Fatalf("decode %s: %v", r.body, err)
	}
}

// errorCode is the code of an error body.
func (r response) errorCode(t *testing.T) string {
	t.Helper()
	var e struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(r.body, &e); err != nil || e.Error.Message == "" {
		t.Fatalf("not an error body (%v): %s", err, r.body)
	}
	return e.Error.Code
}
