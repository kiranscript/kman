package profiles_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/pricing"
	"github.com/kirank55/kman/internal/profiles"
	"github.com/kirank55/kman/internal/registry"
)

var update = flag.Bool("update", false, "rewrite testdata/presets.json from the presets")

const lockFile = "presets.json"

func byName(t *testing.T, name string) registry.Preset {
	t.Helper()
	i := slices.IndexFunc(profiles.Presets(), func(p registry.Preset) bool { return p.Name == name })
	if i < 0 {
		t.Fatalf("no preset %s", name)
	}
	return profiles.Presets()[i]
}

// The list is keman's, written out by hand (SPEC §5).
func TestTheBinaryHoldsKemansTwentyOnePresets(t *testing.T) {
	want := []string{
		"openai", "anthropic", "openrouter", "gemini", "groq", "mistral", "deepseek", "xai", "together", "fireworks", "cerebras",
		"stripe", "github", "slack", "twilio", "sendgrid", "resend", "postmark", "cloudflare", "linear", "notion",
	}
	var got []string
	for _, p := range profiles.Presets() {
		got = append(got, p.Name)
	}
	if !slices.Equal(got, want) {
		t.Errorf("presets = %v, want %v", got, want)
	}
}

func TestEveryPresetIsAValidVersionedUpstream(t *testing.T) {
	for _, p := range profiles.Presets() {
		t.Run(p.Name, func(t *testing.T) {
			if err := p.Spec.Validate(); err != nil {
				t.Error(err)
			}
			if p.Version < 1 {
				t.Errorf("version = %d", p.Version)
			}
			if p.Title == "" || p.Spec.SDKEnv == "" {
				t.Errorf("title %q, SDK env %q", p.Title, p.Spec.SDKEnv)
			}
			if p.Spec.Port != 443 || !p.Spec.TLS {
				t.Errorf("port %d, TLS %v, want 443 over TLS", p.Spec.Port, p.Spec.TLS)
			}
		})
	}
}

func TestPresetsCarryTheirUpstreamsAuthShapeAndSDKEnv(t *testing.T) {
	bearer := registry.Auth{Style: registry.AuthHeader, Header: "Authorization: Bearer {key}"}
	for _, tc := range []struct {
		name, host, profile, env, base string
		auth                           registry.Auth
		inject                         bool
	}{
		{"openai", "api.openai.com", "openai", "OPENAI_API_KEY", "/v1", bearer, true},
		{"anthropic", "api.anthropic.com", "anthropic", "ANTHROPIC_API_KEY", "", registry.Auth{Style: registry.AuthHeader, Header: "x-api-key: {key}"}, false},
		{"openrouter", "openrouter.ai", "openai", "OPENROUTER_API_KEY", "/api/v1", bearer, true},
		{"mistral", "api.mistral.ai", "openai", "MISTRAL_API_KEY", "/v1", bearer, false},
		{"stripe", "api.stripe.com", "generic", "STRIPE_SECRET_KEY", "", bearer, false},
		{"twilio", "api.twilio.com", "generic", "TWILIO_AUTH_TOKEN", "", registry.Auth{Style: registry.AuthBasic}, false},
		{"postmark", "api.postmarkapp.com", "generic", "POSTMARK_SERVER_TOKEN", "", registry.Auth{Style: registry.AuthHeader, Header: "X-Postmark-Server-Token: {key}"}, false},
		{"linear", "api.linear.app", "generic", "LINEAR_API_KEY", "", registry.Auth{Style: registry.AuthHeader, Header: "Authorization: {key}"}, false},
	} {
		s := byName(t, tc.name).Spec
		if s.Host != tc.host || s.Profile != tc.profile || s.SDKEnv != tc.env || s.SDKBasePath != tc.base || s.Auth != tc.auth || s.InjectUsage != tc.inject {
			t.Errorf("%s = %+v", tc.name, s)
		}
	}
}

// SPEC §7: the default LLM allowlists exclude batch, files, fine-tune, and
// multipart audio and image routes.
func TestLLMPresetsLeaveOutTheRoutesSPECExcludes(t *testing.T) {
	excluded := []string{"batch", "/files", "fine_tuning", "fine-tun", "audio/transcriptions", "audio/translations", "images/edits", "images/variations"}
	for _, p := range profiles.Presets() {
		if p.Spec.Profile == registry.ProfileGeneric {
			continue
		}
		for _, rule := range p.Spec.Paths {
			for _, x := range excluded {
				if strings.Contains(rule, x) {
					t.Errorf("%s allows %q, which SPEC §7 excludes by default", p.Name, rule)
				}
			}
		}
	}
}

// An app sets its SDK's base URL to kman plus SDKBasePath, so a preset must
// allow something under that path, or it cannot be used.
func TestEverySDKBasePathReachesAnAllowedRoute(t *testing.T) {
	for _, p := range profiles.Presets() {
		if p.Spec.SDKBasePath == "" {
			continue
		}
		reachable := slices.ContainsFunc(p.Spec.Paths, func(rule string) bool {
			_, path, _ := strings.Cut(rule, " ")
			return strings.HasPrefix(path, p.Spec.SDKBasePath+"/")
		})
		if !reachable {
			t.Errorf("%s: nothing is allowed under its base path %s", p.Name, p.Spec.SDKBasePath)
		}
	}
}

func TestChangingPresetsDoesNotChangeTheBinarysData(t *testing.T) {
	first := profiles.Presets()
	first[0].Spec.Paths[0] = "POST /changed"
	first[0].Name = "changed"
	second := profiles.Presets()
	if second[0].Name == "changed" || second[0].Spec.Paths[0] == "POST /changed" {
		t.Error("Presets shares its data with its caller")
	}
}

// An upstream records its preset's name, and pricing looks a model up by that
// name: a preset that misses its section would price nothing from it, and every
// model only it lists would be refused on a capped key. pricegen names the
// sections by hand, so the two lists could drift apart in either direction.
func TestLLMPresetsAndThePriceTableSectionsNameTheSameThings(t *testing.T) {
	table := pricing.Builtin()
	sections := map[string]string{} // a section, and one model in it
	for row := range table.All() {
		sections[row.Preset] = row.Model
	}
	global, ok := sections[""]
	if !ok {
		t.Fatal("the price table has no global section")
	}
	// OpenAI's and Anthropic's rows are the global section, which every lookup
	// falls back to (#38).
	inGlobal := []string{"openai", "anthropic"}

	presets := map[string]bool{}
	for _, p := range profiles.Presets() {
		presets[p.Name] = true
		if p.Spec.Profile == registry.ProfileGeneric {
			if _, ok := sections[p.Name]; ok {
				t.Errorf("preset %s is unmetered, but the price table has a section for it", p.Name)
			}
			continue
		}
		model, ok := sections[p.Name]
		if !ok {
			if !slices.Contains(inGlobal, p.Name) {
				t.Errorf("LLM preset %s has no section in the price table", p.Name)
				continue
			}
			model = global
		}
		if _, ok := table.BuiltinEntry(p.Name, model); !ok {
			t.Errorf("preset %s does not find %s in the price table", p.Name, model)
		}
	}
	for section := range sections {
		if section != "" && !presets[section] {
			t.Errorf("the price table has a section %s, which no preset names", section)
		}
	}
}

// locked is what testdata/presets.json records of a preset.
type locked struct {
	Version int    `json:"version"`
	SHA256  string `json:"sha256"`
}

// TestAChangedPresetBumpsItsVersion compares the presets to their committed
// record. An upgrade never changes an existing upstream; sync offers a preset
// only when its version rises, so a change without a bump would never reach
// anyone. Rerun with -update once the version is bumped.
func TestAChangedPresetBumpsItsVersion(t *testing.T) {
	path := filepath.Join("testdata", lockFile)
	current := map[string]locked{}
	for _, p := range profiles.Presets() {
		b, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(b)
		current[p.Name] = locked{Version: p.Version, SHA256: hex.EncodeToString(sum[:])}
	}
	raw, err := os.ReadFile(path)
	if err != nil && !*update {
		t.Fatal(err)
	}
	record := map[string]locked{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &record); err != nil {
			t.Fatal(err)
		}
	}
	for name, now := range current {
		was, known := record[name]
		switch {
		case !known:
		case was.SHA256 != now.SHA256 && now.Version <= was.Version:
			t.Errorf("preset %s changed but stayed at version %d: bump its version", name, was.Version)
		case now.Version < was.Version:
			t.Errorf("preset %s went from version %d back to %d", name, was.Version, now.Version)
		}
		if !*update && was != now {
			t.Errorf("preset %s is not as testdata/%s records: rerun with -update", name, lockFile)
		}
	}
	if !*update {
		return
	}
	if t.Failed() {
		t.Fatal("not rewriting the record")
	}
	b, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		t.Fatal(fmt.Errorf("write the record: %w", err))
	}
}
