package admin_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/store"
)

var t0 = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

// install is an admin service over its own store, with a clock the test
// moves and the owner token already made, as init leaves it.
type install struct {
	svc     *admin.Service
	store   *store.Store
	now     time.Time
	owner   string // the owner token's secret
	applied []admin.Change
}

func newInstall(t *testing.T) *install {
	t.Helper()
	st, err := store.Open(context.Background(), store.Options{Dir: t.TempDir(), Now: t0})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	in := &install{store: st, now: t0}
	in.svc = admin.New(admin.Options{
		Store: st,
		Now:   func() time.Time { return in.now },
		Apply: func(c admin.Change) { in.applied = append(in.applied, c) },
	})
	created, err := in.svc.CreateAdminToken(as("system:init"), admin.CreateAdminTokenRequest{Name: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	in.owner = created.Secret
	return in
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
