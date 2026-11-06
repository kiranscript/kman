package releasecheck_test

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Dependabot keeps the pins fresh, and the 7-day cooldown keeps a freshly
// compromised release of a dependency out until it has had time to be noticed.
func TestDependabotUpdatesEveryEcosystemAfterACooldown(t *testing.T) {
	var cfg struct {
		Version int
		Updates []struct {
			PackageEcosystem string   `yaml:"package-ecosystem"`
			Directory        string   `yaml:"directory"`
			Directories      []string `yaml:"directories"`
			Schedule         struct{ Interval string }
			Cooldown         struct {
				DefaultDays int `yaml:"default-days"`
			}
		}
	}
	decode(t, ".github/dependabot.yml", &cfg)
	if cfg.Version != 2 {
		t.Fatalf("dependabot version = %d, want 2", cfg.Version)
	}
	got := map[string][]string{}
	for _, u := range cfg.Updates {
		dirs := u.Directories
		if u.Directory != "" {
			dirs = append(dirs, u.Directory)
		}
		got[u.PackageEcosystem] = append(got[u.PackageEcosystem], dirs...)
		if u.Cooldown.DefaultDays != 7 {
			t.Errorf("%s: cooldown default-days = %d, want 7", u.PackageEcosystem, u.Cooldown.DefaultDays)
		}
		if u.Schedule.Interval == "" {
			t.Errorf("%s: no schedule", u.PackageEcosystem)
		}
	}
	want := map[string][]string{
		"gomod": {"/", "/tools"}, "github-actions": {"/"}, "docker": {"/"},
		"npm": {"/e2e/testdata/sdk/node"}, "pip": {"/e2e/testdata/sdk/python"},
	}
	for eco, dirs := range want {
		g := slices.Clone(got[eco])
		slices.Sort(g)
		if !slices.Equal(g, dirs) {
			t.Errorf("%s directories = %v, want %v", eco, g, dirs)
		}
	}
	if len(got) != len(want) {
		t.Errorf("ecosystems = %v, want %v", got, want)
	}
}

var weekly = regexp.MustCompile(`^\d{1,2} \d{1,2} \* \* [0-6]$`)

func TestScorecardRunsWeeklyAndPublishes(t *testing.T) {
	w := loadWorkflow(t, "scorecard.yml")
	var crons []string
	if sched, ok := w.On["schedule"].([]any); ok {
		for _, s := range sched {
			crons = append(crons, s.(map[string]any)["cron"].(string))
		}
	}
	if len(crons) != 1 || !weekly.MatchString(crons[0]) {
		t.Errorf("scorecard schedule = %v, want one weekly cron", crons)
	}
	found := false
	for _, j := range w.Jobs {
		for _, s := range stepsUsing(j, "ossf/scorecard-action") {
			found = true
			if s.With["publish_results"] != "true" {
				t.Errorf("publish_results = %q, want true (for the badge)", s.With["publish_results"])
			}
		}
		if len(j.Env) != 0 {
			t.Error("Scorecard rejects published results from a job with env vars")
		}
	}
	if !found {
		t.Error("scorecard.yml does not run ossf/scorecard-action")
	}
}

type ruleset struct {
	Name        string
	Target      string
	Enforcement string
	Conditions  struct {
		RefName struct{ Include, Exclude []string } `json:"ref_name"`
	}
	Rules        []struct{ Type string }
	BypassActors []struct {
		ActorID    int    `json:"actor_id"`
		ActorType  string `json:"actor_type"`
		BypassMode string `json:"bypass_mode"`
	} `json:"bypass_actors"`
}

func loadRuleset(t *testing.T, rel string) ruleset {
	t.Helper()
	var rs ruleset
	if err := json.Unmarshal(read(t, rel), &rs); err != nil {
		t.Fatalf("parse %s: %v", rel, err)
	}
	if rs.Target != "tag" || rs.Enforcement != "active" {
		t.Errorf("%s: target %q enforcement %q, want an active tag ruleset", rel, rs.Target, rs.Enforcement)
	}
	if !slices.Equal(rs.Conditions.RefName.Include, []string{"refs/tags/v*"}) || len(rs.Conditions.RefName.Exclude) != 0 {
		t.Errorf("%s: refs = %+v, want exactly refs/tags/v*", rel, rs.Conditions.RefName)
	}
	return rs
}

func (rs ruleset) types() []string {
	var out []string
	for _, r := range rs.Rules {
		out = append(out, r.Type)
	}
	slices.Sort(out)
	return out
}

// Only a maintainer creates a v* tag, and no one, the maintainer included,
// moves or deletes one, so "release.yml@refs/tags/<tag>" names one commit for
// good. Bypass actors skip every rule of a ruleset, hence two rulesets.
func TestTheTagRulesetsLockReleaseTags(t *testing.T) {
	create := loadRuleset(t, ".github/rulesets/release-tags-create.json")
	if !slices.Equal(create.types(), []string{"creation"}) {
		t.Errorf("create ruleset rules = %v, want only creation", create.types())
	}
	// Repository role 5 is admin.
	if len(create.BypassActors) != 1 || create.BypassActors[0].ActorType != "RepositoryRole" ||
		create.BypassActors[0].ActorID != 5 || create.BypassActors[0].BypassMode != "always" {
		t.Errorf("create ruleset bypass = %+v, want only the admin role", create.BypassActors)
	}
	locked := loadRuleset(t, ".github/rulesets/release-tags-locked.json")
	if want := []string{"deletion", "non_fast_forward", "update"}; !slices.Equal(locked.types(), want) {
		t.Errorf("locked ruleset rules = %v, want %v", locked.types(), want)
	}
	if len(locked.BypassActors) != 0 {
		t.Errorf("locked ruleset bypass = %+v, want none", locked.BypassActors)
	}
	apply := string(read(t, "scripts/apply-repo-settings.sh"))
	for _, want := range []string{
		".github/rulesets/release-tags-create.json", ".github/rulesets/release-tags-locked.json",
		"immutable-releases", "sha_pinning_required",
	} {
		if !strings.Contains(apply, want) {
			t.Errorf("scripts/apply-repo-settings.sh does not apply %s", want)
		}
	}
}
