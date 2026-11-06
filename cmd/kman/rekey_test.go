package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/profiles"
	"github.com/kirank55/kman/internal/registry"
	"github.com/kirank55/kman/internal/store"
	"github.com/kirank55/kman/internal/vault"
)

const (
	rekeySecret  = "sk-live-rekey-sentinel-0123456789abcdef"
	deletedToken = "sk-live-rekey-deleted-0123456789abcdef"
)

// rekeyHome is an install made by init, in its own directories.
type rekeyHome struct {
	config, data string
}

func (h rekeyHome) keyFile() string { return filepath.Join(h.config, masterKeyFile) }
func (h rekeyHome) db() string      { return filepath.Join(h.data, store.FileName) }

// newRekeyHome runs init with these extra arguments, answering prompts from
// answers, and adds an upstream with a live and a deleted credential, sealed
// under the key from src.
func newRekeyHome(t *testing.T, src vault.KeySource, initArgs []string, answers ...string) rekeyHome {
	t.Helper()
	dir := t.TempDir()
	h := rekeyHome{config: filepath.Join(dir, "config"), data: filepath.Join(dir, "data")}
	s, stdout, stderr := testSys(t, nil, answers...)
	args := append([]string{"init", "--config-dir", h.config, "--data-dir", h.data, "--allow-debug"}, initArgs...)
	if code := run(t.Context(), args, s); code != 0 {
		t.Fatalf("init exited %d:\n%s%s", code, stdout, stderr)
	}
	if src == nil {
		src = vault.FileSource(h.keyFile())
	}
	h.seed(t, src)
	return h
}

// seed adds an upstream with a live and a deleted credential.
func (h rekeyHome) seed(t *testing.T, src vault.KeySource) {
	t.Helper()
	h.withService(t, src, func(ctx context.Context, svc *admin.Service) {
		if _, err := svc.AddUpstream(ctx, admin.AddUpstreamRequest{Preset: "openai",
			Credential: &admin.NewCredential{Name: "main", Secret: admin.Secret(rekeySecret)}}); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.AddCredential(ctx, admin.AddCredentialRequest{Name: "gone", Upstream: "openai", Secret: admin.Secret(deletedToken)}); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.RemoveCredential(ctx, admin.CredentialRef{Ref: "gone"}); err != nil {
			t.Fatal(err)
		}
	})
}

// withService runs fn with an admin service over the install, unlocked with src.
func (h rekeyHome) withService(t *testing.T, src vault.KeySource, fn func(context.Context, *admin.Service)) {
	t.Helper()
	ctx := admin.WithCaller(t.Context(), admin.Caller{Actor: "owner"})
	st, v := h.unlock(t, src)
	defer st.Close()
	defer v.Close()
	s, _, _ := testSys(t, nil)
	fn(ctx, admin.New(admin.Options{Store: st, Now: s.now, Vault: v, Presets: profiles.Presets(), Apply: registry.New().Apply}))
}

// unlock opens the store and unwraps its current data key with src, as serve does.
func (h rekeyHome) unlock(t *testing.T, src vault.KeySource) (*store.Store, *vault.Vault) {
	t.Helper()
	s, _, _ := testSys(t, nil)
	st, err := store.Open(t.Context(), store.Options{Dir: h.data, Now: s.now()})
	if err != nil {
		t.Fatal(err)
	}
	dk := h.dataKey(t, st)
	v, err := vault.Unlock(src, vault.WrappedKey{ID: dk.ID, Nonce: dk.Nonce, Wrapped: dk.Wrapped, Fingerprint: dk.KEKFingerprint}, vault.Options{AllowDebug: true})
	if err != nil {
		st.Close()
		t.Fatalf("unlock with %s: %v", src, err)
	}
	return st, v
}

func (h rekeyHome) dataKey(t *testing.T, st *store.Store) store.DataKey {
	t.Helper()
	var dk store.DataKey
	if err := st.Do(t.Context(), func(tx *store.Tx) error {
		var err error
		dk, err = tx.CurrentDataKey()
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return dk
}

// sealed returns the data key and the live credential as they are stored.
func (h rekeyHome) sealed(t *testing.T) (store.DataKey, store.Credential) {
	t.Helper()
	s, _, _ := testSys(t, nil)
	st, err := store.Open(t.Context(), store.Options{Dir: h.data, Now: s.now()})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	dk := h.dataKey(t, st)
	var c store.Credential
	if err := st.Do(t.Context(), func(tx *store.Tx) error {
		c, err = tx.CredentialByName("main")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return dk, c
}

// checkOpensWith fails unless the live credential decrypts under src.
func (h rekeyHome) checkOpensWith(t *testing.T, src vault.KeySource) {
	t.Helper()
	st, v := h.unlock(t, src)
	defer st.Close()
	defer v.Close()
	if err := st.Do(t.Context(), func(tx *store.Tx) error {
		c, err := tx.CredentialByName("main")
		if err != nil {
			return err
		}
		if err := v.Put(c.ID, vault.Sealed{DataKeyID: c.DataKeyID, Nonce: c.Nonce, Ciphertext: c.Ciphertext}); err != nil {
			return err
		}
		if got, _ := v.Credential(c.ID); string(got) != rekeySecret {
			t.Errorf("the credential decrypts to %q, want the sentinel", got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// rekey runs kman rekey on the install and returns its exit code and output.
func (h rekeyHome) rekey(t *testing.T, env map[string]string, answers []string, args ...string) (int, string) {
	t.Helper()
	s, stdout, stderr := testSys(t, env, answers...)
	code := run(t.Context(), append([]string{"rekey", "--config-dir", h.config, "--allow-debug"}, args...), s)
	return code, stdout.String() + stderr.String()
}

func (h rekeyHome) mustRekey(t *testing.T, env map[string]string, answers []string, args ...string) string {
	t.Helper()
	code, out := h.rekey(t, env, answers, args...)
	if code != 0 {
		t.Fatalf("kman rekey exited %d:\n%s", code, out)
	}
	return out
}

// dirBytes is every file in dir, joined.
func dirBytes(t *testing.T, dir string) []byte {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var all []byte
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, b...)
	}
	return all
}

// saved copies the key file aside, so a test can try the old key afterwards.
func saved(t *testing.T, path string) vault.KeySource {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(t.TempDir(), "old.key")
	if err := os.WriteFile(copyPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return vault.FileSource(copyPath)
}

func TestRegression_F8_RekeyLeavesNothingOfTheOldDataKey(t *testing.T) {
	h := newRekeyHome(t, nil, nil)
	oldKey, oldCred := h.sealed(t)

	h.mustRekey(t, nil, nil)

	newKey, newCred := h.sealed(t)
	if newKey.ID == oldKey.ID {
		t.Errorf("the data key is still %s: rekey must replace it, not only wrap it again", oldKey.ID)
	}
	if newCred.DataKeyID != newKey.ID {
		t.Errorf("the credential names data key %s, want %s", newCred.DataKeyID, newKey.ID)
	}
	if bytes.Equal(newCred.Ciphertext, oldCred.Ciphertext) {
		t.Error("the credential's ciphertext did not change")
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(h.db())+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var rows, stale int
	if err := db.QueryRow(`SELECT count(*) FROM data_keys`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM credentials WHERE dek_id <> ?`, newKey.ID).Scan(&stale); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if rows != 1 || stale != 0 {
		t.Errorf("data_keys has %d rows, want 1; %d credentials, deleted ones included, name another data key", rows, stale)
	}
	files := dirBytes(t, h.data)
	for what, b := range map[string][]byte{"wrapped data key": oldKey.Wrapped, "ciphertext": oldCred.Ciphertext} {
		if bytes.Contains(files, b) {
			t.Errorf("the old %s is still in the data directory", what)
		}
	}
}

func TestRekeyedCredentialsOpenUnderTheNewMasterKeyAndNotTheOld(t *testing.T) {
	h := newRekeyHome(t, nil, nil)
	old := saved(t, h.keyFile())
	oldText, err := os.ReadFile(h.keyFile())
	if err != nil {
		t.Fatal(err)
	}

	h.mustRekey(t, nil, nil)

	h.checkOpensWith(t, vault.FileSource(h.keyFile()))
	if b, err := os.ReadFile(h.keyFile()); err != nil {
		t.Fatal(err)
	} else if bytes.Equal(b, oldText) {
		t.Error("the key file still holds the old master key")
	}
	if fi, err := os.Stat(h.keyFile()); err != nil {
		t.Fatal(err)
	} else if fi.Mode().Perm() != fileMode && runtime.GOOS != "windows" {
		t.Errorf("the new key file has mode %v", fi.Mode().Perm())
	}
	if _, err := os.Stat(h.keyFile() + pendingSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the staged key file is still there: %v", err)
	}
	dk, _ := h.sealed(t)
	_, err = vault.Unlock(old, vault.WrappedKey{ID: dk.ID, Nonce: dk.Nonce, Wrapped: dk.Wrapped, Fingerprint: dk.KEKFingerprint}, vault.Options{AllowDebug: true})
	if !errors.Is(err, vault.ErrWrongMasterKey) {
		t.Errorf("the old master key on the new data key: %v, want ErrWrongMasterKey", err)
	}
}

func TestRekeyIsAudited(t *testing.T) {
	h := newRekeyHome(t, nil, nil)
	oldKey, _ := h.sealed(t)
	h.mustRekey(t, nil, nil)
	newKey, _ := h.sealed(t)

	s, _, _ := testSys(t, nil)
	st, err := store.Open(t.Context(), store.Options{Dir: h.data, Now: s.now()})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var latest []store.AuditEntry
	if err := st.Do(t.Context(), func(tx *store.Tx) error {
		latest, err = tx.LatestAuditEntries(1)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	e := latest[0]
	if e.Actor != "system:rekey" || e.Action != "data_key.rekey" || e.TargetID != newKey.ID {
		t.Errorf("audit entry = %+v", e)
	}
	var before, after map[string]any
	if err := json.Unmarshal([]byte(e.BeforeJSON), &before); err != nil {
		t.Fatalf("%q: %v", e.BeforeJSON, err)
	}
	if err := json.Unmarshal([]byte(e.AfterJSON), &after); err != nil {
		t.Fatalf("%q: %v", e.AfterJSON, err)
	}
	if before["data_key"] != oldKey.ID || after["data_key"] != newKey.ID || after["secrets"] != float64(1) {
		t.Errorf("before %v, after %v", before, after)
	}
}

// snapshot is everything a failed rekey must leave as it was.
type snapshot struct {
	db, key, config []byte
	files           []string
}

func (h rekeyHome) snapshot(t *testing.T) snapshot {
	t.Helper()
	read := func(p string) []byte {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	var files []string
	for _, dir := range []string{h.config, h.data} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			files = append(files, e.Name())
		}
	}
	return snapshot{db: masked(read(h.db())), key: read(h.keyFile()), config: read(filepath.Join(h.config, configFile)), files: files}
}

// masked is a database file without its two change counters, the header fields
// SQLite bumps on any open that takes the write lock: the file change counter
// at offset 24 and the version-valid-for number at offset 92. Every other byte,
// each page included, must match.
func masked(db []byte) []byte {
	db = bytes.Clone(db)
	clear(db[24:28])
	clear(db[92:96])
	return db
}

func (h rekeyHome) checkUnchanged(t *testing.T, was snapshot) {
	t.Helper()
	now := h.snapshot(t)
	if !bytes.Equal(now.db, was.db) {
		t.Error("the database file changed beyond its change counters")
	}
	if !bytes.Equal(now.key, was.key) {
		t.Error("the master key file changed")
	}
	if !bytes.Equal(now.config, was.config) {
		t.Error("kman.yaml changed")
	}
	if !slices.Equal(now.files, was.files) {
		t.Errorf("the files are now %v, were %v", now.files, was.files)
	}
}

// sqlExec runs statements on the database directly, going around every repo.
func (h rekeyHome) sqlExec(t *testing.T, stmts ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(h.db())+"?_pragma=foreign_keys(0)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

func TestRekeyRefusesWhileTheServerHoldsTheDatabase(t *testing.T) {
	h := newRekeyHome(t, nil, nil)
	was := h.snapshot(t)
	s, _, _ := testSys(t, nil)
	held, err := store.Open(t.Context(), store.Options{Dir: h.data, Now: s.now()})
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()

	code, out := h.rekey(t, nil, nil)
	if code != 1 {
		t.Fatalf("kman rekey exited %d, want 1:\n%s", code, out)
	}
	if !strings.Contains(out, "stop kman serve") {
		t.Errorf("kman rekey did not say to stop the server:\n%s", out)
	}
	held.Close()
	h.checkUnchanged(t, was)
}

func TestRekeyFailingPartwayLeavesEverythingAsItWas(t *testing.T) {
	for name, sabotage := range map[string][]string{
		// The very last statement, so everything before it had happened.
		"the last write fails": {`CREATE TRIGGER sabotage BEFORE DELETE ON data_keys BEGIN SELECT RAISE(ABORT, 'sabotaged'); END`},
		// The audit entry comes after the data key has been replaced.
		"the audit entry fails": {`CREATE TRIGGER sabotage BEFORE INSERT ON audit_log WHEN NEW.actor = 'system:rekey' BEGIN SELECT RAISE(ABORT, 'sabotaged'); END`},
		// A secret that does not decrypt, after others that do.
		"a secret does not decrypt": {`UPDATE credentials SET ciphertext = x'00' WHERE name = 'main'`},
	} {
		t.Run(name, func(t *testing.T) {
			h := newRekeyHome(t, nil, nil)
			h.sqlExec(t, sabotage...)
			was := h.snapshot(t)

			code, out := h.rekey(t, nil, nil)
			if code != 1 {
				t.Fatalf("kman rekey exited %d, want 1:\n%s", code, out)
			}
			if !strings.Contains(out, "Nothing has changed") {
				t.Errorf("kman rekey did not say the install is untouched:\n%s", out)
			}
			h.checkUnchanged(t, was)
		})
	}
}

func TestAFailedRekeyCanBeRunAgain(t *testing.T) {
	h := newRekeyHome(t, nil, nil)
	h.sqlExec(t, `CREATE TRIGGER sabotage BEFORE DELETE ON data_keys BEGIN SELECT RAISE(ABORT, 'sabotaged'); END`)
	if code, out := h.rekey(t, nil, nil); code != 1 {
		t.Fatalf("kman rekey exited %d, want 1:\n%s", code, out)
	}
	h.checkOpensWith(t, vault.FileSource(h.keyFile()))
	h.sqlExec(t, `DROP TRIGGER sabotage`)
	h.mustRekey(t, nil, nil)
	h.checkOpensWith(t, vault.FileSource(h.keyFile()))
}

func TestRekeyRefusesTheMasterKeyAlreadyInUse(t *testing.T) {
	h := newRekeyHome(t, nil, nil)
	was := h.snapshot(t)
	code, out := h.rekey(t, nil, nil, "--new-key-file", h.keyFile())
	if code != 1 || !strings.Contains(out, "kman keygen") {
		t.Errorf("kman rekey to the same key exited %d:\n%s", code, out)
	}
	h.checkUnchanged(t, was)
}

func TestRekeyRefusesAnOldKeyThatDoesNotUnwrap(t *testing.T) {
	h := newRekeyHome(t, nil, nil)
	writeKey(t, h.keyFile())
	was := h.snapshot(t)
	code, out := h.rekey(t, nil, nil)
	if code != 1 || !strings.Contains(out, "does not unwrap") {
		t.Errorf("kman rekey with the wrong old key exited %d:\n%s", code, out)
	}
	h.checkUnchanged(t, was)
}

// writeKey replaces path with a fresh master key, as kman keygen prints it.
func writeKey(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, vault.EncodeMasterKey(vault.NewMasterKey()), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A crash between the commit and the install leaves the database under the
// new key and the old key in place. The new key must survive a rerun, which
// says how to finish.
func TestARerunAfterACrashKeepsTheStagedKeyAndSaysHowToFinish(t *testing.T) {
	h := newRekeyHome(t, nil, nil)
	oldText, err := os.ReadFile(h.keyFile())
	if err != nil {
		t.Fatal(err)
	}
	h.mustRekey(t, nil, nil)
	pending := h.keyFile() + pendingSuffix
	if err := os.Rename(h.keyFile(), pending); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.keyFile(), oldText, 0o600); err != nil {
		t.Fatal(err)
	}

	code, out := h.rekey(t, nil, nil)
	if code != 1 || !strings.Contains(out, "move "+pending+" over "+h.keyFile()) {
		t.Errorf("the rerun exited %d, and does not say how to finish:\n%s", code, out)
	}
	if err := os.Rename(pending, h.keyFile()); err != nil {
		t.Fatalf("the staged key did not survive the rerun: %v", err)
	}
	h.checkOpensWith(t, vault.FileSource(h.keyFile()))
}

func TestARerunReplacesAStagedKeyFromARekeyThatNeverCommitted(t *testing.T) {
	h := newRekeyHome(t, nil, nil)
	pending := h.keyFile() + pendingSuffix
	writeKey(t, pending)
	h.mustRekey(t, nil, nil)
	h.checkOpensWith(t, vault.FileSource(h.keyFile()))
	if _, err := os.Stat(pending); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the stale staged key is still there: %v", err)
	}
}

func TestRekeyTakesANewKeyFileAndPointsTheConfigAtIt(t *testing.T) {
	h := newRekeyHome(t, nil, nil)
	path := filepath.Join(h.config, configFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append([]byte("# the operator's own note\n"), raw...), fileMode); err != nil {
		t.Fatal(err)
	}
	newKey := filepath.Join(t.TempDir(), "next.key")
	writeKey(t, newKey)

	out := h.mustRekey(t, nil, nil, "--new-key-file", newKey)

	h.checkOpensWith(t, vault.FileSource(newKey))
	s, _, _ := testSys(t, nil)
	cfg, err := loadConfig(path, s.getenv)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MasterKeyFile != newKey || cfg.KeySource != keySourceFile {
		t.Errorf("kman.yaml says %s %s, want file %s", cfg.KeySource, cfg.MasterKeyFile, newKey)
	}
	if edited, _ := os.ReadFile(path); !bytes.Contains(edited, []byte("the operator's own note")) {
		t.Errorf("the edit dropped a comment:\n%s", edited)
	}
	if !strings.Contains(out, h.keyFile()+" still holds the old master key") {
		t.Errorf("kman rekey did not name the old key file:\n%s", out)
	}
}

func TestRekeyWithAKeyFileFromTheEnvironmentLeavesItToTheOperator(t *testing.T) {
	h := newRekeyHome(t, nil, nil)
	env := map[string]string{keyFileEnv: h.keyFile()}
	path := filepath.Join(h.config, configFile)
	before, _ := os.ReadFile(path)

	// It will not make a key over one from outside.
	code, out := h.rekey(t, env, nil)
	if code != 1 || !strings.Contains(out, "--new-key-file") {
		t.Errorf("kman rekey with %s and no new key exited %d:\n%s", keyFileEnv, code, out)
	}

	newKey := filepath.Join(t.TempDir(), "next.key")
	writeKey(t, newKey)
	out = h.mustRekey(t, env, nil, "--new-key-file", newKey)
	if !strings.Contains(out, keyFileEnv) || !strings.Contains(out, newKey) {
		t.Errorf("kman rekey did not say to point %s at the new key:\n%s", keyFileEnv, out)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(after, before) {
		t.Errorf("kman.yaml changed, though the environment names the key:\n%s", after)
	}
	h.checkOpensWith(t, vault.FileSource(newKey))
}

// newPassphraseHome is an install whose master key is sealed with passphrase.
func newPassphraseHome(t *testing.T, passphrase string) (rekeyHome, string) {
	t.Helper()
	dir := t.TempDir()
	h := rekeyHome{config: filepath.Join(dir, "config"), data: filepath.Join(dir, "data")}
	s, stdout, stderr := testSys(t, nil, passphrase, passphrase)
	if code := run(t.Context(), []string{"init", "--config-dir", h.config, "--data-dir", h.data, "--key-source", "passphrase", "--allow-debug"}, s); code != 0 {
		t.Fatalf("init exited %d:\n%s%s", code, stdout, stderr)
	}
	sealed := filepath.Join(h.config, sealedKeyFile)
	h.seed(t, passphraseOf(sealed, passphrase))
	return h, sealed
}

func passphraseOf(path, pass string) vault.KeySource {
	return vault.PassphraseSource(path, func() ([]byte, error) { return []byte(pass), nil })
}

func TestRekeyWithAPassphraseSealsTheNewKeyUnderTheNewPassphrase(t *testing.T) {
	h, sealed := newPassphraseHome(t, "old horse")
	before, err := os.ReadFile(sealed)
	if err != nil {
		t.Fatal(err)
	}
	h.mustRekey(t, nil, []string{"old horse", "new horse", "new horse"})
	h.checkOpensWith(t, passphraseOf(sealed, "new horse"))
	after, err := os.ReadFile(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(before, after) {
		t.Error("the sealed key file did not change")
	}
}

func TestRekeyAsksForTheOldPassphraseBeforeTheNewOne(t *testing.T) {
	h, sealed := newPassphraseHome(t, "old horse")
	before, _ := os.ReadFile(sealed)
	// The wrong old passphrase: it asks for no new one, and writes nothing.
	code, out := h.rekey(t, nil, []string{"wrong horse", "new horse", "new horse"})
	if code != 1 || !strings.Contains(out, "passphrase does not unseal") {
		t.Errorf("kman rekey with the wrong passphrase exited %d:\n%s", code, out)
	}
	if after, _ := os.ReadFile(sealed); !bytes.Equal(before, after) {
		t.Error("the sealed key file changed")
	}
	if _, err := os.Stat(sealed + pendingSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a new key was staged: %v", err)
	}
	h.checkOpensWith(t, passphraseOf(sealed, "old horse"))
}

func TestAMismatchedNewPassphraseChangesNothing(t *testing.T) {
	h, sealed := newPassphraseHome(t, "old horse")
	code, out := h.rekey(t, nil, []string{"old horse", "new horse", "other horse"})
	if code != 1 || !strings.Contains(out, "differ") {
		t.Errorf("kman rekey exited %d:\n%s", code, out)
	}
	h.checkOpensWith(t, passphraseOf(sealed, "old horse"))
}

func TestRekeyMovesAnInstallToAnotherKeySource(t *testing.T) {
	t.Run("file to passphrase", func(t *testing.T) {
		h := newRekeyHome(t, nil, nil)
		out := h.mustRekey(t, nil, []string{"new horse", "new horse"}, "--new-key-source", "passphrase")
		sealed := filepath.Join(h.config, sealedKeyFile)
		h.checkOpensWith(t, passphraseOf(sealed, "new horse"))
		h.checkConfig(t, keySourcePassphrase, sealed)
		if !strings.Contains(out, h.keyFile()+" still holds the old master key") {
			t.Errorf("kman rekey did not name the old key file:\n%s", out)
		}
	})
	t.Run("passphrase to file", func(t *testing.T) {
		h, _ := newPassphraseHome(t, "old horse")
		h.mustRekey(t, nil, []string{"old horse"}, "--new-key-source", "file")
		h.checkOpensWith(t, vault.FileSource(h.keyFile()))
		h.checkConfig(t, keySourceFile, h.keyFile())
	})
	t.Run("file to env", func(t *testing.T) {
		h := newRekeyHome(t, nil, nil)
		code, out := h.rekey(t, nil, nil, "--new-key-source", "env")
		if code != 1 || !strings.Contains(out, "--new-key-file") {
			t.Errorf("kman rekey to env with no new key exited %d:\n%s", code, out)
		}
		newKey := filepath.Join(t.TempDir(), "next.key")
		writeKey(t, newKey)
		out = h.mustRekey(t, nil, nil, "--new-key-source", "env", "--new-key-file", newKey)
		if !strings.Contains(out, vault.EnvVar) {
			t.Errorf("kman rekey did not say to set %s:\n%s", vault.EnvVar, out)
		}
		h.checkOpensWith(t, vault.FileSource(newKey))
		h.checkConfig(t, keySourceEnv, "")
	})
	t.Run("env to env", func(t *testing.T) {
		old := vault.EncodeMasterKey(vault.NewMasterKey())
		t.Setenv(vault.EnvVar, string(old))
		h := newEnvHome(t)
		newKey := filepath.Join(t.TempDir(), "next.key")
		writeKey(t, newKey)
		h.mustRekey(t, nil, nil, "--new-key-file", newKey)
		h.checkOpensWith(t, vault.FileSource(newKey))
		h.checkConfig(t, keySourceEnv, "")
	})
}

// newEnvHome is an install whose master key comes from $KMAN_MASTER_KEY.
func newEnvHome(t *testing.T) rekeyHome {
	t.Helper()
	dir := t.TempDir()
	h := rekeyHome{config: filepath.Join(dir, "config"), data: filepath.Join(dir, "data")}
	s, stdout, stderr := testSys(t, nil)
	if code := run(t.Context(), []string{"init", "--config-dir", h.config, "--data-dir", h.data, "--key-source", "env", "--allow-debug"}, s); code != 0 {
		t.Fatalf("init exited %d:\n%s%s", code, stdout, stderr)
	}
	h.seed(t, vault.EnvSource(io.Discard))
	return h
}

func (h rekeyHome) checkConfig(t *testing.T, source, keyFile string) {
	t.Helper()
	s, _, _ := testSys(t, nil)
	cfg, err := loadConfig(filepath.Join(h.config, configFile), s.getenv)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.KeySource != source || cfg.MasterKeyFile != keyFile {
		t.Errorf("kman.yaml says %q %q, want %q %q", cfg.KeySource, cfg.MasterKeyFile, source, keyFile)
	}
}

func TestRekeyRefusesBadRequestsBeforeTouchingAnything(t *testing.T) {
	h := newRekeyHome(t, nil, nil)
	was := h.snapshot(t)
	inData := filepath.Join(h.data, "next.key")
	for name, c := range map[string]struct {
		args []string
		says string
	}{
		"an unknown source":                {[]string{"--new-key-source", "keychain"}, "not one of"},
		"a stray argument":                 {[]string{"extra"}, "usage: kman rekey"},
		"a plain key for a sealed source":  {[]string{"--new-key-source", "passphrase", "--new-key-file", h.keyFile()}, "leave it out"},
		"systemd-creds with no key":        {[]string{"--new-key-source", "systemd-creds"}, "--new-key-file"},
		"a new key inside the data dir":    {[]string{"--new-key-file", inData}, "inside the data directory"},
		"a new key file that is not there": {[]string{"--new-key-file", filepath.Join(h.config, "missing.key")}, "missing.key"},
	} {
		t.Run(name, func(t *testing.T) {
			code, out := h.rekey(t, nil, nil, c.args...)
			if code != 1 || !strings.Contains(out, c.says) {
				t.Errorf("exited %d, want 1 saying %q:\n%s", code, c.says, out)
			}
			h.checkUnchanged(t, was)
		})
	}
}

func TestRekeyInAContainerMakesNoKey(t *testing.T) {
	h := newRekeyHome(t, nil, nil)
	was := h.snapshot(t)
	s, stdout, stderr := testSys(t, nil)
	s.inContainer = func() bool { return true }
	if code := run(t.Context(), []string{"rekey", "--config-dir", h.config, "--allow-debug"}, s); code != 1 {
		t.Fatalf("exited %d, want 1:\n%s%s", code, stdout, stderr)
	}
	if !strings.Contains(stderr.String(), "kman keygen") {
		t.Errorf("it does not say to bring a key:\n%s", stderr)
	}
	h.checkUnchanged(t, was)
}

func TestRekeyWithNoDatabaseCreatesNone(t *testing.T) {
	h := newRekeyHome(t, nil, nil)
	if err := os.Rename(h.db(), h.db()+".moved"); err != nil {
		t.Fatal(err)
	}
	code, out := h.rekey(t, nil, nil)
	if code != 1 || !strings.Contains(out, "holds no kman database") {
		t.Errorf("exited %d:\n%s", code, out)
	}
	if _, err := os.Stat(h.db()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a database appeared: %v", err)
	}
}

func TestAnInstallThatFailsAfterTheCommitSaysHowToFinish(t *testing.T) {
	h := newRekeyHome(t, nil, nil)
	sealed := filepath.Join(h.config, sealedKeyFile)
	// A directory in the way, with something in it, so the rename cannot replace it.
	if err := os.MkdirAll(filepath.Join(sealed, "in-the-way"), 0o700); err != nil {
		t.Fatal(err)
	}

	code, out := h.rekey(t, nil, []string{"new horse", "new horse"}, "--new-key-source", "passphrase")
	if code != 1 {
		t.Fatalf("kman rekey exited %d, want 1:\n%s", code, out)
	}
	if strings.Contains(out, "Nothing has changed") || !strings.Contains(out, "the database is rekeyed") {
		t.Errorf("kman rekey misreports what happened:\n%s", out)
	}
	if !strings.Contains(out, "move "+sealed+pendingSuffix+" over "+sealed) {
		t.Errorf("kman rekey does not say how to finish:\n%s", out)
	}
	// The way out it names works, and the old key really is finished.
	if err := os.RemoveAll(sealed); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(sealed+pendingSuffix, sealed); err != nil {
		t.Fatal(err)
	}
	h.checkOpensWith(t, passphraseOf(sealed, "new horse"))
}
