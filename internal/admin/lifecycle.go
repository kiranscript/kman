package admin

import (
	"context"
	"time"

	"github.com/kiranscript/kman/internal/store"
)

// ContainKeyRequest is a command that stops a key: disable or revoke, which an
// operator may run for an incident.
type ContainKeyRequest struct {
	ID         string `json:"-" path:"id" doc:"The key id."`
	IncidentID int64  `json:"incident_id,omitempty" doc:"The incident this is done for. It must exist, be about this key and be unresolved. The audit entry carries its id, so the incident's timeline shows the action. Disabling leaves the incident open; revoking resolves it as confirmed."`
}

// DisableKey stops a key until it is enabled again. The next request with it
// is refused. Disabling a disabled key changes nothing; a revoked key is over,
// so it is a conflict.
func (s *Service) DisableKey(ctx context.Context, req ContainKeyRequest) (Key, error) {
	return s.moveKey(ctx, req, false, "key.disable", func(k *store.VirtualKey, now time.Time) (bool, error) {
		if err := refuseCanary(*k); err != nil {
			return false, err
		}
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
	return s.moveKey(ctx, ContainKeyRequest{ID: req.ID}, false, "key.enable", func(k *store.VirtualKey, _ time.Time) (bool, error) {
		if err := refuseCanary(*k); err != nil {
			return false, err
		}
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
func (s *Service) RevokeKey(ctx context.Context, req ContainKeyRequest) (Key, error) {
	return s.moveKey(ctx, req, true, "key.revoke", func(k *store.VirtualKey, now time.Time) (bool, error) {
		if k.Status == store.KeyRevoked {
			return false, nil
		}
		k.Status, k.RevokedAt = store.KeyRevoked, now
		return true, nil
	})
}

// moveKey runs a status change on a key in one command: store, audit and
// registry. move edits the row and says whether it changed anything; a command
// that changes nothing records and publishes nothing. With an incident, the
// incident is checked first, the audit entry carries its id, and when resolve
// is set the incident is resolved as confirmed in the same transaction, even if
// the key was already in the state asked for.
func (s *Service) moveKey(ctx context.Context, req ContainKeyRequest, resolve bool, action string, move func(k *store.VirtualKey, now time.Time) (bool, error)) (Key, error) {
	var out Key
	fx := &effects{}
	c, err := caller(ctx)
	if err != nil {
		return Key{}, err
	}
	err = s.commandWith(ctx, fx, func(tx *store.Tx, now time.Time) ([]audit, error) {
		before, err := tx.Key(req.ID)
		if err != nil {
			return nil, fromStore(err, "key "+req.ID)
		}
		var inc store.Incident
		if req.IncidentID != 0 {
			if inc, err = incidentForKey(tx, req.IncidentID, req.ID); err != nil {
				return nil, err
			}
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
		var entries []audit
		out = prior
		if changed {
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
			entries = append(entries, audit{action: action, kind: "key", id: k.ID, before: prior, after: out, incident: req.IncidentID})
		}
		if resolve && req.IncidentID != 0 {
			e, err := resolveIncident(tx, inc, store.ResolutionConfirmed, "incident.resolve", c.Actor, now)
			if err != nil {
				return nil, err
			}
			entries = append(entries, e)
		}
		return entries, nil
	})
	if err != nil {
		return Key{}, err
	}
	return out, nil
}
