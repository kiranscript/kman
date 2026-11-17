package alerts_test

import (
	"context"
	"net/netip"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/kirank55/kman/internal/alerts"
	"github.com/kirank55/kman/internal/events"
	"github.com/kirank55/kman/internal/store"
)

// warningLane runs a bus whose ordinary events the credential warnings listen to.
func (in *install) warningLane() (bus *events.Bus, stop func()) {
	bus = events.New(events.Options{})
	sub := bus.Subscribe("warnings", events.SubOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { bus.Run(ctx) })
	wg.Go(func() { in.svc.RunCredentialWarnings(sub) })
	stopRun := in.run()
	return bus, func() { cancel(); wg.Wait(); stopRun() }
}

// withUpstream makes an upstream and the credential kman presents to it.
func (in *install) withUpstream(name, credential string) (upstreamID string) {
	in.t.Helper()
	err := in.store.Do(context.Background(), func(tx *store.Tx) error {
		u, err := tx.CreateUpstream(store.Upstream{
			Name: name, Preset: "openai", PresetVersion: 1, Profile: "openai", Host: "api.openai.com", Port: 443, TLS: true,
			PathAllowlistJSON: `["/v1/"]`, PriceMultiplierPPM: 1_000_000, CreatedAt: t0, UpdatedAt: t0,
		})
		if err != nil {
			return err
		}
		upstreamID = u.ID
		_, err = tx.CreateCredential(store.Credential{ID: "cr_" + name, UpstreamID: u.ID, Name: credential,
			Ciphertext: []byte("sealed"), Nonce: []byte("n"), DataKeyID: "dek_1", CreatedAt: t0})
		return err
	})
	if err != nil {
		in.t.Fatal(err)
	}
	return upstreamID
}

func upstreamAnswered(upstreamID string, status int) events.Event {
	return events.Event{
		Time: time.Now(), Kind: events.KindRequest, KeyID: "abcd1234", ClientIP: netip.MustParseAddr("203.0.113.7"),
		UpstreamID: upstreamID, Method: "POST", Path: "/v1/chat/completions", Status: status,
	}
}

// A provider that answers 401 to the credential kman presents is a credential
// that was revoked or has expired at the provider: every key on it is failing.
// The warning names the upstream and the credential, and never the secret.
func TestAnUpstreamRefusingKmansCredentialIsAWarning(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t)
		in.channel("ch_1", "slack", "hooks.test", "medium", true, "")
		up := in.withUpstream("openai", "main")
		bus, stop := in.warningLane()
		defer stop()

		bus.Emit(upstreamAnswered(up, 200))
		bus.Emit(upstreamAnswered(up, 429))
		synctest.Wait()
		if got := in.bodies("hooks.test"); len(got) != 0 {
			t.Fatalf("an alert for a request that was fine: %v", got)
		}
		bus.Emit(upstreamAnswered(up, 401))
		synctest.Wait()
		got := in.bodies("hooks.test")
		if len(got) != 1 {
			t.Fatalf("%d alerts, want 1: %v", len(got), got)
		}
		mustContain(t, got[0], "HIGH", "openai", "main", "401", "kman credential update main")
	})
}

// A credential that is refused is refused on every request, so it alerts once an
// hour for each upstream, and says how many it held back.
func TestARefusedCredentialAlertsOncePerWindowAndCountsWhatItHeldBack(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t)
		in.channel("ch_1", "slack", "hooks.test", "medium", true, "")
		up := in.withUpstream("openai", "main")
		other := in.withUpstream("anthropic", "work")
		bus, stop := in.warningLane()
		defer stop()

		for range 5 {
			bus.Emit(upstreamAnswered(up, 401))
		}
		bus.Emit(upstreamAnswered(other, 401)) // another upstream has its own
		synctest.Wait()
		if got := in.bodies("hooks.test"); len(got) != 2 {
			t.Fatalf("%d alerts, want 2 (one per upstream): %v", len(got), got)
		}
		time.Sleep(alerts.DefaultCredentialWarningWindow + time.Minute)
		bus.Emit(upstreamAnswered(up, 401))
		synctest.Wait()
		got := in.bodies("hooks.test")
		if len(got) != 3 {
			t.Fatalf("%d alerts after the window, want 3: %v", len(got), got)
		}
		mustContain(t, got[2], "Held back", "4 more")
	})
}

// A channel that turned lifecycle warnings off does not hear of a credential.
func TestARefusedCredentialIsALifecycleWarningAChannelCanTurnOff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t)
		in.channel("ch_1", "slack", "hooks.test", "low", false, "")
		up := in.withUpstream("openai", "main")
		bus, stop := in.warningLane()
		defer stop()
		bus.Emit(upstreamAnswered(up, 401))
		synctest.Wait()
		if got := in.bodies("hooks.test"); len(got) != 0 {
			t.Fatalf("a channel with warnings off heard: %v", got)
		}
	})
}

// A refusal of kman's own (blocked), an event with no upstream, and an upstream
// kman no longer holds are not a provider refusing a credential kman presents.
func TestOnlyAnUpstreamsOwn401IsAWarning(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := newInstall(t)
		in.channel("ch_1", "slack", "hooks.test", "medium", true, "")
		up := in.withUpstream("openai", "main")
		bus, stop := in.warningLane()
		defer stop()
		blocked := upstreamAnswered(up, 401)
		blocked.Kind, blocked.BlockedCode = events.KindBlocked, "invalid_key"
		noUpstream := upstreamAnswered("", 401)
		unknown := upstreamAnswered("up_gone", 401)
		bus.Emit(blocked)
		bus.Emit(noUpstream)
		bus.Emit(unknown)
		synctest.Wait()
		if got := in.bodies("hooks.test"); len(got) != 0 {
			t.Fatalf("an alert that is not about a provider refusing: %v", got)
		}
	})
}
