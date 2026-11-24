package store

import (
	"context"
	"slices"
	"testing"
)

// m1Tables are the tables of migration 0001, from the schema ticket (#25).
var m1Tables = []string{
	"admin_tokens", "alert_channels", "alert_outbox", "audit_log",
	"budget_alerts", "credentials", "data_keys", "events", "key_secrets",
	"meta", "price_overrides", "settings", "spend_daily", "upstreams",
	"usage_hourly", "virtual_keys",
}

// m2Tables are the tables migration 0002 adds so far (#83).
var m2Tables = []string{"baseline_places", "canaries"}

func TestEveryMigrationCreatesStrictTables(t *testing.T) {
	s := openTest(t, t.TempDir())
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT name, strict FROM pragma_table_list WHERE schema = 'main' AND type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var name string
		var strict int
		if err := rows.Scan(&name, &strict); err != nil {
			t.Fatal(err)
		}
		if strict != 1 {
			t.Errorf("table %s is not STRICT", name)
		}
		got = append(got, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := slices.Sorted(slices.Values(slices.Concat(m1Tables, m2Tables)))
	if !slices.Equal(got, want) {
		t.Errorf("tables = %v\nwant     %v", got, want)
	}
}

func TestOpenRecordsTheSchemaVersion(t *testing.T) {
	s := openTest(t, t.TempDir())
	if got := userVersion(t, s); got != 2 || SchemaVersion != 2 || len(migrations) != SchemaVersion {
		t.Errorf("user_version = %d, SchemaVersion = %d, %d migrations; want 2 each", got, SchemaVersion, len(migrations))
	}
}

func userVersion(t *testing.T, s *Store) int {
	t.Helper()
	var v int
	if err := s.db.QueryRowContext(context.Background(), "PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}
