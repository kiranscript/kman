package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/store"
	"github.com/kirank55/kman/internal/vault"
)

func TestGenerateIsDeterministic(t *testing.T) {
	a := generateTo(t, t.TempDir(), "v0.1.0")
	b := generateTo(t, t.TempDir(), "v0.1.0")
	x, err := os.ReadFile(a)
	if err != nil {
		t.Fatal(err)
	}
	y, err := os.ReadFile(b)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(x, y) {
		t.Error("two runs wrote different fixtures")
	}
}

func TestGenerateWritesAnOpenableInstall(t *testing.T) {
	path := generateTo(t, t.TempDir(), "v0.1.0")
	if filepath.Base(path) != "v0.1.0.db" {
		t.Errorf("fixture is %s, want v0.1.0.db", filepath.Base(path))
	}
	dir := t.TempDir()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, store.FileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(context.Background(), store.Options{Dir: dir, Now: fixedNow})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	err = s.Do(context.Background(), func(tx *store.Tx) error {
		// The admin service made these too, with their real preset data.
		upstreams, err := tx.Upstreams()
		if err != nil {
			return err
		}
		var names []string
		for _, u := range upstreams {
			names = append(names, u.Name)
		}
		if got := strings.Join(names, " "); got != "acme anthropic openai" {
			t.Errorf("upstreams = %s, want acme anthropic openai (and gone deleted)", got)
		}
		openai, err := tx.UpstreamByName("openai")
		if err != nil {
			return err
		}
		if openai.Preset != "openai" || openai.PresetVersion != 1 || openai.SDKEnv != "OPENAI_API_KEY" {
			t.Errorf("openai = %+v", openai)
		}
		acme, err := tx.UpstreamByName("acme")
		if err != nil {
			return err
		}
		if acme.PriceMultiplierPPM != 1_100_000 {
			t.Errorf("acme's price multiplier = %d", acme.PriceMultiplierPPM)
		}
		overrides, err := tx.PriceOverrides()
		if err != nil {
			return err
		}
		var scopes []string
		for _, o := range overrides {
			scopes = append(scopes, o.Scope+"/"+o.Model)
		}
		if want := []string{"install/fixture-model", acme.ID + "/fixture-model"}; !slices.Equal(scopes, want) {
			t.Errorf("overrides = %v, want %v (and those on gone, and removed-model, deleted)", scopes, want)
		}
		// The admin service made these, as a real install's would be.
		tokens, err := tx.AdminTokens()
		if err != nil {
			return err
		}
		names = nil
		for _, tok := range tokens {
			state := "live"
			if !tok.RevokedAt.IsZero() {
				state = "revoked"
			}
			names = append(names, tok.Name+":"+state)
		}
		if got := strings.Join(names, " "); got != "ci:live old:revoked owner:live" {
			t.Errorf("admin tokens = %s, want ci:live old:revoked owner:live", got)
		}
		settings, err := tx.Settings()
		if err != nil {
			return err
		}
		if len(settings) != 2 {
			t.Errorf("settings = %+v, want default_key_ttl and spend_timezone set", settings)
		}
		entries, err := tx.AuditEntries(0, 100)
		if len(entries) == 0 {
			return errors.New("no audit entries")
		}
		return err
	})
	if err != nil {
		t.Error(err)
	}
}

// The committed test-only master key still unlocks a fixture's credentials,
// which is what the migration test relies on.
func TestAFixturesCredentialsDecryptUnderTheCommittedKey(t *testing.T) {
	path := generateTo(t, t.TempDir(), "v0.1.0")
	dir := t.TempDir()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, store.FileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(context.Background(), store.Options{Dir: dir, Now: fixedNow})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	v, err := unlock(context.Background(), s, vault.FileSource("test-master.key"))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if err := admin.New(admin.Options{Store: s, Now: func() time.Time { return fixedNow }, Vault: v}).Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"openai":  "sk-fixture-openai-0123456789abcdef",
		"primary": "sk-fixture-anthropic-rotated-0123456789abcdef",
		"acme":    "fixture-user:fixture-password",
	}
	err = s.Do(context.Background(), func(tx *store.Tx) error {
		creds, err := tx.Credentials()
		if err != nil {
			return err
		}
		if len(creds) != len(want) {
			t.Errorf("credentials = %+v, want %d", creds, len(want))
		}
		for _, c := range creds {
			if plain, ok := v.Credential(c.ID); !ok || string(plain) != want[c.Name] {
				t.Errorf("credential %s decrypts to %q, %v", c.Name, plain, ok)
			}
			if bytes.Contains(data, []byte(want[c.Name])) {
				t.Errorf("the fixture holds credential %s in the clear", c.Name)
			}
		}
		return nil
	})
	if err != nil {
		t.Error(err)
	}
}

// unlock opens a fixture's data key from src.
func unlock(ctx context.Context, s *store.Store, src vault.KeySource) (*vault.Vault, error) {
	var dk store.DataKey
	err := s.Do(ctx, func(tx *store.Tx) error {
		var err error
		dk, err = tx.CurrentDataKey()
		return err
	})
	if err != nil {
		return nil, err
	}
	return vault.Unlock(src, vault.WrappedKey{ID: dk.ID, Nonce: dk.Nonce, Wrapped: dk.Wrapped, Fingerprint: dk.KEKFingerprint}, vault.Options{AllowDebug: true})
}

func TestGenerateRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	generateTo(t, dir, "v0.1.0")
	if err := generate(context.Background(), dir, "v0.1.0"); err == nil {
		t.Error("a second generate over the same fixture succeeded")
	}
}

func TestGenerateRefusesABadVersion(t *testing.T) {
	for _, v := range []string{"", "0.1.0", "v0.1", "v0.1.0/../x", "latest"} {
		if err := generate(context.Background(), t.TempDir(), v); err == nil {
			t.Errorf("generate with version %q succeeded", v)
		}
	}
}

func generateTo(t *testing.T, dir, version string) string {
	t.Helper()
	if err := generate(context.Background(), dir, version); err != nil {
		t.Fatalf("generate: %v", err)
	}
	return filepath.Join(dir, version+".db")
}
