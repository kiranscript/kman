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
// It drives the real admin service with the real presets, so the fixture holds
// what a real install holds: admin tokens, settings, upstreams, and sentinel
// credentials under the committed test-only master key in test-master.key,
// which the migration test also reads to check they still decrypt. As later
// tickets add commands, they add them here.
package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
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
	"github.com/kirank55/kman/internal/profiles"
	"github.com/kirank55/kman/internal/registry"
	"github.com/kirank55/kman/internal/store"
	"github.com/kirank55/kman/internal/vault"
)

// fixedNow is the time every fixture is made at.
var fixedNow = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// seed is the fixed seed of every random id in a fixture.
var seed = [32]byte([]byte("kman fixture generator seed v1.."))

// masterKeyFile is the fixture's master key. It unlocks nothing but fixtures.
//
//go:embed test-master.key
var masterKeyFile []byte

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
// the admin service can make so far.
func seedInstall(ctx context.Context, dir string) error {
	rng := rand.NewChaCha8(seed)
	s, err := store.Open(ctx, store.Options{Dir: dir, Now: fixedNow, Rand: rng})
	if err != nil {
		return err
	}
	err = seedAdmin(ctx, s, rng)
	// Closing checkpoints the WAL, so the database file is complete.
	return errors.Join(err, s.Close())
}

// masterKey is the committed test-only master key as a key source.
type masterKey struct{}

func (masterKey) MasterKey() ([]byte, error) {
	enc := base64.StdEncoding
	text := bytes.TrimSpace(masterKeyFile)
	mk := make([]byte, enc.DecodedLen(len(text)))
	n, err := enc.Decode(mk, text)
	return mk[:n], err
}

func (masterKey) String() string { return "the fixture's test-only master key" }

// newDataKey makes the fixture's data key, as init does.
func newDataKey(ctx context.Context, s *store.Store, rng io.Reader) (*vault.Vault, error) {
	id, err := store.NewID(rng, "dek_")
	if err != nil {
		return nil, err
	}
	// AllowDebug: this is a build tool holding a key that protects nothing.
	v, wk, err := vault.Create(masterKey{}, id, vault.Options{AllowDebug: true, Rand: rng})
	if err != nil {
		return nil, err
	}
	err = s.Do(ctx, func(tx *store.Tx) error {
		return tx.CreateDataKey(store.DataKey{ID: wk.ID, Wrapped: wk.Wrapped, Nonce: wk.Nonce, KEKFingerprint: wk.Fingerprint, CreatedAt: fixedNow})
	})
	if err != nil {
		v.Close()
		return nil, err
	}
	return v, nil
}

// seedAdmin makes what the admin commands make: the owner token as init
// does, a live and a revoked token, a rotation, changed settings, upstreams
// from a preset and by hand, credentials on them, one replaced and renamed,
// and an upstream and its credential deleted.
func seedAdmin(ctx context.Context, s *store.Store, rng io.Reader) error {
	v, err := newDataKey(ctx, s, rng)
	if err != nil {
		return err
	}
	defer v.Close()
	reg := registry.New()
	svc := admin.New(admin.Options{Store: s, Now: func() time.Time { return fixedNow }, Rand: rng,
		Vault: v, Presets: profiles.Presets(), Apply: reg.Apply})
	as := func(actor string) context.Context {
		return admin.WithCaller(ctx, admin.Caller{Actor: actor, ClientIP: "127.0.0.1"})
	}
	ttl, tz := int64(30*24*60*60), "Europe/Berlin"
	gw, rotated, renamed := "gw.example.test", admin.Secret("sk-fixture-anthropic-rotated-0123456789abcdef"), "primary"
	inputPrice, outputPrice, cachedPrice, maxOutput, uplift := int64(2_000_000), int64(8_000_000), int64(500_000), int64(4096), int64(1_100_000)
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
		func() error {
			_, err := svc.AddUpstream(as("owner"), admin.AddUpstreamRequest{Name: "openai", Preset: "openai",
				Credential: &admin.NewCredential{Secret: "sk-fixture-openai-0123456789abcdef"}})
			return err
		},
		func() error {
			_, err := svc.AddUpstream(as("owner"), admin.AddUpstreamRequest{Preset: "anthropic"})
			return err
		},
		func() error {
			_, err := svc.AddCredential(as("owner"), admin.AddCredentialRequest{Name: "anthropic-main", Upstream: "anthropic", Secret: "sk-fixture-anthropic-0123456789abcdef"})
			return err
		},
		func() error {
			_, err := svc.UpdateCredential(as("ci"), admin.CredentialUpdate{Ref: "anthropic-main", Name: &renamed, Secret: &rotated})
			return err
		},
		func() error {
			paths := []string{"POST /v1/things", "GET /v1/things/*"}
			_, err := svc.AddUpstream(as("owner"), admin.AddUpstreamRequest{Name: "acme",
				UpstreamSettings: admin.UpstreamSettings{Host: &gw, Paths: &paths, Auth: &admin.Auth{Style: "basic"}},
				Credential:       &admin.NewCredential{Secret: "fixture-user:fixture-password"}})
			return err
		},
		func() error {
			_, err := svc.UpdateUpstream(as("ci"), admin.UpstreamUpdate{Ref: "acme", UpstreamSettings: admin.UpstreamSettings{ServerName: &gw}})
			return err
		},
		func() error {
			_, err := svc.AddUpstream(as("owner"), admin.AddUpstreamRequest{Name: "gone", Preset: "groq",
				Credential: &admin.NewCredential{Secret: "sk-fixture-gone-0123456789abcdef"}})
			return err
		},
		func() error {
			_, err := svc.SetPriceOverride(as("owner"), admin.PriceOverrideSet{Upstream: "gone", Model: "gone-model", InputMicro: &inputPrice, OutputMicro: &outputPrice, MaxOutput: &maxOutput})
			return err
		},
		func() error {
			_, err := svc.RemoveUpstream(as("owner"), admin.UpstreamRef{Ref: "gone"})
			return err
		},
		func() error {
			req := admin.PriceOverrideSet{Model: "fixture-model", InputMicro: &inputPrice, OutputMicro: &outputPrice, MaxOutput: &maxOutput}
			req.CachedMicro = &cachedPrice
			_, err := svc.SetPriceOverride(as("owner"), req)
			return err
		},
		func() error {
			_, err := svc.SetPriceOverride(as("ci"), admin.PriceOverrideSet{Upstream: "acme", Model: "fixture-model", InputMicro: &outputPrice, OutputMicro: &outputPrice, MaxOutput: &maxOutput})
			return err
		},
		func() error {
			_, err := svc.SetPriceOverride(as("ci"), admin.PriceOverrideSet{Model: "removed-model", InputMicro: &inputPrice, OutputMicro: &outputPrice, MaxOutput: &maxOutput})
			return err
		},
		func() error {
			_, err := svc.RemovePriceOverride(as("ci"), admin.PriceOverrideRef{Model: "removed-model"})
			return err
		},
		func() error {
			_, err := svc.UpdateUpstream(as("ci"), admin.UpstreamUpdate{Ref: "acme", UpstreamSettings: admin.UpstreamSettings{PriceMultiplierPPM: &uplift}})
			return err
		},
	} {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
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
