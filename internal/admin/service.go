package admin

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/kirank55/kman/internal/registry"
	"github.com/kirank55/kman/internal/store"
	"github.com/kirank55/kman/internal/vault"
)

// API is every command of the admin API. *Service implements it in process;
// the CLI's client implements it over /admin/v1. Each method reads its
// caller from ctx.
type API interface {
	Whoami(ctx context.Context) (Whoami, error)

	CreateAdminToken(ctx context.Context, req CreateAdminTokenRequest) (CreatedAdminToken, error)
	ListAdminTokens(ctx context.Context) (AdminTokenList, error)
	RotateAdminToken(ctx context.Context, req AdminTokenName) (CreatedAdminToken, error)
	RevokeAdminToken(ctx context.Context, req AdminTokenName) (AdminToken, error)

	Settings(ctx context.Context) (Settings, error)
	UpdateSettings(ctx context.Context, req SettingsUpdate) (Settings, error)

	Audit(ctx context.Context, req AuditQuery) (AuditPage, error)

	AddUpstream(ctx context.Context, req AddUpstreamRequest) (Upstream, error)
	ListUpstreams(ctx context.Context) (UpstreamList, error)
	GetUpstream(ctx context.Context, req UpstreamRef) (Upstream, error)
	UpdateUpstream(ctx context.Context, req UpstreamUpdate) (Upstream, error)
	RemoveUpstream(ctx context.Context, req UpstreamRef) (Upstream, error)
	PlanUpstreamSync(ctx context.Context, req UpstreamRef) (UpstreamSyncPlan, error)
	SyncUpstream(ctx context.Context, req UpstreamSyncRequest) (Upstream, error)

	AddCredential(ctx context.Context, req AddCredentialRequest) (Credential, error)
	UpdateCredential(ctx context.Context, req CredentialUpdate) (Credential, error)
	RemoveCredential(ctx context.Context, req CredentialRef) (Credential, error)

	CreateKey(ctx context.Context, req CreateKeyRequest) (CreatedKey, error)
	ListKeys(ctx context.Context, req KeyQuery) (KeyList, error)
	GetKey(ctx context.Context, req KeyRef) (Key, error)
	UpdateKey(ctx context.Context, req KeyUpdate) (Key, error)
	DisableKey(ctx context.Context, req KeyRef) (Key, error)
	EnableKey(ctx context.Context, req KeyRef) (Key, error)
	RevokeKey(ctx context.Context, req KeyRef) (Key, error)

	ListPrices(ctx context.Context, req PriceQuery) (PriceList, error)
	PriceDataStatus(ctx context.Context) (PriceDataStatus, error)
	SetPriceOverride(ctx context.Context, req PriceOverrideSet) (PriceOverride, error)
	RemovePriceOverride(ctx context.Context, req PriceOverrideRef) (PriceOverride, error)
	ImportPriceOverrides(ctx context.Context, req PriceOverrideImport) (PriceOverrideImportResult, error)
}

var _ API = (*Service)(nil)

// Options configures New.
type Options struct {
	Store *store.Store
	// Now is the clock: admin never reads the wall clock itself.
	Now func() time.Time
	// Rand is where new secrets come from. It defaults to crypto/rand; the
	// fixture generator passes a seeded one.
	Rand io.Reader
	// Vault seals credentials and holds their plaintext. Only init, which
	// makes no credential, runs without one.
	Vault *vault.Vault
	// Presets are the templates AddUpstream copies, from profiles, which
	// admin may not import (ADR 0006).
	Presets []registry.Preset
	// Apply publishes a committed change to the registry: pass
	// (*registry.Registry).Apply. Nil does nothing, for init, which serves
	// nothing, and tools that run no request path.
	Apply func(registry.Change)
	// Prices returns the price table this binary carries. The composition
	// root adapts it, since admin may not import pricing (SPEC §2).
	Prices func() PriceTable
	// NormalizePolicy is policy's rules for a key's policy: pass
	// policy.Normalize, which admin may not import (SPEC §2). It returns the
	// policy's version and the form to store. Key commands refuse to run
	// without it.
	NormalizePolicy func(raw []byte) (version int, normalized []byte, err error)
	// ApplyPrices publishes every price override to the request path, whole,
	// after a command that changes one commits and at startup. The
	// composition root adapts it, since admin may not import pricing. Nil
	// does nothing, for what runs no request path.
	ApplyPrices func([]PriceOverride) error
	// Spend reports what a key has spent today and this month, beside its caps,
	// from its policy as stored. The composition root adapts the meter, which
	// admin may not import. Nil shows no spend, for what runs no request path.
	Spend func(keyID, policy string) KeySpend
}

// Service runs the admin commands.
type Service struct {
	store   *store.Store
	now     func() time.Time
	rand    io.Reader
	vault   *vault.Vault
	presets []registry.Preset
	apply   func(registry.Change)
	prices  func() PriceTable
	policy  func(raw []byte) (int, []byte, error)
	publish func([]PriceOverride) error
	spend   func(keyID, policy string) KeySpend

	// publishing holds a command from its transaction to its publish, so
	// memory sees commands in the order they committed. The request path
	// never takes it.
	publishing sync.Mutex
}

// New returns the admin service over opts.Store.
func New(opts Options) *Service {
	s := &Service{store: opts.Store, now: opts.Now, rand: opts.Rand, vault: opts.Vault, presets: opts.Presets, apply: opts.Apply, prices: opts.Prices, policy: opts.NormalizePolicy, publish: opts.ApplyPrices, spend: opts.Spend}
	if s.rand == nil {
		s.rand = rand.Reader
	}
	if s.apply == nil {
		s.apply = func(registry.Change) {}
	}
	if s.publish == nil {
		s.publish = func([]PriceOverride) error { return nil }
	}
	return s
}

// audit is one audit entry a command records. Before and After are
// marshalled to JSON and must never hold a secret.
type audit struct {
	action        string
	kind, id      string
	before, after any
}

// effects is what a command does to memory once it has committed. A command
// that has any fills one in during its transaction and passes it to
// commandWith, since nothing in memory may change before the commit.
type effects struct {
	change registry.Change // the rows the request path reads
	seal   []sealedSecret  // cached in the vault before publishing
	wipe   []string        // wiped from the vault after publishing
	// overrides is every price override after the command, when it changed
	// any. Nil means it changed none.
	overrides []PriceOverride
}

// sealedSecret is a sealed credential to cache.
type sealedSecret struct {
	id     string
	sealed vault.Sealed
}

// command runs one admin command, the one way anything changes: fn's
// writes and its audit entries, which carry the caller as their actor,
// commit in one store transaction; then the change is published to the
// registry. A command that fails, or records no entry, changes nothing and
// publishes nothing.
func (s *Service) command(ctx context.Context, fn func(tx *store.Tx, now time.Time) ([]audit, error)) error {
	return s.commandWith(ctx, &effects{}, fn)
}

// commandWith is command for a command with effects. A secret reaches the
// vault before the rows that name it are published, and leaves it only after
// they are gone, so the request path never finds a credential with no secret.
// fn must not run another command: commands publish one at a time.
func (s *Service) commandWith(ctx context.Context, fx *effects, fn func(tx *store.Tx, now time.Time) ([]audit, error)) error {
	c, err := caller(ctx)
	if err != nil {
		return err
	}
	s.publishing.Lock()
	defer s.publishing.Unlock()
	now := s.now()
	published := false
	err = s.store.Do(ctx, func(tx *store.Tx) error {
		entries, err := fn(tx, now)
		if err != nil || len(entries) == 0 {
			return err
		}
		for _, e := range entries {
			before, err := marshalOrEmpty(e.before)
			if err != nil {
				return err
			}
			after, err := marshalOrEmpty(e.after)
			if err != nil {
				return err
			}
			if _, err := tx.AppendAudit(store.AuditEntry{TS: now, Actor: c.Actor, Action: e.action,
				TargetKind: e.kind, TargetID: e.id, BeforeJSON: before, AfterJSON: after, ClientIP: c.ClientIP}); err != nil {
				return err
			}
		}
		published = true
		fx.change.Generation, err = tx.Generation()
		return err
	})
	if err != nil || !published {
		return err
	}
	// The command has committed. A vault that refuses now, which only a
	// closed vault does while kman shuts down, cannot undo it, so the rest
	// still runs and the caller hears about it.
	var putErr error
	for _, p := range fx.seal {
		putErr = errors.Join(putErr, s.vault.Put(p.id, p.sealed))
	}
	if fx.overrides != nil {
		putErr = errors.Join(putErr, s.publish(fx.overrides))
	}
	s.apply(fx.change)
	for _, id := range fx.wipe {
		s.vault.Delete(id)
	}
	return putErr
}

// Load rebuilds what is held in memory from the store, at startup: each
// credential's plaintext in the vault, the price overrides, then the registry's
// snapshot, with every key, since the request path answers a revoked key by
// what it was. It takes
// no caller and changes nothing durable. A credential that does not decrypt
// fails it, naming the credential: kman will not serve with one it cannot use.
func (s *Service) Load(ctx context.Context) error {
	s.publishing.Lock()
	defer s.publishing.Unlock()
	var change registry.Change
	var sealed []sealedSecret
	var overrides []PriceOverride
	err := s.store.Do(ctx, func(tx *store.Tx) error {
		meta, err := tx.Meta()
		if err != nil {
			return err
		}
		change.Generation = meta.Generation
		upstreams, err := tx.Upstreams()
		if err != nil {
			return err
		}
		for _, u := range upstreams {
			p, err := publishedUpstream(u)
			if err != nil {
				return err
			}
			change.Upstreams = append(change.Upstreams, p)
		}
		creds, err := tx.Credentials()
		if err != nil {
			return err
		}
		for _, c := range creds {
			change.Credentials = append(change.Credentials, publishedCredential(c))
			sealed = append(sealed, sealedSecret{c.ID, sealedOf(c)})
		}
		keys, err := tx.Keys()
		if err != nil {
			return err
		}
		for _, k := range keys {
			published, err := publishedKey(tx, k)
			if err != nil {
				return err
			}
			change.Keys = append(change.Keys, published)
		}
		overrides, err = allOverrides(tx)
		return err
	})
	if err != nil {
		return err
	}
	for _, p := range sealed {
		if err := s.vault.Put(p.id, p.sealed); err != nil {
			return fmt.Errorf("load credential %s: %w", p.id, err)
		}
	}
	if err := s.publish(overrides); err != nil {
		return fmt.Errorf("load price overrides: %w", err)
	}
	s.apply(change)
	return nil
}

// read runs fn in a transaction for a caller, changing nothing.
func (s *Service) read(ctx context.Context, fn func(tx *store.Tx) error) error {
	if _, err := caller(ctx); err != nil {
		return err
	}
	return s.store.Do(ctx, fn)
}

func marshalOrEmpty(v any) (string, error) {
	if v == nil {
		return "", nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", errors.Join(errors.New("admin: marshal audit entry"), err)
	}
	return string(b), nil
}
