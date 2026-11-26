package admin

import (
	"context"
	"time"

	"github.com/kiranscript/kman/internal/store"
)

// CompromiseCredentialRequest replaces a leaked credential.
type CompromiseCredentialRequest struct {
	Ref        string `json:"-" path:"ref" doc:"The credential's name or id."`
	Secret     Secret `json:"secret" doc:"The new real secret, made at the provider after the leaked one was revoked there. kman seals it and never shows it again."`
	IncidentID int64  `json:"incident_id,omitempty" doc:"The incident this is done for. It must exist, be unresolved and be about a key on this credential. The compromise and that key's rotation are audited with its id, and it is resolved as confirmed."`
}

// CompromisedCredential is what a credential compromise did: the credential,
// now holding its new secret, and the new secret of every virtual key on it.
type CompromisedCredential struct {
	Credential  Credential   `json:"credential"`
	RotatedKeys []RotatedKey `json:"rotated_keys" doc:"Every virtual key on the credential that was not revoked, oldest first, with its new secret, shown only this once. Each is an app that needs its new key."`
}

// CompromiseCredential answers a leaked credential in one transaction: it
// replaces the credential's secret, keeping its id, and rotates every virtual
// key on it that is not revoked with no grace, since a leak is no time to
// leave an old secret working. It returns the new secrets, which are the apps
// that need new keys. Either everything changes or nothing does. The real
// secret must already be revoked at the provider: nothing kman does reaches it.
func (s *Service) CompromiseCredential(ctx context.Context, req CompromiseCredentialRequest) (CompromisedCredential, error) {
	var out CompromisedCredential
	fx := &effects{}
	c0, err := caller(ctx)
	if err != nil {
		return CompromisedCredential{}, err
	}
	err = s.commandWith(ctx, fx, func(tx *store.Tx, now time.Time) ([]audit, error) {
		before, err := credentialByRef(tx, req.Ref)
		if err != nil {
			return nil, err
		}
		var inc store.Incident
		if req.IncidentID != 0 {
			if inc, err = incidentForCredential(tx, req.IncidentID, before.ID); err != nil {
				return nil, err
			}
		}
		u, err := tx.Upstream(before.UpstreamID)
		if err != nil {
			return nil, err
		}
		pub, err := publishedUpstream(u)
		if err != nil {
			return nil, err
		}
		sealed, err := s.sealCredential(before.ID, string(req.Secret), pub.Auth)
		if err != nil {
			return nil, err
		}
		row := before
		row.Ciphertext, row.Nonce, row.DataKeyID = sealed.Ciphertext, sealed.Nonce, sealed.DataKeyID
		row.Last4, row.RotatedAt = last4(string(req.Secret)), now
		c, err := tx.UpdateCredential(row)
		if err != nil {
			return nil, fromStore(err, "credential "+row.Name)
		}
		fx.seal = append(fx.seal, sealedSecret{c.ID, sealed})
		fx.change.Credentials = append(fx.change.Credentials, publishedCredential(c))
		out.Credential = credentialDTO(c, u.Name)
		entries := []audit{{action: "credential.compromise", kind: "credential", id: c.ID, before: credentialDTO(before, u.Name), after: out.Credential, incident: req.IncidentID}}

		keys, err := tx.Keys()
		if err != nil {
			return nil, err
		}
		out.RotatedKeys = []RotatedKey{}
		for _, k := range keys {
			if k.CredentialID != c.ID || k.Status == store.KeyRevoked {
				continue
			}
			entry, rotated, err := s.rotateKey(tx, fx, now, k.ID, 0)
			if err != nil {
				return nil, err
			}
			if k.ID == inc.KeyID && req.IncidentID != 0 {
				entry.incident = req.IncidentID
			}
			entries = append(entries, entry)
			out.RotatedKeys = append(out.RotatedKeys, rotated)
		}
		if req.IncidentID != 0 {
			resolved, err := resolveIncident(tx, inc, store.ResolutionConfirmed, "incident.resolve", c0.Actor, now)
			if err != nil {
				return nil, err
			}
			entries = append(entries, resolved)
		}
		return entries, nil
	})
	if err != nil {
		return CompromisedCredential{}, err
	}
	s.wakeSettling()
	return out, nil
}
