package store_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/store"
	"github.com/kirank55/kman/internal/vault"
)

// TestEveryFixtureMigrates upgrades a copy of every released fixture in
// fixtures/ (one per tag, from fixturegen) to the current schema, and checks
// that its data survived and its credentials still decrypt under the
// committed test-only master key. Later tickets add to the checks: a fixture
// key still authenticates, spend totals are unchanged.
func TestEveryFixtureMigrates(t *testing.T) {
	fixtures, err := filepath.Glob(filepath.Join("fixtures", "*.db"))
	if err != nil {
		t.Fatal(err)
	}
	if len(fixtures) == 0 {
		t.Log("no fixtures yet; the first is committed with v0.1.0")
	}
	for _, f := range fixtures {
		t.Run(filepath.Base(f), func(t *testing.T) {
			dir := t.TempDir()
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, store.FileName)
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			before := countRows(t, path)

			s, err := store.Open(context.Background(), store.Options{Dir: dir, Now: now})
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			checkCredentialsDecrypt(t, s)
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			if v := schemaVersion(t, path); v != store.SchemaVersion {
				t.Errorf("user_version = %d, want %d", v, store.SchemaVersion)
			}
			if after := countRows(t, path); after != before {
				t.Errorf("rows after migrating = %+v, want %+v", after, before)
			}
		})
	}
}

// checkCredentialsDecrypt unlocks the fixture's data key and opens each of its
// credentials, which fixturegen makes as sentinels.
func checkCredentialsDecrypt(t *testing.T, s *store.Store) {
	t.Helper()
	err := s.Do(context.Background(), func(tx *store.Tx) error {
		dk, err := tx.CurrentDataKey()
		if err != nil {
			return err
		}
		v, err := vault.Unlock(vault.FileSource(filepath.Join("fixturegen", "test-master.key")),
			vault.WrappedKey{ID: dk.ID, Nonce: dk.Nonce, Wrapped: dk.Wrapped, Fingerprint: dk.KEKFingerprint},
			vault.Options{AllowDebug: true})
		if err != nil {
			return err
		}
		defer v.Close()
		creds, err := tx.Credentials()
		if err != nil {
			return err
		}
		if len(creds) == 0 {
			t.Error("the fixture holds no credentials")
		}
		for _, c := range creds {
			if err := v.Put(c.ID, vault.Sealed{DataKeyID: c.DataKeyID, Nonce: c.Nonce, Ciphertext: c.Ciphertext}); err != nil {
				return err
			}
			if plain, ok := v.Credential(c.ID); !ok || !strings.Contains(string(plain), "fixture") {
				t.Errorf("credential %s does not decrypt to a sentinel", c.Name)
			}
		}
		return nil
	})
	if err != nil {
		t.Errorf("the fixture's credentials: %v", err)
	}
}

// counts are what a migration must keep. The fixture is read with plain SQL
// because it holds an older schema than the store's repos expect.
type counts struct {
	installID   string
	audit       int
	upstreams   int
	adminTokens int
	settings    int
	credentials int
	keys        int
	keySecrets  int
	overrides   int
	// What a release's data is worth, which no migration may change: the
	// keys by state, their policies, and spend totals.
	keyStates string
	policies  string
	spend     string
}

func countRows(t *testing.T, path string) counts {
	t.Helper()
	db := rawOpen(t, path)
	var c counts
	for q, dst := range map[string]any{
		`SELECT install_id FROM meta`:          &c.installID,
		`SELECT count(*) FROM audit_log`:       &c.audit,
		`SELECT count(*) FROM upstreams`:       &c.upstreams,
		`SELECT count(*) FROM admin_tokens`:    &c.adminTokens,
		`SELECT count(*) FROM settings`:        &c.settings,
		`SELECT count(*) FROM credentials`:     &c.credentials,
		`SELECT count(*) FROM virtual_keys`:    &c.keys,
		`SELECT count(*) FROM key_secrets`:     &c.keySecrets,
		`SELECT count(*) FROM price_overrides`: &c.overrides,
	} {
		if err := db.QueryRow(q).Scan(dst); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for q, dst := range map[string]*string{
		`SELECT coalesce(group_concat(status || ':' || n, ','), '') FROM (SELECT status, count(*) n FROM virtual_keys GROUP BY status ORDER BY status)`:                                                           &c.keyStates,
		`SELECT coalesce(group_concat(policy, '|'), '') FROM (SELECT policy_json || '@' || policy_version AS policy FROM virtual_keys ORDER BY created_at, key_id)`:                                               &c.policies,
		`SELECT coalesce(sum(cost_micro), 0) || '/' || coalesce(sum(estimated_micro), 0) || '/' || coalesce(sum(requests), 0) || '/' || coalesce(sum(unmetered_requests), 0) || '/' || count(*) FROM spend_daily`: &c.spend,
	} {
		if err := db.QueryRow(q).Scan(dst); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	return c
}

func schemaVersion(t *testing.T, path string) int {
	t.Helper()
	var v int
	if err := rawOpen(t, path).QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func rawOpen(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// TestEveryReleaseInTheChangelogHasAFixture is the pull-request side of the
// release checklist's fixture step (SPEC §15): a CHANGELOG section headed
// "## [X.Y.Z]" means a release is cut or being cut, and it must come with
// fixtures/vX.Y.Z.db. The release job runs scripts/check-fixture.sh on the
// pushed tag, so a tag with no fixture never builds.
func TestEveryReleaseInTheChangelogHasAFixture(t *testing.T) {
	text, err := os.ReadFile(filepath.Join("..", "..", "CHANGELOG.md"))
	if err != nil {
		t.Fatal(err)
	}
	heading := regexp.MustCompile(`(?m)^## \[([0-9]+\.[0-9]+\.[0-9]+[^\]]*)\]`)
	versions := heading.FindAllStringSubmatch(string(text), -1)
	if len(versions) == 0 {
		t.Fatal("CHANGELOG.md has no released section")
	}
	for _, m := range versions {
		if _, err := os.Stat(filepath.Join("fixtures", "v"+m[1]+".db")); err != nil {
			t.Errorf("CHANGELOG.md has a section for %s but fixtures/v%s.db is missing: run go run ./internal/store/fixturegen --version v%s and commit it", m[1], m[1], m[1])
		}
	}
}
