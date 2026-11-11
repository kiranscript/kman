package admin_test

import (
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/store"
)

func (in *install) seedHistory(t *testing.T, daysBack int) { in.seedHistoryAt(t, in.now, daysBack) }

// seedHistoryAt fills the install with one event a day for daysBack days before
// base, each of which makes an hourly usage row, and a daily spend row for each.
func (in *install) seedHistoryAt(t *testing.T, base time.Time, daysBack int) {
	t.Helper()
	var rows []store.EventRow
	for d := 0; d < daysBack; d++ {
		ts := base.Add(-time.Duration(d)*24*time.Hour - time.Hour)
		rows = append(rows, store.EventRow{Time: ts, Kind: "request", KeyID: "kkkk1111", Status: 200, CostKnown: true, CostMicro: 10})
		in.sqlExec(t, `INSERT INTO spend_daily (key_id, day, cost_micro, requests) VALUES ('kkkk1111', ?, 10, 1)`, ts.Format(time.DateOnly))
	}
	if err := in.store.Do(context.Background(), func(tx *store.Tx) error { return tx.AddEvents(rows) }); err != nil {
		t.Fatal(err)
	}
}

func (in *install) count(t *testing.T, table string) (n int) {
	t.Helper()
	err := in.store.Do(context.Background(), func(tx *store.Tx) error {
		switch table {
		case "events":
			es, err := tx.EventsSince(time.Time{})
			n = len(es)
			return err
		case "usage_hourly":
			us, err := tx.UsageSince(time.Time{})
			n = len(us)
			return err
		}
		t.Fatalf("count: no table %s", table)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// spendRows counts the install's daily spend rows, from outside the store.
func (in *install) spendRows(t *testing.T) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(in.dir, store.FileName))+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM spend_daily`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (in *install) addToken(t *testing.T, name string) {
	t.Helper()
	if _, err := in.svc.CreateAdminToken(as("owner"), admin.CreateAdminTokenRequest{Name: name}); err != nil {
		t.Fatal(err)
	}
}

// allAudit is the whole audit log, oldest first.
func (in *install) allAudit(t *testing.T) []admin.AuditEntry {
	t.Helper()
	page, err := in.svc.Audit(as("owner"), admin.AuditQuery{Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	return page.Entries
}

// drain reads a backup download to a file, closes it, and returns the file.
func (in *install) drain(t *testing.T, d admin.Download) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "copy.db")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	n, err := io.Copy(f, d.Body)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := d.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if n != d.Size {
		t.Errorf("the download held %d bytes, it said %d", n, d.Size)
	}
	return path
}

// stagingLeft lists what is in the data directory besides the database and its
// log: what a backup made and did not clean up.
func (in *install) stagingLeft(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(in.dir)
	if err != nil {
		t.Fatal(err)
	}
	var left []string
	for _, e := range entries {
		if n := e.Name(); !strings.HasPrefix(n, store.FileName) {
			left = append(left, n)
		}
	}
	return left
}
