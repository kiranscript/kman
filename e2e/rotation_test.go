package e2e_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// thingWith makes the lifecycle tests' request with this virtual key.
func (p *proxied) thingWith(secret string) (*http.Response, string) {
	p.t.Helper()
	return sendThrough(p.t, p.s, "POST", "/v1/things", strings.NewReader("{}"), http.Header{"Authorization": {"Bearer " + secret}})
}

func (p *proxied) wantThing(secret, which string) {
	p.t.Helper()
	if resp, body := p.thingWith(secret); resp.StatusCode != 201 {
		p.t.Errorf("%s: %d %s, want it to work", which, resp.StatusCode, body)
	}
}

func (p *proxied) wantNoThing(secret, which string) {
	p.t.Helper()
	resp, body := p.thingWith(secret)
	wantError(p.t, resp, body, 401, "invalid_key")
	if resp.StatusCode == 201 {
		p.t.Errorf("%s works: %s", which, body)
	}
}

// rotated is key rotate --json.
type rotated struct {
	Key struct {
		ID string `json:"id"`
	} `json:"key"`
	Secret             string `json:"secret"`
	PreviousValidUntil string `json:"previous_valid_until"`
}

func (p *proxied) rotate(args ...string) rotated {
	p.t.Helper()
	out, _ := p.h.mustRun(p.t, append([]string{"key", "rotate", "my-app", "--json"}, args...)...)
	var got rotated
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.Secret == "" {
		p.t.Fatalf("key rotate --json printed %q: %v", out, err)
	}
	return got
}

// E2E containment: rotate with a grace keeps both secrets for the grace, with no
// restart and no wait for anything but the grace itself, and then only the new one.
func TestRotatingWithAGraceKeepsBothSecretsForTheGraceAndThenOnlyTheNewOne(t *testing.T) {
	p := behindKman(t, "generic-json-thrice")
	id := p.h.showKey(t, "my-app").ID
	old := p.secret

	got := p.rotate("--grace", "3s")
	if got.Key.ID != id || got.Secret == old || !strings.HasPrefix(got.Secret, "kmn_"+id+"_") {
		t.Fatalf("rotated = %+v, want a new secret for key %s", got, id)
	}
	until, err := time.Parse(time.RFC3339, got.PreviousValidUntil)
	if err != nil {
		t.Fatal(err)
	}

	p.wantThing(old, "the old secret in its grace")
	p.wantThing(got.Secret, "the new secret in the grace")
	time.Sleep(time.Until(until) + 300*time.Millisecond)
	p.wantNoThing(old, "the old secret after the grace")
	p.wantThing(got.Secret, "the new secret after the grace")
	if n := len(p.mock.requests()); n != 3 {
		t.Errorf("the upstream got %d requests, want the 3 made with a working secret", n)
	}
	if k := p.h.showKey(t, "my-app"); k.ID != id {
		t.Errorf("the key is now %s, want %s: a rotation keeps the key", k.ID, id)
	}
}

func TestRotatingWithNoGraceStopsTheOldSecretAtTheNextRequest(t *testing.T) {
	p := behindKman(t, "generic-json")
	old := p.secret
	got := p.rotate()
	if got.PreviousValidUntil != "" {
		t.Errorf("a rotation with no grace has no previous secret: %+v", got)
	}
	p.wantNoThing(old, "the old secret")
	p.wantThing(got.Secret, "the new secret")
}

// F4: keman's rotation made a new key, which reset the spend and the learning
// period. kman rotates the secret and keeps the key, so nothing resets.
func TestRegression_F4_RotationKeepsTheKeyItsSpendAndItsPolicy(t *testing.T) {
	p := behindKmanAs(t, "openai-chat", "openai")
	p.setPrices("gpt-4o-mini")
	p.setPolicy(`{"daily_cap_micro":5000000}`)
	if resp, b := p.chat(chatRequest); resp.StatusCode != 200 {
		t.Fatalf("chat: %d %s", resp.StatusCode, b)
	}
	before := p.settled(1)
	if before.Spend.Today.CostMicro == 0 {
		t.Fatalf("the request cost nothing: %+v", before.Spend.Today)
	}
	id := p.h.showKey(t, "my-app").ID

	p.rotate()

	after := p.spend()
	if after.Spend.Today != before.Spend.Today || after.Spend.Month != before.Spend.Month {
		t.Errorf("spend after rotation = %+v, want what it was, %+v", after.Spend, before.Spend)
	}
	if string(after.Policy) != string(before.Policy) || after.Spend.Today.CapMicro != 5000000 {
		t.Errorf("policy after rotation = %s, cap %d", after.Policy, after.Spend.Today.CapMicro)
	}
	if k := p.h.showKey(t, "my-app"); k.ID != id {
		t.Errorf("the key is now %s, want %s", k.ID, id)
	}
	out, _ := p.h.mustRun(t, "key", "list", "--json")
	if strings.Count(out, `"id"`) != 1 {
		t.Errorf("a rotation left more than one key:\n%s", out)
	}
}

// E2E containment: a credential compromise rotates every key on the credential.
func TestACredentialCompromiseRotatesEveryKeyOnTheCredential(t *testing.T) {
	p := behindKman(t, "generic-json-twice")
	other := p.h.createKey(t, "other-app", "mock")
	mine := p.secret
	const replaced = "sk-live-e2e-replacement-0123456789abcdefQRST"

	out, _, code := p.h.runWithInput(t, replaced+"\n", "credential", "compromise", "mock", "--json")
	if code != 0 {
		t.Fatalf("credential compromise exited %d:\n%s", code, out)
	}
	var got struct {
		RotatedKeys []rotated `json:"rotated_keys"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || len(got.RotatedKeys) != 2 {
		t.Fatalf("credential compromise --json printed %q: %v", out, err)
	}
	if strings.Contains(out, replaced) {
		t.Errorf("the output holds the credential: %s", out)
	}

	p.wantNoThing(mine, "my-app's old secret")
	p.wantNoThing(other, "other-app's old secret")
	if n := len(p.mock.requests()); n != 0 {
		t.Fatalf("the upstream got %d requests from old secrets", n)
	}
	for _, r := range got.RotatedKeys {
		p.wantThing(r.Secret, "the new secret of key "+r.Key.ID)
	}
	for _, r := range p.mock.requests() {
		if r.Header.Get("Authorization") != "Bearer "+replaced {
			t.Errorf("the upstream was sent %q, want the replacement credential", r.Header.Get("Authorization"))
		}
	}
}
