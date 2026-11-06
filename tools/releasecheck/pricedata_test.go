package releasecheck_test

import (
	"slices"
	"strings"
	"testing"
)

// ADR 0007: a weekly job regenerates the table and opens a pull request with
// the diff, and nothing is merged without a person.
func TestThePriceDataJobOpensAPullRequestEveryWeek(t *testing.T) {
	w := loadWorkflow(t, "price-data.yml")
	sched, ok := w.On["schedule"].([]any)
	if !ok || len(sched) != 1 {
		t.Fatalf("on.schedule = %#v, want one weekly cron", w.On["schedule"])
	}
	cron, _ := sched[0].(map[string]any)["cron"].(string)
	fields := strings.Fields(cron)
	if len(fields) != 5 || fields[2] != "*" || fields[3] != "*" || fields[4] == "*" {
		t.Errorf("cron %q is not weekly", cron)
	}
	if _, ok := w.On["workflow_dispatch"]; !ok {
		t.Error("no workflow_dispatch: a maintainer can't run it on demand")
	}
	j, ok := w.Jobs["update"]
	if !ok {
		t.Fatalf("no update job: %v", w.Jobs)
	}
	perms, _ := j.Permissions.(map[string]any)
	if perms["contents"] != "write" || perms["pull-requests"] != "write" || len(perms) != 2 {
		t.Errorf("permissions = %v, want contents and pull-requests write, nothing else", j.Permissions)
	}
	var all string
	for _, s := range j.Steps {
		all += s.Run + "\n"
	}
	for _, want := range []string{
		"scripts/update-price-data.sh",
		"pricegen check --base",
		"gh pr create",
		"git diff --quiet -- internal/pricing/table.json",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("the job's steps lack %q:\n%s", want, all)
		}
	}
	for _, banned := range []string{"gh pr merge", "--admin", "git push origin main"} {
		if strings.Contains(all, banned) {
			t.Errorf("the job must leave merging to a person, but has %q", banned)
		}
	}
}

// CI flags any max output equal to the context window, and any changed value,
// on every pull request.
func TestCIFlagsPriceDataOnEveryPullRequest(t *testing.T) {
	w := loadWorkflow(t, "ci.yml")
	j, ok := w.Jobs["price-flags"]
	if !ok {
		t.Fatalf("no price-flags job")
	}
	var all string
	for _, s := range j.Steps {
		all += s.Run + "\n"
	}
	for _, want := range []string{"pricegen check --base", "--github", "GITHUB_STEP_SUMMARY", "BASE_SHA"} {
		if !strings.Contains(all, want) {
			t.Errorf("the job's steps lack %q:\n%s", want, all)
		}
	}
	if needs := needsOf(w.Jobs["required"]); !slices.Contains(needs, "price-flags") {
		t.Errorf("job required needs %v, want price-flags", needs)
	}
}

func TestTheRepoSettingsLetTheWeeklyJobOpenAPullRequest(t *testing.T) {
	apply := string(read(t, "scripts/apply-repo-settings.sh"))
	if !strings.Contains(apply, "can_approve_pull_request_reviews=true") {
		t.Error("scripts/apply-repo-settings.sh does not allow Actions to open pull requests")
	}
}

func TestOnlyTheUpdateScriptDownloadsPriceData(t *testing.T) {
	script := string(read(t, "scripts/update-price-data.sh"))
	for _, want := range []string{"BerriAI/litellm", "pydantic/genai-prices/main/prices/new_data/v2/data.json", "models.dev/api.json", "pricegen generate"} {
		if !strings.Contains(script, want) {
			t.Errorf("scripts/update-price-data.sh lacks %q", want)
		}
	}
}
