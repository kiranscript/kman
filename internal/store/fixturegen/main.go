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
// For now it seeds the store directly. Once the admin service exists (#33),
// it drives that instead, with sentinel credentials under a committed
// test-only master key, so the fixture holds what a real install holds.
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
	s, err := store.Open(ctx, store.Options{Dir: dir, Now: fixedNow, Rand: rand.NewChaCha8(seed)})
	if err != nil {
		return err
	}
	err = s.Do(ctx, func(tx *store.Tx) error {
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
	// Closing checkpoints the WAL, so the database file is complete.
	return errors.Join(err, s.Close())
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
