package store_test

import (
	"testing"
	"time"

	"github.com/kirank55/kman/internal/store"
)

func TestPutSettingInsertsThenReplaces(t *testing.T) {
	s := open(t)
	var first, second store.Setting
	do(t, s, func(tx *store.Tx) error {
		var err error
		first, err = tx.PutSetting(store.Setting{Key: "spend_timezone", ValueJSON: `"UTC"`, UpdatedAt: now})
		return err
	})
	later := now.Add(time.Hour)
	do(t, s, func(tx *store.Tx) error {
		var err error
		second, err = tx.PutSetting(store.Setting{Key: "spend_timezone", ValueJSON: `"Europe/Berlin"`, UpdatedAt: later})
		return err
	})
	if first.Generation == 0 || second.Generation <= first.Generation {
		t.Errorf("generations %d then %d, want rising", first.Generation, second.Generation)
	}
	var got []store.Setting
	do(t, s, func(tx *store.Tx) error {
		var err error
		got, err = tx.Settings()
		return err
	})
	want := store.Setting{Key: "spend_timezone", ValueJSON: `"Europe/Berlin"`, UpdatedAt: later, Generation: second.Generation}
	if len(got) != 1 || got[0] != want {
		t.Errorf("Settings = %+v, want [%+v]", got, want)
	}
}

func TestSettingsOfANewInstallIsEmpty(t *testing.T) {
	s := open(t)
	do(t, s, func(tx *store.Tx) error {
		got, err := tx.Settings()
		if len(got) != 0 {
			t.Errorf("Settings = %+v, want none", got)
		}
		return err
	})
}

func TestLearningPeriodIsTheSettingOrSevenDays(t *testing.T) {
	s := open(t)
	read := func() (d time.Duration, err error) {
		err = s.Do(t.Context(), func(tx *store.Tx) (e error) { d, e = tx.LearningPeriod(); return })
		return
	}
	if d, err := read(); err != nil || d != 7*24*time.Hour {
		t.Errorf("LearningPeriod with no setting = %v, %v, want 7 days", d, err)
	}
	for value, want := range map[string]time.Duration{"259200": 72 * time.Hour, "0": 0} {
		do(t, s, func(tx *store.Tx) error {
			_, err := tx.PutSetting(store.Setting{Key: store.SettingLearningPeriod, ValueJSON: value, UpdatedAt: now})
			return err
		})
		if d, err := read(); err != nil || d != want {
			t.Errorf("LearningPeriod with %s stored = %v, %v, want %v", value, d, err, want)
		}
	}
	do(t, s, func(tx *store.Tx) error {
		_, err := tx.PutSetting(store.Setting{Key: store.SettingLearningPeriod, ValueJSON: `"soon"`, UpdatedAt: now})
		return err
	})
	if _, err := read(); err == nil {
		t.Error("a learning period that is not a number of seconds was read")
	}
}
