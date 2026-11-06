package store_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/store"
)

func TestAnUpstreamKeepsItsAuthAndSDKFields(t *testing.T) {
	s := open(t)
	u := openai("openai")
	u.AuthJSON, u.SDKEnv, u.SDKBasePath = `{"style":"header"}`, "OPENAI_API_KEY", "/v1"
	do(t, s, func(tx *store.Tx) error {
		_, err := tx.CreateUpstream(u)
		return err
	})
	do(t, s, func(tx *store.Tx) error {
		got, err := tx.UpstreamByName("openai")
		if got.AuthJSON != u.AuthJSON || got.SDKEnv != u.SDKEnv || got.SDKBasePath != u.SDKBasePath {
			t.Errorf("read back %+v", got)
		}
		return err
	})
}

func TestUpdateUpstreamRewritesTheRowAndKeepsItsCreation(t *testing.T) {
	s := open(t)
	var before store.Upstream
	do(t, s, func(tx *store.Tx) error {
		var err error
		before, err = tx.CreateUpstream(openai("openai"))
		return err
	})
	later := now.Add(time.Hour)
	want := before
	want.Name, want.PresetVersion, want.Host, want.Port, want.TLS = "renamed", 2, "new.example.com", 8443, false
	want.ServerName, want.PathAllowlistJSON, want.InjectUsage = "tls.example.com", `["GET /x"]`, true
	want.AuthJSON, want.SDKEnv, want.SDKBasePath, want.Profile = `{"style":"basic"}`, "X_KEY", "/x", "generic"
	want.UpdatedAt = later
	var got store.Upstream
	do(t, s, func(tx *store.Tx) error {
		var err error
		got, err = tx.UpdateUpstream(want)
		return err
	})
	if got.Generation <= before.Generation {
		t.Errorf("generation %d, want above %d", got.Generation, before.Generation)
	}
	want.Generation = got.Generation
	if !got.UpdatedAt.Equal(later) || !got.CreatedAt.Equal(now) {
		t.Errorf("created %v, updated %v", got.CreatedAt, got.UpdatedAt)
	}
	want.UpdatedAt, want.CreatedAt = got.UpdatedAt, got.CreatedAt
	if got != want {
		t.Errorf("UpdateUpstream = %+v, want %+v", got, want)
	}
}

func TestUpdateUpstreamRefusesATakenNameAndAMissingRow(t *testing.T) {
	s := open(t)
	var b store.Upstream
	do(t, s, func(tx *store.Tx) error {
		if _, err := tx.CreateUpstream(openai("a")); err != nil {
			return err
		}
		var err error
		b, err = tx.CreateUpstream(openai("b"))
		return err
	})
	err := s.Do(t.Context(), func(tx *store.Tx) error {
		c := b
		c.Name = "a"
		_, err := tx.UpdateUpstream(c)
		return err
	})
	if !errors.Is(err, store.ErrConflict) {
		t.Errorf("renaming onto a taken name = %v, want ErrConflict", err)
	}
	do(t, s, func(tx *store.Tx) error { return tx.DeleteUpstream(b.ID, now) })
	err = s.Do(t.Context(), func(tx *store.Tx) error {
		_, err := tx.UpdateUpstream(b)
		return err
	})
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("updating a deleted upstream = %v, want ErrNotFound", err)
	}
}

func TestUpstreamsListsLiveOnesByName(t *testing.T) {
	s := open(t)
	do(t, s, func(tx *store.Tx) error {
		for _, n := range []string{"zulu", "alpha"} {
			if _, err := tx.CreateUpstream(openai(n)); err != nil {
				return err
			}
		}
		gone, err := tx.CreateUpstream(openai("gone"))
		if err != nil {
			return err
		}
		return tx.DeleteUpstream(gone.ID, now)
	})
	do(t, s, func(tx *store.Tx) error {
		all, err := tx.Upstreams()
		var names []string
		for _, u := range all {
			names = append(names, u.Name)
		}
		if want := []string{"alpha", "zulu"}; !slices.Equal(names, want) {
			t.Errorf("Upstreams = %v, want %v", names, want)
		}
		return err
	})
}
