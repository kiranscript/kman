package registry_test

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/kirank55/kman/internal/registry"
)

func upstream(id string, gen int64) registry.Upstream {
	return registry.Upstream{ID: id, Name: "name-" + id, Generation: gen}
}

func TestANewRegistryIsEmptyAtGenerationZero(t *testing.T) {
	r := registry.New()
	if g := r.Generation(); g != 0 {
		t.Errorf("Generation = %d, want 0", g)
	}
	if _, ok := r.Lookup("abcd1234"); ok {
		t.Error("an empty registry found a key")
	}
	if _, ok := r.Upstream("up_1"); ok {
		t.Error("an empty registry found an upstream")
	}
}

func TestApplyPublishesAChangeAndItsGeneration(t *testing.T) {
	r := registry.New()
	r.Apply(registry.Change{
		Generation:  7,
		Upstreams:   []registry.Upstream{upstream("up_1", 7)},
		Credentials: []registry.Credential{{ID: "cr_1", UpstreamID: "up_1", Name: "main", Generation: 7}},
		Keys:        []registry.Key{{ID: "abcd1234", Name: "app", CredentialID: "cr_1", Status: "active", Generation: 7}},
	})
	if g := r.Generation(); g != 7 {
		t.Errorf("Generation = %d, want 7", g)
	}
	route, ok := r.Lookup("abcd1234")
	if !ok {
		t.Fatal("the key is not there")
	}
	if route.Key.Name != "app" || route.Credential.Name != "main" || route.Upstream.Name != "name-up_1" {
		t.Errorf("Lookup = key %q, credential %q, upstream %q", route.Key.Name, route.Credential.Name, route.Upstream.Name)
	}
}

func TestApplyLeavesAnEarlierSnapshotUnchanged(t *testing.T) {
	r := registry.New()
	r.Apply(registry.Change{Generation: 1, Upstreams: []registry.Upstream{upstream("up_1", 1)}})
	before := r.Snapshot()

	changed := upstream("up_1", 2)
	changed.Host = "changed.example.com"
	r.Apply(registry.Change{Generation: 2, Upstreams: []registry.Upstream{changed, upstream("up_2", 2)}})

	if u, _ := before.Upstream("up_1"); u.Host != "" {
		t.Errorf("the earlier snapshot sees host %q", u.Host)
	}
	if _, ok := before.Upstream("up_2"); ok {
		t.Error("the earlier snapshot sees an upstream added later")
	}
	if before.Generation() != 1 {
		t.Errorf("the earlier snapshot is at generation %d, want 1", before.Generation())
	}
	if u, _ := r.Upstream("up_1"); u.Host != "changed.example.com" {
		t.Errorf("the current snapshot sees host %q", u.Host)
	}
}

func TestApplyRemovesWhatTheChangeDeleted(t *testing.T) {
	r := registry.New()
	r.Apply(registry.Change{
		Generation:  1,
		Upstreams:   []registry.Upstream{upstream("up_1", 1)},
		Credentials: []registry.Credential{{ID: "cr_1", UpstreamID: "up_1", Generation: 1}},
		Keys:        []registry.Key{{ID: "abcd1234", CredentialID: "cr_1", Generation: 1}},
	})
	r.Apply(registry.Change{
		Generation:         2,
		DeletedUpstreams:   []string{"up_1"},
		DeletedCredentials: []string{"cr_1"},
		DeletedKeys:        []string{"abcd1234"},
	})
	if _, ok := r.Upstream("up_1"); ok {
		t.Error("the deleted upstream is still there")
	}
	if _, ok := r.Credential("cr_1"); ok {
		t.Error("the deleted credential is still there")
	}
	if _, ok := r.Lookup("abcd1234"); ok {
		t.Error("the deleted key is still there")
	}
}

// A change with nothing in it still moves the generation: every admin command
// publishes, including those that change nothing the request path reads.
func TestAChangeWithOnlyAGenerationMovesTheGeneration(t *testing.T) {
	r := registry.New()
	r.Apply(registry.Change{Generation: 1, Upstreams: []registry.Upstream{upstream("up_1", 1)}})
	r.Apply(registry.Change{Generation: 2})
	if g := r.Generation(); g != 2 {
		t.Errorf("Generation = %d, want 2", g)
	}
	if _, ok := r.Upstream("up_1"); !ok {
		t.Error("the upstream went missing")
	}
}

func TestAStaleChangeIsIgnored(t *testing.T) {
	r := registry.New()
	current := upstream("up_1", 5)
	current.Host = "current.example.com"
	r.Apply(registry.Change{Generation: 5, Upstreams: []registry.Upstream{current}})

	stale := upstream("up_1", 4)
	stale.Host = "stale.example.com"
	r.Apply(registry.Change{Generation: 4, Upstreams: []registry.Upstream{stale}})

	if g := r.Generation(); g != 5 {
		t.Errorf("Generation = %d, want 5", g)
	}
	if u, _ := r.Upstream("up_1"); u.Host != "current.example.com" {
		t.Errorf("host = %q: a stale change overwrote the snapshot", u.Host)
	}
}

// A canary has no credential, so its route has no upstream.
func TestAKeyWithoutACredentialHasARouteWithoutAnUpstream(t *testing.T) {
	r := registry.New()
	r.Apply(registry.Change{Generation: 1, Keys: []registry.Key{{ID: "canary12", Generation: 1}}})
	route, ok := r.Lookup("canary12")
	if !ok || route.Key.ID != "canary12" {
		t.Fatalf("Lookup = %+v, %v", route, ok)
	}
	if route.Credential != nil || route.Upstream != nil {
		t.Errorf("route = %+v, want no credential or upstream", route)
	}
}

func TestLookupAllocatesNothing(t *testing.T) {
	r := seeded()
	if n := testing.AllocsPerRun(100, func() { r.Lookup("abcd1234") }); n != 0 {
		t.Errorf("Lookup allocates %v times per run", n)
	}
}

// TestLookupsSeeOnlyWholeSnapshots checks, under -race in CI, that readers
// running through a stream of changes see each one whole: a change moves a key
// and its credential together, so the two always agree.
func TestLookupsSeeOnlyWholeSnapshots(t *testing.T) {
	r := registry.New()
	publish := func(gen int64) {
		cred := "cr_" + string(rune('a'+gen%2))
		r.Apply(registry.Change{
			Generation:  gen,
			Upstreams:   []registry.Upstream{upstream("up_1", gen)},
			Credentials: []registry.Credential{{ID: cred, UpstreamID: "up_1", Generation: gen}},
			Keys:        []registry.Key{{ID: "abcd1234", CredentialID: cred, Generation: gen}},
		})
	}
	publish(1)
	var stop atomic.Bool
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for !stop.Load() {
				route, ok := r.Lookup("abcd1234")
				if !ok || route.Credential == nil || route.Upstream == nil {
					t.Errorf("Lookup = %+v, %v", route, ok)
					return
				}
				if route.Credential.ID != route.Key.CredentialID {
					t.Errorf("key names credential %s, the route holds %s", route.Key.CredentialID, route.Credential.ID)
					return
				}
			}
		})
	}
	for gen := int64(2); gen < 500; gen++ {
		publish(gen)
	}
	stop.Store(true)
	wg.Wait()
	if g := r.Generation(); g != 499 {
		t.Errorf("Generation = %d, want 499", g)
	}
}

func BenchmarkLookup(b *testing.B) {
	r := seeded()
	b.ReportAllocs()
	for b.Loop() {
		if _, ok := r.Lookup("abcd1234"); !ok {
			b.Fatal("no key")
		}
	}
}

func seeded() *registry.Registry {
	r := registry.New()
	r.Apply(registry.Change{
		Generation:  1,
		Upstreams:   []registry.Upstream{upstream("up_1", 1)},
		Credentials: []registry.Credential{{ID: "cr_1", UpstreamID: "up_1", Generation: 1}},
		Keys:        []registry.Key{{ID: "abcd1234", CredentialID: "cr_1", Generation: 1}},
	})
	return r
}

// The request path reads a key's policy as fields, not as JSON: Apply reads it
// once, so that a request parses nothing.
func TestAPublishedKeyCarriesItsPolicyAsFields(t *testing.T) {
	r := registry.New()
	r.Apply(registry.Change{Generation: 1, Keys: []registry.Key{
		{ID: "capped000", Policy: `{"daily_cap_micro":5000000}`},
		{ID: "plain0000", Policy: `{}`},
		{ID: "legacy000"},
	}})
	for id, want := range map[string]int64{"capped000": 5_000_000, "plain0000": 0, "legacy000": 0} {
		route, _ := r.Lookup(id)
		if got := route.Key.Rules.DailyCapMicro; got != want || route.Key.PolicyErr != nil {
			t.Errorf("%s: cap %d, err %v; want %d", id, got, route.Key.PolicyErr, want)
		}
	}
}

// A policy kman cannot read is no policy at all, which would let a key go free:
// the key says so, and the gateway refuses it.
func TestAPolicyKmanCannotReadIsMarkedSoItCanBeRefused(t *testing.T) {
	r := registry.New()
	r.Apply(registry.Change{Generation: 1, Keys: []registry.Key{{ID: "future000", Policy: `{"cap_from_the_future":1}`}}})
	route, _ := r.Lookup("future000")
	if route.Key.PolicyErr == nil {
		t.Error("an unreadable policy left no error on the key")
	}
}
