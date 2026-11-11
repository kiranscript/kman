package registry

import (
	"maps"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kirank55/kman/internal/policy"
	"github.com/kirank55/kman/internal/store"
	"github.com/kirank55/kman/internal/token"
)

// Upstream is one upstream as the request path reads it.
type Upstream struct {
	ID            string
	Name          string
	Preset        string // "" for a custom upstream
	PresetVersion int
	Spec
	PriceMultiplierPPM int64
	Generation         int64

	// Rules is Paths compiled, which Apply fills in. A caller leaves it out.
	Rules *policy.Paths
}

// Credential is one credential as the request path reads it. Its secret is
// not here: the vault holds it, under ID.
type Credential struct {
	ID         string
	UpstreamID string
	Name       string
	Generation int64
}

// A key's statuses are the store's, which owns them. They are here for the
// gateway, which may not import the store.
const (
	KeyActive   = store.KeyActive
	KeyDisabled = store.KeyDisabled
	KeyRevoked  = store.KeyRevoked
)

// Key is one virtual key as the request path reads it.
type Key struct {
	ID            string
	Name          string
	CredentialID  string // "" for a canary
	Status        string
	Secrets       []KeySecret // those that can authenticate: never a retired one
	ExpiresAt     time.Time
	Policy        string // the policy as JSON, in the form policy.Normalize gives
	PolicyVersion int
	Generation    int64

	// Compiled is Policy compiled, which Apply fills in: the request path reads
	// it and never parses the JSON. A caller leaves it out. A policy that can't be
	// read compiles to one that allows nothing.
	Compiled *policy.Compiled
}

// KeySecret is the hash of a secret that can authenticate a key.
type KeySecret struct {
	Hash       token.Hash
	ValidUntil time.Time // when a grace period ends; zero for no limit
}

// Route is what the request path needs to serve a key. Its pointers lead into
// an immutable snapshot, so a holder must not change what they point at.
// Credential and Upstream are nil for a key with no credential, a canary.
type Route struct {
	Key        *Key
	Credential *Credential
	Upstream   *Upstream
}

// Change is what one committed admin transaction changed. A row in the change
// replaces the row with its id; deleted ids are removed. A change holding only
// a generation is valid: a command may change nothing the request path reads.
type Change struct {
	Generation int64

	Upstreams   []Upstream
	Credentials []Credential
	Keys        []Key

	DeletedUpstreams   []string
	DeletedCredentials []string
	DeletedKeys        []string
}

// Snapshot is one immutable state of the registry.
type Snapshot struct {
	generation  int64
	upstreams   map[string]*Upstream
	credentials map[string]*Credential
	keys        map[string]*Key
}

// Generation is the generation of the last change in this snapshot.
func (s *Snapshot) Generation() int64 { return s.generation }

// Upstream returns the upstream with this id.
func (s *Snapshot) Upstream(id string) (*Upstream, bool) {
	u, ok := s.upstreams[id]
	return u, ok
}

// Credential returns the credential with this id.
func (s *Snapshot) Credential(id string) (*Credential, bool) {
	c, ok := s.credentials[id]
	return c, ok
}

// Lookup returns the route of the key with this key id. It takes no locks and
// allocates nothing.
func (s *Snapshot) Lookup(keyID string) (Route, bool) {
	k, ok := s.keys[keyID]
	if !ok {
		return Route{}, false
	}
	route := Route{Key: k}
	if c, ok := s.credentials[k.CredentialID]; ok {
		route.Credential = c
		route.Upstream = s.upstreams[c.UpstreamID]
	}
	return route, true
}

// Registry holds the current snapshot, which it swaps atomically.
type Registry struct {
	mu   sync.Mutex // serializes Apply; readers never take it
	snap atomic.Pointer[Snapshot]
}

// New returns an empty registry at generation 0.
func New() *Registry {
	r := &Registry{}
	r.snap.Store(&Snapshot{
		upstreams:   map[string]*Upstream{},
		credentials: map[string]*Credential{},
		keys:        map[string]*Key{},
	})
	return r
}

// Snapshot returns the current snapshot, for a reader that needs several
// lookups to agree.
func (r *Registry) Snapshot() *Snapshot { return r.snap.Load() }

// Generation returns the generation of the current snapshot.
func (r *Registry) Generation() int64 { return r.snap.Load().generation }

// Lookup returns the route of the key with this key id, from the current
// snapshot. It takes no locks and allocates nothing.
func (r *Registry) Lookup(keyID string) (Route, bool) { return r.snap.Load().Lookup(keyID) }

// Upstream returns the upstream with this id, from the current snapshot.
func (r *Registry) Upstream(id string) (*Upstream, bool) { return r.snap.Load().Upstream(id) }

// Credential returns the credential with this id, from the current snapshot.
func (r *Registry) Credential(id string) (*Credential, bool) { return r.snap.Load().Credential(id) }

// Apply publishes a change as a new snapshot, which readers see whole or not
// at all. Copying the maps is the cost of a change, which an admin command
// pays and a request never does. A change no newer than the current snapshot
// is ignored, so a late publish can never undo a later one.
func (r *Registry) Apply(c Change) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cur := r.snap.Load()
	if c.Generation <= cur.generation {
		return
	}
	next := &Snapshot{
		generation: c.Generation,
		upstreams: merged(cur.upstreams, c.Upstreams, func(u *Upstream) string { return u.ID }, c.DeletedUpstreams, func(u *Upstream) {
			u.Rules, _ = policy.CompilePaths(u.Paths) // an unreadable list is nil, which allows nothing
		}),
		credentials: merged(cur.credentials, c.Credentials, func(x *Credential) string { return x.ID }, c.DeletedCredentials, nil),
		keys: merged(cur.keys, c.Keys, func(k *Key) string { return k.ID }, c.DeletedKeys, func(k *Key) {
			k.Compiled, _ = policy.Compile(k.PolicyVersion, k.Policy)
		}),
	}
	r.snap.Store(next)
}

// merged returns cur with rows set and deleted removed. It returns cur itself
// when there is nothing to do, which is safe because no snapshot's maps ever
// change once it is published.
//
// prepare, if not nil, finishes each new row's copy before it is shared.
func merged[T any](cur map[string]*T, rows []T, id func(*T) string, deleted []string, prepare func(*T)) map[string]*T {
	if len(rows) == 0 && len(deleted) == 0 {
		return cur
	}
	next := maps.Clone(cur)
	for i := range rows {
		row := rows[i] // a copy, so the snapshot shares nothing with the caller
		if prepare != nil {
			prepare(&row)
		}
		next[id(&row)] = &row
	}
	for _, d := range deleted {
		delete(next, d)
	}
	return next
}
