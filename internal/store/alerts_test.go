package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/store"
)

// withDataKey is a store holding a data key, as an alert channel needs.
func withDataKey(t *testing.T) *store.Store {
	t.Helper()
	s := open(t)
	do(t, s, func(tx *store.Tx) error {
		return tx.CreateDataKey(store.DataKey{ID: "dek_1", Wrapped: []byte("w"), Nonce: []byte("n"), KEKFingerprint: "mkf_1", CreatedAt: now})
	})
	return s
}

func channel(id, name string) store.AlertChannel {
	return store.AlertChannel{
		ID: id, Name: name, Kind: "slack", ConfigCiphertext: []byte("sealed"), Nonce: []byte("nonce"), DataKeyID: "dek_1",
		DisplayHost: "hooks.example.com", MinSeverity: "medium", LifecycleAlerts: true, CreatedAt: now, UpdatedAt: now,
	}
}

func TestAChannelIsStoredAndReadBackByIdAndName(t *testing.T) {
	s := withDataKey(t)
	var created store.AlertChannel
	do(t, s, func(tx *store.Tx) error {
		var err error
		created, err = tx.CreateAlertChannel(channel("ch_1", "ops"))
		return err
	})
	if created.Generation == 0 {
		t.Error("the channel has no generation")
	}
	do(t, s, func(tx *store.Tx) error {
		byID, err := tx.AlertChannel("ch_1")
		if err != nil {
			return err
		}
		byName, err := tx.AlertChannelByName("ops")
		if err != nil {
			return err
		}
		want := channel("ch_1", "ops")
		for _, got := range []store.AlertChannel{byID, byName} {
			got.Generation = 0
			if string(got.ConfigCiphertext) != "sealed" || got.Name != want.Name || got.Kind != "slack" || got.DisplayHost != want.DisplayHost ||
				got.MinSeverity != "medium" || !got.LifecycleAlerts || !got.CreatedAt.Equal(now) {
				t.Errorf("channel = %+v, want %+v", got, want)
			}
		}
		return nil
	})
}

func TestAChannelNameIsUniqueAmongLiveChannelsAndFreedByDeleting(t *testing.T) {
	s := withDataKey(t)
	do(t, s, func(tx *store.Tx) error {
		_, err := tx.CreateAlertChannel(channel("ch_1", "ops"))
		return err
	})
	err := s.Do(t.Context(), func(tx *store.Tx) error {
		_, err := tx.CreateAlertChannel(channel("ch_2", "ops"))
		return err
	})
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("a second live channel named ops: %v, want ErrConflict", err)
	}
	do(t, s, func(tx *store.Tx) error { return tx.DeleteAlertChannel("ch_1", now) })
	do(t, s, func(tx *store.Tx) error {
		if _, err := tx.AlertChannel("ch_1"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("a deleted channel is read back: %v, want ErrNotFound", err)
		}
		_, err := tx.CreateAlertChannel(channel("ch_2", "ops"))
		return err
	})
	do(t, s, func(tx *store.Tx) error {
		got, err := tx.AlertChannels()
		if err != nil || len(got) != 1 || got[0].ID != "ch_2" {
			t.Errorf("AlertChannels = %+v, %v; want only ch_2", got, err)
		}
		return nil
	})
}

func TestDeletingAChannelGivesUpItsPendingAlerts(t *testing.T) {
	s := withDataKey(t)
	do(t, s, func(tx *store.Tx) error {
		if _, err := tx.CreateAlertChannel(channel("ch_1", "ops")); err != nil {
			return err
		}
		if _, err := tx.EnqueueAlert(store.OutboxAlert{ChannelID: "ch_1", Kind: "audit", PayloadJSON: "{}", CreatedAt: now}); err != nil {
			return err
		}
		return tx.DeleteAlertChannel("ch_1", now.Add(time.Minute))
	})
	do(t, s, func(tx *store.Tx) error {
		stats, err := tx.OutboxStats()
		if err != nil {
			return err
		}
		if got := stats["ch_1"]; got.Pending != 0 || got.Failed != 1 {
			t.Errorf("OutboxStats[ch_1] = %+v, want the alert given up (failed 1, pending 0)", got)
		}
		return nil
	})
}

func TestAnAlertIsDueWhenItsTimeComesAndPriorityGoesFirst(t *testing.T) {
	s := withDataKey(t)
	do(t, s, func(tx *store.Tx) error {
		for _, id := range []string{"ch_1", "ch_2"} {
			if _, err := tx.CreateAlertChannel(channel(id, id)); err != nil {
				return err
			}
		}
		for _, a := range []store.OutboxAlert{
			{ChannelID: "ch_1", Kind: "audit", PayloadJSON: `{"n":1}`, CreatedAt: now},
			{ChannelID: "ch_1", Kind: "audit", PayloadJSON: `{"n":2}`, CreatedAt: now, NextAttemptAt: now.Add(time.Hour)},
			{ChannelID: "ch_1", Kind: "canary", PayloadJSON: `{"n":3}`, CreatedAt: now, Priority: true},
			{ChannelID: "ch_2", Kind: "audit", PayloadJSON: `{"n":4}`, CreatedAt: now},
		} {
			if _, err := tx.EnqueueAlert(a); err != nil {
				return err
			}
		}
		return nil
	})
	do(t, s, func(tx *store.Tx) error {
		due, err := tx.DueAlerts("ch_1", now, 10)
		if err != nil {
			return err
		}
		if len(due) != 2 || due[0].PayloadJSON != `{"n":3}` || due[1].PayloadJSON != `{"n":1}` {
			t.Errorf("DueAlerts(ch_1, now) = %+v, want the priority alert, then the first", due)
		}
		next, ok, err := tx.NextAlertDue("ch_1")
		if err != nil || !ok || !next.Equal(now) {
			t.Errorf("NextAlertDue = %v, %v, %v; want now", next, ok, err)
		}
		later, err := tx.DueAlerts("ch_1", now.Add(time.Hour), 10)
		if err != nil || len(later) != 3 {
			t.Errorf("an hour later %d are due (%v), want 3", len(later), err)
		}
		return nil
	})
}

func TestAnAlertIsMarkedDeliveredRetriedOrFailed(t *testing.T) {
	s := withDataKey(t)
	var a, b, c store.OutboxAlert
	do(t, s, func(tx *store.Tx) error {
		if _, err := tx.CreateAlertChannel(channel("ch_1", "ops")); err != nil {
			return err
		}
		enqueue := func(out *store.OutboxAlert) error {
			var err error
			*out, err = tx.EnqueueAlert(store.OutboxAlert{ChannelID: "ch_1", Kind: "audit", PayloadJSON: "{}", CreatedAt: now})
			return err
		}
		for _, out := range []*store.OutboxAlert{&a, &b, &c} {
			if err := enqueue(out); err != nil {
				return err
			}
		}
		return nil
	})
	if a.ID == 0 || a.ID == b.ID {
		t.Fatalf("ids %d, %d", a.ID, b.ID)
	}
	retryAt := now.Add(30 * time.Second)
	do(t, s, func(tx *store.Tx) error {
		if err := tx.AlertDelivered(a.ID, now.Add(time.Second)); err != nil {
			return err
		}
		if err := tx.AlertRetry(b.ID, retryAt, "slack answered 503"); err != nil {
			return err
		}
		return tx.AlertFailed(c.ID, now.Add(2*time.Second), "slack answered 404")
	})
	do(t, s, func(tx *store.Tx) error {
		due, err := tx.DueAlerts("ch_1", now.Add(time.Minute), 10)
		if err != nil {
			return err
		}
		if len(due) != 1 || due[0].ID != b.ID || due[0].Attempts != 1 || due[0].LastError != "slack answered 503" || !due[0].NextAttemptAt.Equal(retryAt) {
			t.Errorf("due = %+v, want only b, once attempted, to retry at %v", due, retryAt)
		}
		stats, err := tx.OutboxStats()
		if err != nil {
			return err
		}
		if got := stats["ch_1"]; got.Pending != 1 || got.Failed != 1 || got.Delivered != 1 || got.LastError != "slack answered 404" {
			t.Errorf("OutboxStats[ch_1] = %+v, want 1 pending, 1 failed, 1 delivered", got)
		}
		return nil
	})
}
