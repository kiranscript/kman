package admin_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/policy"
	"github.com/kirank55/kman/internal/profiles"
	"github.com/kirank55/kman/internal/registry"
	"github.com/kirank55/kman/internal/store"
	"github.com/kirank55/kman/internal/vault"
)

var t0 = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

// install is an admin service over its own store, vault and registry, with
// the real presets and a clock the test moves, and the owner token already
// made, as init leaves it.
type install struct {
	svc     *admin.Service
	store   *store.Store
	vault   *vault.Vault
	reg     *registry.Registry
	dir     string
	now     time.Time
	owner   string // the owner token's secret
	applied []registry.Change
	// normalize is the policy's rules; a test may swap them for rules that
	// know more fields than policy does yet.
	normalize func([]byte) (int, []byte, error)
	// spend stands for the meter, when a test shows spend.
	spend func(keyID, policy string) admin.KeySpend
}

func newInstall(t *testing.T) *install {
	t.Helper()
	return newInstallWith(t, profiles.Presets())
}

func newInstallWith(t *testing.T, presets []registry.Preset) *install {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), store.Options{Dir: dir, Now: t0})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	in := &install{store: st, dir: dir, now: t0, reg: registry.New(), vault: newVault(t, st), normalize: policy.Normalize}
	in.svc = in.service(presets)
	created, err := in.svc.CreateAdminToken(as("system:init"), admin.CreateAdminTokenRequest{Name: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	in.owner = created.Secret
	return in
}

// service is another admin service over the install, as a later release of
// kman would be with its own presets.
func (in *install) service(presets []registry.Preset) *admin.Service {
	return admin.New(admin.Options{
		Store:           in.store,
		Now:             func() time.Time { return in.now },
		Vault:           in.vault,
		Presets:         presets,
		NormalizePolicy: func(raw []byte) (int, []byte, error) { return in.normalize(raw) },
		Spend:           in.spend,
		Apply: func(c registry.Change) {
			in.applied = append(in.applied, c)
			in.reg.Apply(c)
		},
	})
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

// unlockVault opens the data key in st, as serve does at startup.
func unlockVault(t *testing.T, st *store.Store) *vault.Vault {
	t.Helper()
	var dk store.DataKey
	err := st.Do(context.Background(), func(tx *store.Tx) error {
		var err error
		dk, err = tx.CurrentDataKey()
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	v, err := vault.Unlock(masterKey{}, vault.WrappedKey{ID: dk.ID, Nonce: dk.Nonce, Wrapped: dk.Wrapped, Fingerprint: dk.KEKFingerprint}, vault.Options{AllowDebug: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(v.Close)
	return v
}

// sqlExec runs a statement against the install's database from outside the
// store, to damage what a command made.
func (in *install) sqlExec(t *testing.T, query string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(in.dir, store.FileName))+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

// as is a context whose caller is actor.
func as(actor string) context.Context {
	return admin.WithCaller(context.Background(), admin.Caller{Actor: actor, ClientIP: "127.0.0.1"})
}

// code is the admin error code of err, or "" if err is not an admin error.
func code(err error) admin.Code {
	var e *admin.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// latest returns the newest audit entries, oldest first.
func (in *install) latest(t *testing.T, n int) []admin.AuditEntry {
	t.Helper()
	page, err := in.svc.Audit(as("owner"), admin.AuditQuery{Latest: true, Limit: n})
	if err != nil {
		t.Fatal(err)
	}
	return page.Entries
}

// summary is an audit entry as "actor action kind/id".
func summary(e admin.AuditEntry) string {
	return strings.Join([]string{e.Actor, e.Action, e.TargetKind + "/" + e.TargetID}, " ")
}
