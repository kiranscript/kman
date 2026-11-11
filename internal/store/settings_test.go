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
