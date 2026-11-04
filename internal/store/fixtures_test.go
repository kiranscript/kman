package store_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/kirank55/kman/internal/store"
)

// TestEveryFixtureMigrates upgrades a copy of every released fixture in
// fixtures/ (one per tag, from fixturegen) to the current schema, and checks
// that its data survived. Later milestones add to the checks: a fixture key
// still authenticates, a credential still decrypts, spend totals are
// unchanged.
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

// counts are what a migration must keep. The fixture is read with plain SQL
// because it holds an older schema than the store's repos expect.
type counts struct {
	installID   string
	audit       int
	upstreams   int
	adminTokens int
	settings    int
}

func countRows(t *testing.T, path string) counts {
	t.Helper()
	db := rawOpen(t, path)
	var c counts
	for q, dst := range map[string]any{
		`SELECT install_id FROM meta`:       &c.installID,
		`SELECT count(*) FROM audit_log`:    &c.audit,
		`SELECT count(*) FROM upstreams`:    &c.upstreams,
		`SELECT count(*) FROM admin_tokens`: &c.adminTokens,
		`SELECT count(*) FROM settings`:     &c.settings,
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
