package admin

import (
	"context"
	"time"

	"github.com/kirank55/kman/internal/store"
	"github.com/kirank55/kman/internal/token"
)

// Rotation's limits (SPEC §4). A retired secret's hash is kept for detection
// for retiredFor, and no grace outlasts it.
const (
	retiredFor = 30 * 24 * time.Hour
	maxGrace   = 30 * 24 * time.Hour
)

// settleEvery is the longest the settling loop sleeps, so a secret to be
// forgotten is forgotten within this long of its time.
const settleEvery = time.Hour

// RotateKeyRequest asks for a new secret for a key.
type RotateKeyRequest struct {
	ID           string `json:"-" path:"id" doc:"The key id."`
	GraceSeconds int64  `json:"grace_seconds,omitempty" doc:"How long the old secret keeps working beside the new one, at most 30 days. Absent or 0 ends it at once, on the next request."`
}

// RotatedKey is a key after rotation, with its new secret, which is shown only this once.
type RotatedKey struct {
	Key                Key        `json:"key" doc:"The same key, with the same id, spend, policy and history: only its secret is new."`
	Secret             Secret     `json:"secret" doc:"The new virtual key (kmn_...). kman keeps only its hash, so it is shown only this once."`
	PreviousValidUntil *time.Time `json:"previous_valid_until,omitempty" doc:"When the old secret stops working. Absent when it stopped at once."`
	SDKEnv             string     `json:"sdk_env,omitempty" doc:"The variable an SDK for its upstream reads the key from."`
}

// rotationAudit is what a rotation records of itself: the key, and how the
// old secret was treated. It holds no secret.
type rotationAudit struct {
	Key                Key        `json:"key"`
	GraceSeconds       int64      `json:"grace_seconds"`
	PreviousValidUntil *time.Time `json:"previous_valid_until,omitempty"`
}

func checkGrace(seconds int64) error {
	if seconds < 0 || time.Duration(seconds)*time.Second > maxGrace {
		return errorf(CodeInvalid, "grace_seconds must be from 0 to %d (30 days): got %d", int64(maxGrace/time.Second), seconds)
	}
	return nil
}

// RotateKey replaces a key's secret and keeps the key: its id, spend,
// baseline, policy and audit history are untouched (ADR 0002). The old secret
// works for the grace, if there is one, and the key never holds more than two
// working secrets, so a rotation inside an earlier grace ends the older secret
// at once. It is one transaction: a rotation that fails changes nothing. The
// new secret is returned once.
func (s *Service) RotateKey(ctx context.Context, req RotateKeyRequest) (RotatedKey, error) {
	if err := checkGrace(req.GraceSeconds); err != nil {
		return RotatedKey{}, err
	}
	var out RotatedKey
	fx := &effects{}
	err := s.commandWith(ctx, fx, func(tx *store.Tx, now time.Time) ([]audit, error) {
		entry, rotated, err := s.rotateKey(tx, fx, now, req.ID, seconds(req.GraceSeconds))
		out = rotated
		return []audit{entry}, err
	})
	if err != nil {
		return RotatedKey{}, err
	}
	s.wakeSettling()
	return out, nil
}

// rotateKey rotates one key as part of a command's transaction, writing its
// rows and effects, and returns its audit entry.
func (s *Service) rotateKey(tx *store.Tx, fx *effects, now time.Time, id string, grace time.Duration) (audit, RotatedKey, error) {
	before, err := tx.Key(id)
	if err != nil {
		return audit{}, RotatedKey{}, fromStore(err, "key "+id)
	}
	if before.Status == store.KeyRevoked {
		return audit{}, RotatedKey{}, errorf(CodeConflict, "key %s is revoked, so it cannot be rotated: create a new key", id)
	}
	names, err := liveKeyNames(tx)
	if err != nil {
		return audit{}, RotatedKey{}, err
	}
	prior, err := keyDTO(before, now, names)
	if err != nil {
		return audit{}, RotatedKey{}, err
	}
	minted, err := token.MintVirtualKeyFrom(token.KeyID(id), s.rand)
	if err != nil {
		return audit{}, RotatedKey{}, err
	}
	if err := tx.RotateKeySecret(id, minted.Hash[:], now, grace, retiredFor); err != nil {
		return audit{}, RotatedKey{}, fromStore(err, "key "+id)
	}
	k, err := tx.Key(id)
	if err != nil {
		return audit{}, RotatedKey{}, err
	}
	published, err := publishedKey(tx, k)
	if err != nil {
		return audit{}, RotatedKey{}, err
	}
	fx.change.Keys = append(fx.change.Keys, published)
	dto, err := keyDTO(k, now, names)
	if err != nil {
		return audit{}, RotatedKey{}, err
	}
	out := RotatedKey{Key: s.withSpend(dto), Secret: Secret(minted.Reveal())}
	if grace > 0 {
		until := now.Add(grace)
		out.PreviousValidUntil = &until
	}
	if u, err := tx.Upstream(names[k.CredentialID].upstreamID); err == nil {
		out.SDKEnv = u.SDKEnv
	}
	return audit{action: "key.rotate", kind: "key", id: id, before: prior,
		after: rotationAudit{Key: dto, GraceSeconds: int64(grace / time.Second), PreviousValidUntil: out.PreviousValidUntil}}, out, nil
}

// SettleSecrets moves what time has decided about secrets: a previous secret
// whose grace has ended becomes a retired one, and a retired secret kept 30
// days is forgotten. The request path needs none of this to stop a secret at
// the end of its grace; this is what keeps the store telling the truth. It
// returns when the next grace ends, or the zero time when none is running. It
// is housekeeping, not a command: it has no caller and records no audit entry.
func (s *Service) SettleSecrets(ctx context.Context) (time.Time, error) {
	var next time.Time
	err := s.store.Do(ctx, func(tx *store.Tx) (err error) {
		next, err = tx.SettleKeySecrets(s.now(), retiredFor)
		return err
	})
	return next, err
}

// RunSettling settles secrets at startup, when each grace ends and at least
// hourly, until ctx ends. A rotation wakes it, since it may have started a
// grace that ends sooner than any it was waiting for.
func (s *Service) RunSettling(ctx context.Context) {
	for {
		wait := settleEvery
		next, err := s.SettleSecrets(ctx)
		switch {
		case err != nil:
			wait = time.Minute
		case !next.IsZero():
			wait = min(wait, max(next.Sub(s.now()), time.Millisecond))
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-s.settle:
		case <-t.C:
		}
		t.Stop()
	}
}

// wakeSettling tells RunSettling something may have changed. It never blocks.
func (s *Service) wakeSettling() {
	select {
	case s.settle <- struct{}{}:
	default:
	}
}
