package admin

import (
	"context"
	"time"

	"github.com/kirank55/kman/internal/store"
)

// DisableKey stops a key until it is enabled again. The next request with it
// is refused. Disabling a disabled key changes nothing; a revoked key is over,
// so it is a conflict.
func (s *Service) DisableKey(ctx context.Context, req KeyRef) (Key, error) {
	return s.moveKey(ctx, req, "key.disable", func(k *store.VirtualKey, now time.Time) (bool, error) {
		switch k.Status {
		case store.KeyRevoked:
			return false, errorf(CodeConflict, "key %s is revoked, so it cannot be disabled", k.ID)
		case store.KeyDisabled:
			return false, nil
		}
		k.Status, k.DisabledAt = store.KeyDisabled, now
		return true, nil
	})
}

// EnableKey lets a disabled key work again. Enabling an active key changes
// nothing, and a revoked key can never be enabled. A key past its expiry stays
// expired: enabling does not extend it.
func (s *Service) EnableKey(ctx context.Context, req KeyRef) (Key, error) {
	return s.moveKey(ctx, req, "key.enable", func(k *store.VirtualKey, _ time.Time) (bool, error) {
		switch k.Status {
		case store.KeyRevoked:
			return false, errorf(CodeConflict, "key %s is revoked, and a revoked key cannot be enabled: create a new key", k.ID)
		case store.KeyActive:
			return false, nil
		}
		k.Status, k.DisabledAt = store.KeyActive, time.Time{}
		return true, nil
	})
}

// RevokeKey ends a key for good. It stays in the registry so the gateway can
// tell its holder it is revoked, and its name is free again. Revoking a
// revoked key changes nothing.
func (s *Service) RevokeKey(ctx context.Context, req KeyRef) (Key, error) {
	return s.moveKey(ctx, req, "key.revoke", func(k *store.VirtualKey, now time.Time) (bool, error) {
		if k.Status == store.KeyRevoked {
			return false, nil
		}
		k.Status, k.RevokedAt = store.KeyRevoked, now
		return true, nil
	})
}

// moveKey runs a status change on a key in one command: store, audit and
// registry. move edits the row and says whether it changed anything; a command
// that changes nothing records and publishes nothing.
func (s *Service) moveKey(ctx context.Context, req KeyRef, action string, move func(k *store.VirtualKey, now time.Time) (bool, error)) (Key, error) {
	var out Key
	fx := &effects{}
	err := s.commandWith(ctx, fx, func(tx *store.Tx, now time.Time) ([]audit, error) {
		before, err := tx.Key(req.ID)
		if err != nil {
			return nil, fromStore(err, "key "+req.ID)
		}
		row := before
		changed, err := move(&row, now)
		if err != nil {
			return nil, err
		}
		names, err := liveKeyNames(tx)
		if err != nil {
			return nil, err
		}
		prior, err := keyDTO(before, now, names)
		if err != nil {
			return nil, err
		}
		if !changed {
			out = prior
			return nil, nil
		}
		k, err := tx.UpdateKey(row)
		if err != nil {
			return nil, fromStore(err, "key "+req.ID)
		}
		published, err := publishedKey(tx, k)
		if err != nil {
			return nil, err
		}
		fx.change.Keys = append(fx.change.Keys, published)
		if out, err = keyDTO(k, now, names); err != nil {
			return nil, err
		}
		return []audit{{action: action, kind: "key", id: k.ID, before: prior, after: out}}, nil
	})
	if err != nil {
		return Key{}, err
	}
	return out, nil
}
