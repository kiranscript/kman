package admin

import (
	"context"
	"errors"
	"regexp"
	"time"

	"github.com/kirank55/kman/internal/store"
	"github.com/kirank55/kman/internal/token"
)

// AdminToken is a named admin token, without its secret.
type AdminToken struct {
	Name       string     `json:"name" doc:"The token's name, which is the actor of every change made with it."`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty" doc:"When the token last authenticated a request, to the minute. Absent if never."`
	RevokedAt  *time.Time `json:"revoked_at,omitempty" doc:"When the token was revoked. Absent while it is live."`
}

// CreatedAdminToken is a new or rotated admin token and its secret, which
// is shown only this once.
type CreatedAdminToken struct {
	Token  AdminToken `json:"token"`
	Secret string     `json:"secret" doc:"The token itself (kma_...). kman keeps only its hash, so it is shown only this once."`
}

// AdminTokenList is every admin token, live or revoked, by name.
type AdminTokenList struct {
	Tokens []AdminToken `json:"tokens"`
}

// CreateAdminTokenRequest names a new admin token.
type CreateAdminTokenRequest struct {
	Name string `json:"name" doc:"1 to 63 of a-z, 0-9, '.', '_' and '-', starting with a letter or digit. A revoked token's name stays taken."`
}

// AdminTokenName names an existing admin token.
type AdminTokenName struct {
	Name string `json:"-" path:"name" doc:"The admin token's name."`
}

// tokenName is the rule for admin token names. It leaves out ':', so no
// token can be named like a system actor such as system:detect.
var tokenName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

// touchEvery is how stale a token's last use may get before Authenticate
// records a new one, so most requests write nothing.
const touchEvery = time.Minute

// Authenticate returns the actor of a live admin token's secret. Anything
// else, including a virtual key, is unauthorized, and the error does not
// say which. It reads the store on every call, so a revoked or rotated
// token is refused from the next request on.
func (s *Service) Authenticate(ctx context.Context, secret string) (string, error) {
	tok, err := token.Parse(secret)
	if err != nil || tok.Kind != token.AdminToken {
		return "", errUnauthorized
	}
	now := s.now()
	var name string
	err = s.store.Do(ctx, func(tx *store.Tx) error {
		t, err := tx.AdminTokenByHash(tok.Hash[:])
		if err != nil {
			return err
		}
		if !t.RevokedAt.IsZero() || !tok.Hash.Equal(token.Hash(t.Hash)) {
			return errUnauthorized
		}
		name = t.Name
		if now.Sub(t.LastUsedAt) >= touchEvery {
			return tx.TouchAdminToken(t.Name, now)
		}
		return nil
	})
	if errors.Is(err, store.ErrNotFound) {
		return "", errUnauthorized
	}
	return name, err
}

// CreateAdminToken mints a new admin token.
func (s *Service) CreateAdminToken(ctx context.Context, req CreateAdminTokenRequest) (CreatedAdminToken, error) {
	if !tokenName.MatchString(req.Name) {
		return CreatedAdminToken{}, errorf(CodeInvalid, "admin token name %q: use 1 to 63 of a-z, 0-9, '.', '_' and '-', starting with a letter or digit", req.Name)
	}
	m, err := token.MintAdminTokenFrom(s.rand)
	if err != nil {
		return CreatedAdminToken{}, err
	}
	var out CreatedAdminToken
	err = s.command(ctx, func(tx *store.Tx, now time.Time) ([]audit, error) {
		t, err := tx.CreateAdminToken(store.AdminToken{Name: req.Name, Hash: m.Hash[:], CreatedAt: now})
		if err != nil {
			return nil, fromStore(err, "admin token "+req.Name)
		}
		out = CreatedAdminToken{Token: adminTokenDTO(t), Secret: m.Reveal()}
		return []audit{{action: "admin_token.create", kind: "admin_token", id: t.Name, after: out.Token}}, nil
	})
	return out, err
}

// ListAdminTokens returns every admin token.
func (s *Service) ListAdminTokens(ctx context.Context) (AdminTokenList, error) {
	out := AdminTokenList{Tokens: []AdminToken{}}
	err := s.read(ctx, func(tx *store.Tx) error {
		ts, err := tx.AdminTokens()
		for _, t := range ts {
			out.Tokens = append(out.Tokens, adminTokenDTO(t))
		}
		return err
	})
	return out, err
}

// RotateAdminToken gives a live admin token a new secret. The old one stops
// working at once; the name, and so the actor, stays.
func (s *Service) RotateAdminToken(ctx context.Context, req AdminTokenName) (CreatedAdminToken, error) {
	m, err := token.MintAdminTokenFrom(s.rand)
	if err != nil {
		return CreatedAdminToken{}, err
	}
	var out CreatedAdminToken
	err = s.command(ctx, func(tx *store.Tx, now time.Time) ([]audit, error) {
		t, err := tx.RotateAdminToken(req.Name, m.Hash[:])
		if err != nil {
			return nil, fromStore(err, "live admin token "+req.Name)
		}
		out = CreatedAdminToken{Token: adminTokenDTO(t), Secret: m.Reveal()}
		return []audit{{action: "admin_token.rotate", kind: "admin_token", id: t.Name}}, nil
	})
	return out, err
}

// RevokeAdminToken revokes a live admin token, refused from its next
// request on. The install's last live token cannot be revoked, since
// nothing could manage the install after it.
func (s *Service) RevokeAdminToken(ctx context.Context, req AdminTokenName) (AdminToken, error) {
	var out AdminToken
	err := s.command(ctx, func(tx *store.Tx, now time.Time) ([]audit, error) {
		ts, err := tx.AdminTokens()
		if err != nil {
			return nil, err
		}
		live, found := 0, false
		for _, t := range ts {
			if t.RevokedAt.IsZero() {
				live++
				found = found || t.Name == req.Name
			}
		}
		if found && live == 1 {
			return nil, errorf(CodeConflict, "%s is the last live admin token: create another before revoking it", req.Name)
		}
		t, err := tx.RevokeAdminToken(req.Name, now)
		if err != nil {
			return nil, fromStore(err, "live admin token "+req.Name)
		}
		out = adminTokenDTO(t)
		return []audit{{action: "admin_token.revoke", kind: "admin_token", id: t.Name}}, nil
	})
	return out, err
}

func adminTokenDTO(t store.AdminToken) AdminToken {
	return AdminToken{Name: t.Name, CreatedAt: t.CreatedAt, LastUsedAt: timePtr(t.LastUsedAt), RevokedAt: timePtr(t.RevokedAt)}
}

// timePtr is nil for the zero time.
func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
