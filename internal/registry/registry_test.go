package registry_test

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/kirank55/kman/internal/policy"
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

func TestApplyCompilesAKeysPolicyAndAnUpstreamsPathsOnceForTheRequestPath(t *testing.T) {
	r := registry.New()
	up := upstream("up_1", 1)
	up.Paths = []string{"POST /v1/chat/completions"}
	r.Apply(registry.Change{
		Generation:  1,
		Upstreams:   []registry.Upstream{up},
		Credentials: []registry.Credential{{ID: "cr_1", UpstreamID: "up_1", Generation: 1}},
		Keys: []registry.Key{
			{ID: "good0001", CredentialID: "cr_1", Policy: `{"allowed_models":["gpt-4o"],"rpm_limit":7}`, PolicyVersion: 1, Generation: 1},
			{ID: "bad00001", CredentialID: "cr_1", Policy: `{"nope":1}`, PolicyVersion: 1, Generation: 1},
		},
	})
	good, _ := r.Lookup("good0001")
	facts := policy.Facts{Method: "POST", Path: "/v1/chat/completions", Model: "gpt-4o", HasModel: true, Upstream: good.Upstream.Rules}
	if got := policy.Evaluate(facts, good.Key.Compiled); got != policy.Allow {
		t.Errorf("Evaluate = %v, want allow", got)
	}
	if good.Key.Compiled.RPM() != 7 {
		t.Errorf("RPM = %d", good.Key.Compiled.RPM())
	}
	bad, _ := r.Lookup("bad00001")
	facts.Upstream = bad.Upstream.Rules
	if got := policy.Evaluate(facts, bad.Key.Compiled); got == policy.Allow || bad.Key.Compiled.Err() == nil {
		t.Errorf("a policy that can't be read must allow nothing and say why: %v, %v", got, bad.Key.Compiled.Err())
	}
}

func TestLookupStillAllocatesNothingWithCompiledPolicies(t *testing.T) {
	r := registry.New()
	r.Apply(registry.Change{Generation: 1, Keys: []registry.Key{{ID: "abcd1234", Policy: `{}`, PolicyVersion: 1, Generation: 1}}})
	if allocs := testing.AllocsPerRun(100, func() { r.Lookup("abcd1234") }); allocs != 0 {
		t.Errorf("Lookup allocated %v times", allocs)
	}
}
