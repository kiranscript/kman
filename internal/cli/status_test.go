package cli_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/cli"
	"github.com/kirank55/kman/internal/registry"
)

// summaryOf runs plain kman's summary with this environment.
func (in *install) summaryOf(t *testing.T, env map[string]string, initialised bool) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := cli.Status(context.Background(), cli.Env{
		Stdout: &stdout, Stderr: &stderr, Stdin: strings.NewReader(""),
		Getenv:      func(k string) string { return env[k] },
		ConfigDir:   in.configDir,
		Initialised: func() bool { return initialised },
	})
	if code != cli.ExitOK {
		t.Errorf("the summary exited %d", code)
	}
	if stderr.Len() != 0 {
		t.Errorf("the summary wrote to stderr: %s", stderr.String())
	}
	return stdout.String()
}

func wantNext(t *testing.T, out, where, next string) {
	t.Helper()
	if !strings.Contains(out, where) || !strings.Contains(out, "Next: "+next+"\n") {
		t.Errorf("want %q and Next: %s, in:\n%s", where, next, out)
	}
}

func TestTheSummaryOfAMachineWithNoInstallNamesInit(t *testing.T) {
	in := newInstall(t) // no credentials file, and not initialised
	wantNext(t, in.summaryOf(t, nil, false), "not initialised", "kman init")
}

func TestTheSummaryOfAnInstallThatIsNotRunningNamesServe(t *testing.T) {
	in := newInstall(t)
	url := "http://" + closedPort(t)
	for name, tc := range map[string]struct {
		env         map[string]string
		initialised bool
		loggedIn    bool
	}{
		"init done, no token saved": {map[string]string{cli.URLEnv: url}, true, false},
		"token saved":               {nil, true, true},
		"environment says where":    {map[string]string{cli.URLEnv: url, cli.TokenEnv: in.owner}, false, false},
		"a URL and no install here": {map[string]string{cli.URLEnv: url}, false, false},
	} {
		t.Run(name, func(t *testing.T) {
			if tc.loggedIn {
				if err := cli.SaveCredentials(in.credentials(), url, in.owner); err != nil {
					t.Fatal(err)
				}
			}
			wantNext(t, in.summaryOf(t, tc.env, tc.initialised), "not running", "kman serve")
		})
	}
}

func TestTheSummaryOfARunningInstallWithoutAGoodTokenNamesLogin(t *testing.T) {
	in := newInstall(t)
	out := in.summaryOf(t, map[string]string{cli.URLEnv: in.url}, true)
	wantNext(t, out, "no admin token", "kman login --admin-url "+in.url)

	out = in.summaryOf(t, map[string]string{cli.URLEnv: in.url, cli.TokenEnv: "kma_not-a-real-token"}, true)
	wantNext(t, out, "refuses the token", "kman login --admin-url "+in.url)
}

func TestTheSummaryWalksTheStagesOfARunningInstall(t *testing.T) {
	in := newInstall(t).loggedIn(t)
	wantNext(t, in.summaryOf(t, nil, true), "no upstream", "kman upstream add openai --preset openai")

	in.kmanIn(t, "", "upstream", "add", "openai", "--preset", "openai", "--no-credential")
	wantNext(t, in.summaryOf(t, nil, true), "no upstream has a credential", "kman credential add <name> --upstream openai")

	in.kmanIn(t, secret+"\n", "credential", "add", "main", "--upstream", "openai")
	wantNext(t, in.summaryOf(t, nil, true), "no key that works", "kman key create my-app --upstream openai")

	in.kman(t, "key", "create", "my-app", "--upstream", "openai")
	wantNext(t, in.summaryOf(t, nil, true), "kman is ready: 1 working key on 1 upstream.", "kman key list")
}

func TestOnlyAWorkingKeyMakesAnInstallReady(t *testing.T) {
	in := withOpenAI(t)
	var disabled, revoked admin.CreatedKey
	in.kmanJSON(t, &disabled, "key", "create", "off", "--upstream", "openai")
	in.kmanJSON(t, &revoked, "key", "create", "gone", "--upstream", "openai")
	in.setKeyStatus(t, disabled.Key.ID, "disabled")
	in.setKeyStatus(t, revoked.Key.ID, "revoked")
	in.sqlExec(t, `INSERT INTO virtual_keys (key_id, credential_id, kind, status, policy_version, expires_at, created_at, generation)
		SELECT 'expired1', id, 'standard', 'active', 1, 1, 0, 1 FROM credentials`)
	wantNext(t, in.summaryOf(t, nil, true), "no key that works", "kman key create my-app --upstream openai")
}

func TestTheSummarySaysWhenAPresetIsNewer(t *testing.T) {
	old := registry.Preset{Name: "acme", Version: 1, Spec: registry.Spec{Profile: registry.ProfileGeneric, Host: "api.acme.test", Port: 443, TLS: true,
		Paths: []string{"* /**"}, Auth: registry.Auth{Style: registry.AuthHeader, Header: "Authorization: Bearer {key}"}}}
	in := newInstallWith(t, []registry.Preset{old}).loggedIn(t)
	in.kmanIn(t, "", "upstream", "add", "acme", "--preset", "acme", "--no-credential")
	out := in.summaryOf(t, nil, true)
	if strings.Contains(out, "newer preset") {
		t.Errorf("a preset that is current is reported:\n%s", out)
	}
	newer := old
	newer.Version = 2
	in.upgrade(t, []registry.Preset{newer})
	out = in.summaryOf(t, nil, true)
	if !strings.Contains(out, "kman upstream sync acme") {
		t.Errorf("no sync hint in:\n%s", out)
	}
}

func TestTheSummaryShowsThePriceDataAtEveryStage(t *testing.T) {
	in := newInstall(t)
	if out := in.status(t, admin.PriceDataStatus{AgeDays: 2}); !strings.Contains(out, "2 days old") {
		t.Errorf("no install: no age of the local price data in:\n%s", out)
	}
	in.loggedIn(t)
	if out := in.summaryOf(t, nil, true); !strings.Contains(out, fmt.Sprintf("%d days old", 3)) {
		t.Errorf("running: no age of the server's price data in:\n%s", out)
	}
}
