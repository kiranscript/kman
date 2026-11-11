package admin_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/profiles"
)

// withSpend stands for the meter, which admin may not import: it answers for a
// key with what the composition root would make of the meter and the policy.
func (in *install) withSpend(t *testing.T) {
	t.Helper()
	in.spend = func(keyID, policy string) admin.KeySpend {
		cap := int64(0)
		if strings.Contains(policy, "5000000") {
			cap = 5_000_000
		}
		return admin.KeySpend{
			Today: admin.SpendPeriod{Period: "2026-11-04", CostMicro: 1_500_000, EstimatedMicro: 100, Requests: 3, UnmeteredRequests: 1, CapMicro: cap},
			Month: admin.SpendPeriod{Period: "2026-11", CostMicro: 9_000_000, Requests: 30},
		}
	}
	in.svc = in.service(profiles.Presets())
}

func TestAKeyShowsItsSpendTodayAndThisMonthBesideItsCaps(t *testing.T) {
	in, _ := withCredential(t)
	in.withSpend(t)
	created := in.createKey(t, admin.CreateKeyRequest{Name: "my-app", Upstream: "openai", Policy: json.RawMessage(`{"daily_cap_micro":5000000}`)})
	got, err := in.svc.GetKey(as("owner"), admin.KeyRef{ID: created.Key.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got.Spend == nil || got.Spend.Today.CostMicro != 1_500_000 || got.Spend.Today.CapMicro != 5_000_000 ||
		got.Spend.Month.CostMicro != 9_000_000 || got.Spend.Month.CapMicro != 0 || got.Spend.Today.UnmeteredRequests != 1 {
		t.Errorf("Spend = %+v", got.Spend)
	}
	list, err := in.svc.ListKeys(as("owner"), admin.KeyQuery{})
	if err != nil || len(list.Keys) != 1 || list.Keys[0].Spend == nil || list.Keys[0].Spend.Month.Requests != 30 {
		t.Errorf("ListKeys = %+v, %v: the list shows spend too", list, err)
	}
}

// Spend is a reading, not a record: the audit entries of a key must not hold it,
// or each would be stale the moment it was written.
func TestSpendIsNotWrittenToTheAudit(t *testing.T) {
	in, _ := withCredential(t)
	in.withSpend(t)
	created := in.createKey(t, admin.CreateKeyRequest{Name: "my-app", Upstream: "openai"})
	if created.Key.Spend != nil {
		t.Errorf("a new key came with spend: %+v", created.Key.Spend)
	}
	in.svc.UpdateKey(as("owner"), admin.KeyUpdate{ID: created.Key.ID, Note: ptr("x")})
	for _, e := range in.latest(t, 2) {
		if strings.Contains(string(e.After), "spend") || strings.Contains(string(e.Before), "spend") {
			t.Errorf("an audit entry holds spend: %+v", e)
		}
	}
}

func TestWithNoMeterAKeyShowsNoSpend(t *testing.T) {
	in, _ := withCredential(t)
	created := in.createKey(t, admin.CreateKeyRequest{Name: "my-app", Upstream: "openai"})
	got, err := in.svc.GetKey(as("owner"), admin.KeyRef{ID: created.Key.ID})
	if err != nil || got.Spend != nil {
		t.Errorf("GetKey = %+v, %v", got, err)
	}
}
