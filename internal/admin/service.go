package admin

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/kirank55/kman/internal/store"
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

	ListPrices(ctx context.Context, req PriceQuery) (PriceList, error)
	PriceDataStatus(ctx context.Context) (PriceDataStatus, error)
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
	// Apply publishes a committed change to the registry. Nil does
	// nothing, which is all there is until the registry exists (#34).
	Apply func(Change)
	// Prices returns the price table this binary carries. The composition
	// root adapts it, since admin may not import pricing (SPEC §2).
	Prices func() PriceTable
}

// Service runs the admin commands.
type Service struct {
	store  *store.Store
	now    func() time.Time
	rand   io.Reader
	apply  func(Change)
	prices func() PriceTable
}

// New returns the admin service over opts.Store.
func New(opts Options) *Service {
	s := &Service{store: opts.Store, now: opts.Now, rand: opts.Rand, apply: opts.Apply, prices: opts.Prices}
	if s.rand == nil {
		s.rand = rand.Reader
	}
	if s.apply == nil {
		s.apply = func(Change) {}
	}
	return s
}

// Change is what one command changed, published to the registry after it
// commits.
type Change struct {
	Generation int64 // the command's transaction's generation
	Targets    []Target
}

// Target is one thing a command changed, as its audit entry names it.
type Target struct {
	Kind string // such as "admin_token"
	ID   string
}

// audit is one audit entry a command records. Before and After are
// marshalled to JSON and must never hold a secret.
type audit struct {
	action        string
	kind, id      string
	before, after any
}

// command runs one admin command, the one way anything changes: fn's
// writes and its audit entries, which carry the caller as their actor,
// commit in one store transaction; then the change is published to the
// registry. A command that fails, or records no entry, changes nothing and
// publishes nothing.
func (s *Service) command(ctx context.Context, fn func(tx *store.Tx, now time.Time) ([]audit, error)) error {
	c, err := caller(ctx)
	if err != nil {
		return err
	}
	now := s.now()
	var change Change
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
			change.Targets = append(change.Targets, Target{Kind: e.kind, ID: e.id})
		}
		change.Generation, err = tx.Generation()
		return err
	})
	if err != nil {
		return err
	}
	if len(change.Targets) > 0 {
		s.apply(change)
	}
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
