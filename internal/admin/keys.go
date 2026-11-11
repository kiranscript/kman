package admin

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/kirank55/kman/internal/registry"
	"github.com/kirank55/kman/internal/store"
	"github.com/kirank55/kman/internal/token"
)

// Key is a virtual key as the admin API shows it. It never holds the secret,
// which is shown once, in CreatedKey.
type Key struct {
	ID            string          `json:"id" doc:"The key id: the non-secret handle inside the key, and how this API addresses it."`
	Name          string          `json:"name,omitempty" doc:"An optional label, unique among keys not revoked."`
	UpstreamID    string          `json:"upstream_id,omitempty"`
	Upstream      string          `json:"upstream,omitempty" doc:"The upstream's name. Absent once a revoked key's upstream is deleted."`
	CredentialID  string          `json:"credential_id,omitempty"`
	Credential    string          `json:"credential,omitempty" doc:"The credential's name. Absent once a revoked key's credential is deleted."`
	Status        string          `json:"status" doc:"active, disabled or revoked."`
	Expired       bool            `json:"expired" doc:"Whether its expiry has passed, whatever its status."`
	Note          string          `json:"note,omitempty"`
	Tags          []string        `json:"tags"`
	Policy        json.RawMessage `json:"policy" doc:"The key's policy, a JSON object: empty until it is edited."`
	PolicyVersion int             `json:"policy_version" doc:"The version of the policy format the policy is in."`
	ExpiresAt     time.Time       `json:"expires_at" doc:"When the key stops working. Every key has one: there is no never."`
	CreatedAt     time.Time       `json:"created_at"`
	Spend         *KeySpend       `json:"spend,omitempty" doc:"What the key has spent today and this month, beside its caps. Shown when the key is read, and not in the audit log."`
}

// KeySpend is what a key has spent today and this month. A request counts when it has been charged, whether or
// not the spend has reached the store yet.
type KeySpend struct {
	Today SpendPeriod `json:"today"`
	Month SpendPeriod `json:"month"`
}

// SpendPeriod is a key's spend in one day or month of the spend_timezone, with its cap.
type SpendPeriod struct {
	Period            string `json:"period" doc:"The day, such as 2026-11-11, or the month, such as 2026-11."`
	CostMicro         int64  `json:"cost_micro" doc:"What has been charged, in microdollars, estimated_micro included."`
	EstimatedMicro    int64  `json:"estimated_micro" doc:"The part of cost_micro charged as an estimate: the whole reservation, for a response that was cut off or reported no usage."`
	ReservedMicro     int64  `json:"reserved_micro" doc:"Held for requests in flight. A cap counts it as it counts cost_micro."`
	Requests          int64  `json:"requests" doc:"Requests charged."`
	UnmeteredRequests int64  `json:"unmetered_requests" doc:"Requests forwarded that could not be metered, whose cost is unknown and so is not in cost_micro."`
	CapMicro          int64  `json:"cap_micro,omitempty" doc:"The key's cap for the period, in microdollars. Absent for none."`
}

// CreatedKey is a new key and its secret, which is shown only this once.
type CreatedKey struct {
	Key         Key    `json:"key"`
	Secret      Secret `json:"secret" doc:"The virtual key itself (kmn_...). kman keeps only its hash, so it is shown only this once."`
	SDKEnv      string `json:"sdk_env,omitempty" doc:"The variable an SDK for its upstream reads the key from."`
	SDKBasePath string `json:"sdk_base_path,omitempty" doc:"Appended to kman's proxy URL to make an SDK's base URL."`
}

// KeyList is every key, or the one a name finds, oldest first.
type KeyList struct {
	Keys []Key `json:"keys"`
}

// KeyQuery filters the list of keys.
type KeyQuery struct {
	Name string `json:"-" query:"name" doc:"Only the key not revoked with this name."`
}

// KeyRef names a key by its key id.
type KeyRef struct {
	ID string `json:"-" path:"id" doc:"The key id."`
}

// CreateKeyRequest is a new virtual key on an upstream.
type CreateKeyRequest struct {
	Name       string          `json:"name,omitempty" doc:"An optional label: 1 to 63 of a-z, 0-9, '.', '_' and '-', starting with a letter or digit, unique among keys not revoked."`
	Upstream   string          `json:"upstream" doc:"The upstream's name or id."`
	Credential string          `json:"credential,omitempty" doc:"The credential's name or id. Needed when the upstream has more than one."`
	TTLSeconds *int64          `json:"ttl_seconds,omitempty" doc:"How long the key stays valid from now, at most the maximum key lifetime. Defaults to the default key TTL."`
	Note       string          `json:"note,omitempty" doc:"Free text of at most 1024 bytes, with no control characters."`
	Tags       []string        `json:"tags,omitempty" doc:"At most 32 labels of a-z, 0-9, '.', '_', ':' and '-'."`
	Policy     json.RawMessage `json:"policy,omitempty" doc:"The key's policy, a JSON object. Defaults to the empty policy."`
}

// KeyUpdate changes what it gives of a key, and leaves the rest.
type KeyUpdate struct {
	ID         string          `json:"-" path:"id" doc:"The key id."`
	Name       *string         `json:"name,omitempty" doc:"A new name, or empty for none."`
	Note       *string         `json:"note,omitempty" doc:"A new note, or empty for none."`
	Tags       *[]string       `json:"tags,omitempty" doc:"The new tags, which replace the old ones."`
	TTLSeconds *int64          `json:"ttl_seconds,omitempty" doc:"A new expiry, this long from now. It may not be later than the key's creation plus the maximum key lifetime, unless that shortens the key's life."`
	Policy     json.RawMessage `json:"policy,omitempty" doc:"The new policy, a JSON object, which replaces the old one."`
}

// Limits on a key's note and tags.
const (
	maxNote = 1024
	maxTags = 32
)

// tagRule is the rule for a tag: a name that may also hold ':', as in team:ml.
var tagRule = regexp.MustCompile(`^[a-z0-9][a-z0-9._:-]{0,62}$`)

func checkNote(note string) error {
	if len(note) > maxNote {
		return errorf(CodeInvalid, "the note is over %d bytes", maxNote)
	}
	if strings.ContainsFunc(note, unicode.IsControl) {
		return errorf(CodeInvalid, "the note holds a control character, such as a line break")
	}
	return nil
}

func checkTags(tags []string) error {
	if len(tags) > maxTags {
		return errorf(CodeInvalid, "a key has at most %d tags", maxTags)
	}
	for i, t := range tags {
		if !tagRule.MatchString(t) {
			return errorf(CodeInvalid, "tag %q: use 1 to 63 of a-z, 0-9, '.', '_', ':' and '-', starting with a letter or digit", t)
		}
		if slices.Contains(tags[:i], t) {
			return errorf(CodeInvalid, "tag %q is given twice", t)
		}
	}
	return nil
}

func marshalTags(tags []string) (string, error) {
	if tags == nil {
		tags = []string{}
	}
	raw, err := json.Marshal(tags)
	if err != nil {
		return "", fmt.Errorf("admin: marshal tags: %w", err)
	}
	return string(raw), nil
}

// normalizePolicy is raw as policy would store it, with its version.
func (s *Service) normalizePolicy(raw []byte) (int, string, error) {
	if s.policy == nil {
		return 0, "", errors.New("admin: no policy rules")
	}
	version, normalized, err := s.policy(raw)
	if err != nil {
		return 0, "", errorf(CodeInvalid, "%v", err)
	}
	return version, string(normalized), nil
}

// checkTTL refuses a time to live that is no time at all, or that is past
// max. There is no value for a key that never expires.
func checkTTL(ttl, max int64) error {
	if ttl <= 0 {
		return errorf(CodeInvalid, "ttl_seconds must be above 0: every key expires, within the maximum key lifetime of %d seconds", max)
	}
	if ttl > max {
		return errorf(CodeInvalid, "a TTL of %d seconds is past the maximum key lifetime of %d seconds", ttl, max)
	}
	return nil
}

// seconds is n seconds as a duration. Callers keep n within maxLifetime.
func seconds(n int64) time.Duration { return time.Duration(n) * time.Second }

// CreateKey mints a new virtual key on an upstream. The secret is returned
// once, here: only its hash is stored.
func (s *Service) CreateKey(ctx context.Context, req CreateKeyRequest) (CreatedKey, error) {
	if req.Name != "" {
		if err := checkName("key", req.Name); err != nil {
			return CreatedKey{}, err
		}
	}
	if req.Upstream == "" {
		return CreatedKey{}, errorf(CodeInvalid, "a key needs an upstream")
	}
	if err := checkNote(req.Note); err != nil {
		return CreatedKey{}, err
	}
	if err := checkTags(req.Tags); err != nil {
		return CreatedKey{}, err
	}
	tags, err := marshalTags(req.Tags)
	if err != nil {
		return CreatedKey{}, err
	}
	raw := []byte(req.Policy)
	if req.Policy == nil {
		raw = []byte(`{}`)
	}
	version, policy, err := s.normalizePolicy(raw)
	if err != nil {
		return CreatedKey{}, err
	}
	id, err := token.NewKeyIDFrom(s.rand)
	if err != nil {
		return CreatedKey{}, err
	}
	minted, err := token.MintVirtualKeyFrom(id, s.rand)
	if err != nil {
		return CreatedKey{}, err
	}
	var out CreatedKey
	fx := &effects{}
	err = s.commandWith(ctx, fx, func(tx *store.Tx, now time.Time) ([]audit, error) {
		settings, err := loadSettings(tx)
		if err != nil {
			return nil, err
		}
		ttl := settings.DefaultKeyTTLSeconds
		if req.TTLSeconds != nil {
			ttl = *req.TTLSeconds
		}
		if err := checkTTL(ttl, settings.MaxKeyLifetimeSeconds); err != nil {
			return nil, err
		}
		u, err := upstreamByRef(tx, req.Upstream)
		if err != nil {
			return nil, err
		}
		cred, err := keyCredential(tx, u, req.Credential)
		if err != nil {
			return nil, err
		}
		k, err := tx.CreateKey(store.VirtualKey{ID: string(id), Name: req.Name, CredentialID: cred.ID, Note: req.Note, TagsJSON: tags,
			PolicyJSON: policy, PolicyVersion: version, ExpiresAt: now.Add(seconds(ttl)), CreatedAt: now}, minted.Hash[:])
		if err != nil {
			return nil, fromStore(err, "key "+cmp.Or(req.Name, string(id)))
		}
		published, err := publishedKey(tx, k)
		if err != nil {
			return nil, err
		}
		fx.change.Keys = append(fx.change.Keys, published)
		dto, err := keyDTO(k, now, keyNames{cred.ID: {u.ID, u.Name, cred.Name}})
		if err != nil {
			return nil, err
		}
		out = CreatedKey{Key: dto, Secret: Secret(minted.Reveal()), SDKEnv: u.SDKEnv, SDKBasePath: u.SDKBasePath}
		return []audit{{action: "key.create", kind: "key", id: k.ID, after: dto}}, nil
	})
	if err != nil {
		return CreatedKey{}, err
	}
	return out, nil
}

// keyCredential is the credential a new key on u uses: the one ref names, or
// else u's only one.
func keyCredential(tx *store.Tx, u store.Upstream, ref string) (store.Credential, error) {
	if ref != "" {
		c, err := credentialByRef(tx, ref)
		if err != nil {
			return store.Credential{}, err
		}
		if c.UpstreamID != u.ID {
			return store.Credential{}, errorf(CodeInvalid, "credential %s is not on upstream %s", c.Name, u.Name)
		}
		return c, nil
	}
	all, err := tx.Credentials()
	if err != nil {
		return store.Credential{}, err
	}
	var on []store.Credential
	var names []string
	for _, c := range all {
		if c.UpstreamID == u.ID {
			on = append(on, c)
			names = append(names, c.Name)
		}
	}
	switch len(on) {
	case 0:
		return store.Credential{}, errorf(CodeConflict, "upstream %s has no credential: add one with kman credential add <name> --upstream %s", u.Name, u.Name)
	case 1:
		return on[0], nil
	}
	return store.Credential{}, errorf(CodeInvalid, "upstream %s has %d credentials: name one of %s", u.Name, len(on), strings.Join(names, ", "))
}

// ListKeys returns every key, oldest first, or the key not revoked with
// req.Name. The list has no key when no key has that name.
func (s *Service) ListKeys(ctx context.Context, req KeyQuery) (KeyList, error) {
	out := KeyList{Keys: []Key{}}
	now := s.now()
	err := s.read(ctx, func(tx *store.Tx) error {
		var rows []store.VirtualKey
		var err error
		if req.Name != "" {
			var k store.VirtualKey
			switch k, err = tx.KeyByName(req.Name); {
			case isNotFound(err):
				return nil
			case err != nil:
				return err
			}
			rows = []store.VirtualKey{k}
		} else if rows, err = tx.Keys(); err != nil {
			return err
		}
		names, err := liveKeyNames(tx)
		if err != nil {
			return err
		}
		for _, k := range rows {
			dto, err := keyDTO(k, now, names)
			if err != nil {
				return err
			}
			out.Keys = append(out.Keys, s.withSpend(dto))
		}
		return nil
	})
	return out, err
}

// GetKey returns one key, by its key id.
func (s *Service) GetKey(ctx context.Context, req KeyRef) (Key, error) {
	var out Key
	now := s.now()
	err := s.read(ctx, func(tx *store.Tx) error {
		k, err := tx.Key(req.ID)
		if err != nil {
			return fromStore(err, "key "+req.ID)
		}
		names, err := liveKeyNames(tx)
		if err != nil {
			return err
		}
		out, err = keyDTO(k, now, names)
		out = s.withSpend(out)
		return err
	})
	return out, err
}

// withSpend adds what the key has spent, when there is a meter to ask. It is for what
// the API shows and not for what it records.
func (s *Service) withSpend(k Key) Key {
	if s.spend != nil {
		spend := s.spend(k.ID, string(k.Policy))
		k.Spend = &spend
	}
	return k
}

// UpdateKey changes what req gives. An update that changes nothing records
// nothing. A revoked key is over, so it cannot be updated.
func (s *Service) UpdateKey(ctx context.Context, req KeyUpdate) (Key, error) {
	if req.Name == nil && req.Note == nil && req.Tags == nil && req.TTLSeconds == nil && req.Policy == nil {
		return Key{}, errorf(CodeInvalid, "the update changes nothing")
	}
	if req.Name != nil && *req.Name != "" {
		if err := checkName("key", *req.Name); err != nil {
			return Key{}, err
		}
	}
	if req.Note != nil {
		if err := checkNote(*req.Note); err != nil {
			return Key{}, err
		}
	}
	if req.Tags != nil {
		if err := checkTags(*req.Tags); err != nil {
			return Key{}, err
		}
	}
	var out Key
	fx := &effects{}
	err := s.commandWith(ctx, fx, func(tx *store.Tx, now time.Time) ([]audit, error) {
		before, err := tx.Key(req.ID)
		if err != nil {
			return nil, fromStore(err, "key "+req.ID)
		}
		if before.Status == store.KeyRevoked {
			return nil, errorf(CodeConflict, "key %s is revoked", req.ID)
		}
		row := before
		set(&row.Name, req.Name)
		set(&row.Note, req.Note)
		if req.Tags != nil {
			if row.TagsJSON, err = marshalTags(*req.Tags); err != nil {
				return nil, err
			}
		}
		if req.Policy != nil {
			if row.PolicyVersion, row.PolicyJSON, err = s.normalizePolicy(req.Policy); err != nil {
				return nil, err
			}
		}
		if req.TTLSeconds != nil {
			if row.ExpiresAt, err = extendedExpiry(tx, before, *req.TTLSeconds, now); err != nil {
				return nil, err
			}
		}
		names, err := liveKeyNames(tx)
		if err != nil {
			return nil, err
		}
		prior, err := keyDTO(before, now, names)
		if err != nil {
			return nil, err
		}
		if row == before {
			out = prior
			return nil, nil
		}
		k, err := tx.UpdateKey(row)
		if err != nil {
			return nil, fromStore(err, "key "+row.Name)
		}
		published, err := publishedKey(tx, k)
		if err != nil {
			return nil, err
		}
		fx.change.Keys = append(fx.change.Keys, published)
		if out, err = keyDTO(k, now, names); err != nil {
			return nil, err
		}
		return []audit{{action: "key.update", kind: "key", id: k.ID, before: prior, after: out}}, nil
	})
	if err != nil {
		return Key{}, err
	}
	return out, nil
}

// extendedExpiry is when k would expire ttl seconds from now. The maximum key
// lifetime counts from the key's creation, so repeated updates cannot keep a
// key alive past it. A key already past a since-lowered maximum may still be
// brought closer to it.
func extendedExpiry(tx *store.Tx, k store.VirtualKey, ttl int64, now time.Time) (time.Time, error) {
	settings, err := loadSettings(tx)
	if err != nil {
		return time.Time{}, err
	}
	if ttl <= 0 || ttl > maxLifetime {
		return time.Time{}, checkTTL(ttl, min(settings.MaxKeyLifetimeSeconds, maxLifetime))
	}
	expires, limit := now.Add(seconds(ttl)), k.CreatedAt.Add(seconds(settings.MaxKeyLifetimeSeconds))
	if expires.After(limit) && !expires.Before(k.ExpiresAt) {
		return time.Time{}, errorf(CodeInvalid, "the key may not outlive %s, its creation plus the maximum key lifetime; to go past that, create a new key",
			limit.UTC().Format(time.DateOnly))
	}
	return expires, nil
}

// keyName is what a key shows of its credential.
type keyName struct{ upstreamID, upstream, credential string }

// keyNames is the keyName of each credential, by credential id.
type keyNames map[string]keyName

// liveKeyNames reads keyNames for every live credential.
func liveKeyNames(tx *store.Tx) (keyNames, error) {
	creds, err := tx.Credentials()
	if err != nil {
		return nil, err
	}
	upstreams, err := tx.Upstreams()
	if err != nil {
		return nil, err
	}
	out := keyNames{}
	for _, c := range creds {
		i := slices.IndexFunc(upstreams, func(u store.Upstream) bool { return u.ID == c.UpstreamID })
		if i >= 0 {
			out[c.ID] = keyName{upstreams[i].ID, upstreams[i].Name, c.Name}
		}
	}
	return out, nil
}

// keyDTO is the row as the API shows it at now.
func keyDTO(k store.VirtualKey, now time.Time, names keyNames) (Key, error) {
	tags := []string{}
	if err := json.Unmarshal([]byte(k.TagsJSON), &tags); err != nil {
		return Key{}, fmt.Errorf("admin: key %s: tags: %w", k.ID, err)
	}
	n := names[k.CredentialID]
	return Key{ID: k.ID, Name: k.Name, UpstreamID: n.upstreamID, Upstream: n.upstream, CredentialID: k.CredentialID, Credential: n.credential,
		Status: k.Status, Expired: !now.Before(k.ExpiresAt), Note: k.Note, Tags: tags,
		Policy: json.RawMessage(k.PolicyJSON), PolicyVersion: k.PolicyVersion, ExpiresAt: k.ExpiresAt, CreatedAt: k.CreatedAt}, nil
}

// publishedKey is the row as the request path reads it, with the hashes of the
// secrets that can authenticate it, so a request needs no store read. A retired
// secret never authenticates, so it stays in the store.
func publishedKey(tx *store.Tx, k store.VirtualKey) (registry.Key, error) {
	stored, err := tx.KeySecrets(k.ID)
	if err != nil {
		return registry.Key{}, err
	}
	secrets := make([]registry.KeySecret, 0, len(stored))
	for _, s := range stored {
		if s.State == store.SecretRetired {
			continue
		}
		if len(s.Hash) != len(token.Hash{}) {
			return registry.Key{}, fmt.Errorf("key %s: a secret hash of %d bytes", k.ID, len(s.Hash))
		}
		secrets = append(secrets, registry.KeySecret{Hash: token.Hash(s.Hash), ValidUntil: s.ValidUntil})
	}
	return registry.Key{ID: k.ID, Name: k.Name, CredentialID: k.CredentialID, Status: k.Status, Secrets: secrets, ExpiresAt: k.ExpiresAt,
		Policy: k.PolicyJSON, PolicyVersion: k.PolicyVersion, Generation: k.Generation}, nil
}
