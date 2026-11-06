package admin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"

	"github.com/kirank55/kman/internal/registry"
	"github.com/kirank55/kman/internal/store"
	"github.com/kirank55/kman/internal/vault"
)

// Credential is a credential as the admin API shows it. It never holds the
// secret: nothing does, after it is entered.
type Credential struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	UpstreamID string     `json:"upstream_id"`
	Upstream   string     `json:"upstream" doc:"The upstream's name."`
	Last4      string     `json:"last4,omitempty" doc:"The end of the secret, to tell credentials apart. Absent for a secret too short to show any of."`
	CreatedAt  time.Time  `json:"created_at"`
	RotatedAt  *time.Time `json:"rotated_at,omitempty" doc:"When the secret was last replaced."`
}

func credentialDTO(c store.Credential, upstream string) Credential {
	return Credential{ID: c.ID, Name: c.Name, UpstreamID: c.UpstreamID, Upstream: upstream, Last4: c.Last4,
		CreatedAt: c.CreatedAt, RotatedAt: timePtr(c.RotatedAt)}
}

// Secret is a credential's secret on its way in. It prints as a placeholder
// under every fmt verb, so a request that reaches a log line leaks nothing;
// only JSON, which is how it travels, sees its value.
type Secret string

const secretPlaceholder = "[secret]"

func (Secret) String() string   { return secretPlaceholder }
func (Secret) GoString() string { return secretPlaceholder }

// Format implements fmt.Formatter, which also covers %q and %x.
func (Secret) Format(f fmt.State, _ rune) { io.WriteString(f, secretPlaceholder) }

// maxSecret is the longest secret accepted, in bytes.
const maxSecret = 16 << 10

// minForLast4 is the shortest secret that shows its last four characters:
// any shorter, and they would give away too much of it.
const minForLast4 = 16

// AddCredentialRequest is a new credential on an upstream.
type AddCredentialRequest struct {
	Name     string `json:"name" doc:"1 to 63 of a-z, 0-9, '.', '_' and '-', starting with a letter or digit, unique among credentials not deleted."`
	Upstream string `json:"upstream" doc:"The upstream's name or id."`
	Secret   Secret `json:"secret" doc:"The real secret. kman seals it and never shows it again. For basic auth without a username, the whole user:password."`
}

// CredentialRef names a credential.
type CredentialRef struct {
	Ref string `json:"-" path:"ref" doc:"The credential's name or id."`
}

// CredentialUpdate renames a credential, replaces its secret, or both.
type CredentialUpdate struct {
	Ref    string  `json:"-" path:"ref" doc:"The credential's name or id."`
	Name   *string `json:"name,omitempty" doc:"A new name."`
	Secret *Secret `json:"secret,omitempty" doc:"A new secret, which overwrites the old one. The credential keeps its id, so virtual keys on it are not touched."`
}

// checkSecret reports what is wrong with a secret for an upstream with this
// auth. The message never quotes it.
func checkSecret(secret string, auth registry.Auth) error {
	switch {
	case secret == "":
		return errorf(CodeInvalid, "no secret given")
	case len(secret) > maxSecret:
		return errorf(CodeInvalid, "the secret is over %d bytes", maxSecret)
	case strings.ContainsFunc(secret, unicode.IsControl):
		return errorf(CodeInvalid, "the secret holds a control character, such as a line break")
	}
	if auth.Style == registry.AuthBasic && auth.BasicUsername == "" {
		if user, _, ok := strings.Cut(secret, ":"); !ok || user == "" {
			return errorf(CodeInvalid, "this upstream uses basic auth with no username, so the secret must be user:password")
		}
	}
	return nil
}

// last4 is what may be shown of a secret.
func last4(secret string) string {
	if len(secret) < minForLast4 {
		return ""
	}
	return secret[len(secret)-4:]
}

// sealCredential seals a secret under id, after checking it, wiping the copy
// it makes.
func (s *Service) sealCredential(id, secret string, auth registry.Auth) (vault.Sealed, error) {
	if err := checkSecret(secret, auth); err != nil {
		return vault.Sealed{}, err
	}
	if s.vault == nil {
		return vault.Sealed{}, errors.New("admin: no vault")
	}
	plain := []byte(secret)
	defer clear(plain)
	return s.vault.Seal(id, plain)
}

// AddCredential seals a secret and adds it as a credential on an upstream.
func (s *Service) AddCredential(ctx context.Context, req AddCredentialRequest) (Credential, error) {
	if err := checkName("credential", req.Name); err != nil {
		return Credential{}, err
	}
	id, err := store.NewID(s.rand, "cr_")
	if err != nil {
		return Credential{}, err
	}
	var out Credential
	fx := &effects{}
	err = s.commandWith(ctx, fx, func(tx *store.Tx, now time.Time) ([]audit, error) {
		u, err := upstreamByRef(tx, req.Upstream)
		if err != nil {
			return nil, err
		}
		pub, err := publishedUpstream(u)
		if err != nil {
			return nil, err
		}
		sealed, err := s.sealCredential(id, string(req.Secret), pub.Auth)
		if err != nil {
			return nil, err
		}
		c, err := tx.CreateCredential(store.Credential{ID: id, UpstreamID: u.ID, Name: req.Name, Ciphertext: sealed.Ciphertext,
			Nonce: sealed.Nonce, DataKeyID: sealed.DataKeyID, Last4: last4(string(req.Secret)), CreatedAt: now})
		if err != nil {
			return nil, fromStore(err, "credential "+req.Name)
		}
		fx.seal = append(fx.seal, sealedSecret{id, sealed})
		fx.change.Credentials = append(fx.change.Credentials, publishedCredential(c))
		out = credentialDTO(c, u.Name)
		return []audit{{action: "credential.create", kind: "credential", id: id, after: out}}, nil
	})
	return out, err
}

// UpdateCredential renames a credential, replaces its secret, or both. A new
// secret overwrites the ciphertext and wipes the old plaintext from memory.
func (s *Service) UpdateCredential(ctx context.Context, req CredentialUpdate) (Credential, error) {
	if req.Name == nil && req.Secret == nil {
		return Credential{}, errorf(CodeInvalid, "give a new name or a new secret")
	}
	if req.Name != nil {
		if err := checkName("credential", *req.Name); err != nil {
			return Credential{}, err
		}
	}
	var out Credential
	fx := &effects{}
	err := s.commandWith(ctx, fx, func(tx *store.Tx, now time.Time) ([]audit, error) {
		before, err := credentialByRef(tx, req.Ref)
		if err != nil {
			return nil, err
		}
		u, err := tx.Upstream(before.UpstreamID)
		if err != nil {
			return nil, err
		}
		row := before
		set(&row.Name, req.Name)
		if req.Secret != nil {
			pub, err := publishedUpstream(u)
			if err != nil {
				return nil, err
			}
			sealed, err := s.sealCredential(before.ID, string(*req.Secret), pub.Auth)
			if err != nil {
				return nil, err
			}
			row.Ciphertext, row.Nonce, row.DataKeyID = sealed.Ciphertext, sealed.Nonce, sealed.DataKeyID
			row.Last4, row.RotatedAt = last4(string(*req.Secret)), now
			fx.seal = append(fx.seal, sealedSecret{before.ID, sealed})
		}
		if req.Secret == nil && row.Name == before.Name {
			out = credentialDTO(before, u.Name)
			return nil, nil
		}
		c, err := tx.UpdateCredential(row)
		if err != nil {
			return nil, fromStore(err, "credential "+row.Name)
		}
		fx.change.Credentials = append(fx.change.Credentials, publishedCredential(c))
		out = credentialDTO(c, u.Name)
		return []audit{{action: "credential.update", kind: "credential", id: c.ID, before: credentialDTO(before, u.Name), after: out}}, nil
	})
	return out, err
}

// RemoveCredential deletes a credential and wipes its secret, unless a virtual
// key that is not revoked depends on it.
func (s *Service) RemoveCredential(ctx context.Context, req CredentialRef) (Credential, error) {
	var out Credential
	fx := &effects{}
	err := s.commandWith(ctx, fx, func(tx *store.Tx, now time.Time) ([]audit, error) {
		c, err := credentialByRef(tx, req.Ref)
		if err != nil {
			return nil, err
		}
		if err := refuseIfKeysDepend(tx, c.ID, "credential "+c.Name); err != nil {
			return nil, err
		}
		u, err := tx.Upstream(c.UpstreamID)
		if err != nil {
			return nil, err
		}
		if err := tx.DeleteCredential(c.ID, now); err != nil {
			return nil, fromStore(err, "credential "+req.Ref)
		}
		fx.change.DeletedCredentials = append(fx.change.DeletedCredentials, c.ID)
		fx.wipe = append(fx.wipe, c.ID)
		out = credentialDTO(c, u.Name)
		return []audit{{action: "credential.delete", kind: "credential", id: c.ID, before: out}}, nil
	})
	return out, err
}

// refuseIfKeysDepend is a conflict if a virtual key that is not revoked uses
// the credential, naming the first few so the operator can act on them.
func refuseIfKeysDepend(tx *store.Tx, credentialID, what string) error {
	const shown = 5
	keys, err := tx.KeysOnCredential(credentialID)
	if err != nil || len(keys) == 0 {
		return err
	}
	list := strings.Join(keys[:min(shown, len(keys))], ", ")
	if len(keys) > shown {
		list += ", ..."
	}
	return errorf(CodeConflict, "%s cannot be deleted: %d virtual keys depend on it (%s). Revoke them first", what, len(keys), list)
}

// credentialByRef finds a live credential by id or by name.
func credentialByRef(tx *store.Tx, ref string) (store.Credential, error) {
	c, err := tx.Credential(ref)
	if err == nil {
		return c, nil
	}
	if !isNotFound(err) {
		return store.Credential{}, err
	}
	c, err = tx.CredentialByName(ref)
	if err != nil {
		return store.Credential{}, fromStore(err, "credential "+ref)
	}
	return c, nil
}

func publishedCredential(c store.Credential) registry.Credential {
	return registry.Credential{ID: c.ID, UpstreamID: c.UpstreamID, Name: c.Name, Generation: c.Generation}
}

func sealedOf(c store.Credential) vault.Sealed {
	return vault.Sealed{DataKeyID: c.DataKeyID, Nonce: c.Nonce, Ciphertext: c.Ciphertext}
}
