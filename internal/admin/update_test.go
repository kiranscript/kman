package admin_test

import (
	"context"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/admin"
)

func TestUpdateStatusIsOffUntilTheOperatorTurnsTheCheckOn(t *testing.T) {
	in := newInstall(t)
	got, err := in.svc.UpdateStatus(as("owner"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Enabled || got.Available {
		t.Errorf("UpdateStatus with no check = %+v, want it off", got)
	}
}

func TestUpdateStatusReportsWhatTheCheckLearned(t *testing.T) {
	in := newInstall(t)
	checked := t0.Add(-time.Hour)
	svc := admin.New(admin.Options{Store: in.store, Now: func() time.Time { return t0 }, Update: func() admin.UpdateStatus {
		return admin.UpdateStatus{Enabled: true, Current: "v0.1.0", Latest: "v0.2.0", Available: true, URL: "https://example.test/v0.2.0", CheckedAt: &checked}
	}})
	got, err := svc.UpdateStatus(as("owner"))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Enabled || !got.Available || got.Latest != "v0.2.0" || got.CheckedAt == nil || !got.CheckedAt.Equal(checked) {
		t.Errorf("UpdateStatus = %+v", got)
	}
	if _, err := svc.UpdateStatus(context.Background()); code(err) != admin.CodeUnauthorized {
		t.Errorf("UpdateStatus with no caller = %v, want unauthorized", err)
	}
}
