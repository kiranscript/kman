// Command fixturegen writes the fixture database of a release, for the
// store's migration test (SPEC §14 and §17). The release checklist runs it
// at each tag and commits the result before tagging:
//
//	go run ./internal/store/fixturegen --version v0.1.0
//
// writes internal/store/fixtures/v0.1.0.db. The output depends only on the
// code: the clock is fixed and every random id comes from a fixed seed, so a
// rerun writes the same bytes.
//
// It drives the admin service where a command exists, so the fixture holds
// what a real install holds: admin tokens and settings so far. Upstreams are
// still seeded into the store directly until their commands land (#34),
// which will also add sentinel credentials under a committed test-only
// master key.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/store"
)

// fixedNow is the time every fixture is made at.
var fixedNow = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// seed is the fixed seed of every random id in a fixture.
var seed = [32]byte([]byte("kman fixture generator seed v1.."))

var versionRE = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(-rc\.[0-9]+)?$`)

func main() {
	version := flag.String("version", "", "the release tag the fixture is for, such as v0.1.0")
	dir := flag.String("dir", filepath.Join("internal", "store", "fixtures"), "the directory to write <version>.db to")
	flag.Parse()
	if err := generate(context.Background(), *dir, *version); err != nil {
		fmt.Fprintln(os.Stderr, "fixturegen:", err)
		os.Exit(1)
	}
}

// generate writes dir/<version>.db. It refuses to overwrite a fixture: a
// released fixture never changes.
func generate(ctx context.Context, dir, version string) error {
	if !versionRE.MatchString(version) {
		return fmt.Errorf("version %q is not a release tag such as v0.1.0", version)
	}
	dst := filepath.Join(dir, version+".db")
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("%s exists; a released fixture never changes", dst)
	}
	work, err := os.MkdirTemp("", "kman-fixturegen-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	if err := seedInstall(ctx, work); err != nil {
		return err
	}
	return copyNew(filepath.Join(work, store.FileName), dst)
}

// seedInstall creates an install in dir and fills it with one of each thing
// the store can hold so far.
func seedInstall(ctx context.Context, dir string) error {
	rng := rand.NewChaCha8(seed)
	s, err := store.Open(ctx, store.Options{Dir: dir, Now: fixedNow, Rand: rng})
	if err != nil {
		return err
	}
	err = errors.Join(seedAdmin(ctx, s, rng), seedStore(ctx, s))
	// Closing checkpoints the WAL, so the database file is complete.
	return errors.Join(err, s.Close())
}

// seedAdmin makes what the admin commands make: the owner token as init
// does, a live and a revoked token, a rotation, and changed settings.
func seedAdmin(ctx context.Context, s *store.Store, rng io.Reader) error {
	svc := admin.New(admin.Options{Store: s, Now: func() time.Time { return fixedNow }, Rand: rng})
	as := func(actor string) context.Context {
		return admin.WithCaller(ctx, admin.Caller{Actor: actor, ClientIP: "127.0.0.1"})
	}
	ttl, tz := int64(30*24*60*60), "Europe/Berlin"
	for _, step := range []func() error{
		func() error {
			_, err := svc.CreateAdminToken(as("system:init"), admin.CreateAdminTokenRequest{Name: "owner"})
			return err
		},
		func() error {
			_, err := svc.CreateAdminToken(as("owner"), admin.CreateAdminTokenRequest{Name: "ci"})
			return err
		},
		func() error {
			_, err := svc.RotateAdminToken(as("owner"), admin.AdminTokenName{Name: "ci"})
			return err
		},
		func() error {
			_, err := svc.CreateAdminToken(as("owner"), admin.CreateAdminTokenRequest{Name: "old"})
			return err
		},
		func() error {
			_, err := svc.RevokeAdminToken(as("owner"), admin.AdminTokenName{Name: "old"})
			return err
		},
		func() error {
			_, err := svc.UpdateSettings(as("ci"), admin.SettingsUpdate{DefaultKeyTTLSeconds: &ttl, SpendTimezone: &tz})
			return err
		},
	} {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

// seedStore writes what has no admin command yet.
func seedStore(ctx context.Context, s *store.Store) error {
	return s.Do(ctx, func(tx *store.Tx) error {
		u, err := tx.CreateUpstream(store.Upstream{
			Name: "openai", Preset: "openai", PresetVersion: 1, Profile: "openai",
			Host: "api.openai.com", Port: 443, TLS: true, PathAllowlistJSON: `["/v1/"]`,
			PriceMultiplierPPM: 1_000_000, CreatedAt: fixedNow, UpdatedAt: fixedNow,
		})
		if err != nil {
			return err
		}
		_, err = tx.AppendAudit(store.AuditEntry{
			TS: fixedNow, Actor: "owner", Action: "upstream.create",
			TargetKind: "upstream", TargetID: u.ID, AfterJSON: `{"name":"openai"}`,
		})
		return err
	})
}

// copyNew copies src to dst, which must not exist.
func copyNew(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, out.Close()) }()
	_, err = io.Copy(out, in)
	return err
}
