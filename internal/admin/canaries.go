package admin

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/kirank55/kman/internal/store"
	"github.com/kirank55/kman/internal/token"
)

// A canary's states, as the API shows them.
const (
	CanaryArmed   = "armed"
	CanaryFired   = "fired"
	CanaryRevoked = "revoked"
)

// The formats a canary's snippet comes in.
const (
	FormatEnv   = "env"
	FormatMCP   = "mcp"
	FormatShell = "shell"
	FormatCI    = "ci"
)

// Limits on a canary.
const (
	maxPlanted = 200
	maxIgnore  = 100
	// minIgnoreBits4 and minIgnoreBits6 are the widest network an ignore list
	// takes: a list that held every address would silence the canary, which is
	// the one thing it must not do.
	minIgnoreBits4 = 16
	minIgnoreBits6 = 32
	maxBaseURL     = 256
	defaultVar     = "OPENAI_API_KEY"
)

// envVar is the rule for the variable a snippet sets.
var envVar = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// Canary is a canary key as the admin API shows it: a virtual key with no
// credential that forwards nothing, so any use of it is a leak. It never holds
// the secret, which is shown once, in CreatedCanary.
type Canary struct {
	ID          string        `json:"id" doc:"The key id: the non-secret handle inside the canary's key, and how this API addresses it. It is the id the canary's alerts and its events name."`
	Name        string        `json:"name,omitempty" doc:"An optional label, unique among keys not revoked."`
	Planted     string        `json:"planted" doc:"Where it was planted, in the operator's words: what an alert says so that the leak can be found."`
	Tags        []string      `json:"tags"`
	State       string        `json:"state" doc:"armed: its next use raises an alert. fired: it was used since it was armed, so more uses count and only now and then alert again, until it is reset. revoked: it is over and answers key_revoked."`
	Ignore      []string      `json:"ignore" doc:"Addresses and networks whose use does not fire it, such as the operator's own scanners. The use is still an event."`
	CreatedAt   time.Time     `json:"created_at"`
	ArmedAt     time.Time     `json:"armed_at" doc:"When it was made or last reset."`
	FiredAt     *time.Time    `json:"fired_at,omitempty" doc:"The first use since it was armed. Absent while it is armed."`
	LastFiredAt *time.Time    `json:"last_fired_at,omitempty" doc:"The last use. A reset keeps it."`
	Fires       int64         `json:"fires" doc:"How many uses were counted since the canary was made, from addresses not on its ignore list."`
	LastUse     *CanaryOrigin `json:"last_use,omitempty" doc:"Where the last use came from."`
}

// CanaryOrigin is where a canary's use came from. The address is personal data,
// so it is left out unless the expose_client_addresses setting is on (ADR 0010);
// the country, the AS and the client are not.
type CanaryOrigin struct {
	Address         string `json:"address,omitempty" doc:"The client address. Left out unless the expose_client_addresses setting is on."`
	AddressWithheld bool   `json:"address_withheld,omitempty" doc:"There is an address, which is left out because expose_client_addresses is off."`
	Country         string `json:"country,omitempty" doc:"An ISO 3166-1 alpha-2 code, when a GeoIP database names one."`
	ASN             int64  `json:"asn,omitempty"`
	ASOrg           string `json:"as_org,omitempty" doc:"The organisation the ASN belongs to, when the ASN database names one."`
	Client          string `json:"client,omitempty" doc:"What called: the User-Agent reduced to a product and major version, such as curl/8, or none."`
}

// CreateCanaryRequest is a new canary.
type CreateCanaryRequest struct {
	Name    string   `json:"name,omitempty" doc:"An optional label: 1 to 63 of a-z, 0-9, '.', '_' and '-', starting with a letter or digit, unique among keys not revoked. It stays out of the snippet."`
	Planted string   `json:"planted" doc:"Where it will be planted: 1 to 200 characters, with no control characters. An alert names it."`
	Format  string   `json:"format,omitempty" doc:"The snippet to plant: env (a .env line), mcp (an MCP server config's env block), shell (an export line) or ci (a gh secret set command). Defaults to env."`
	Var     string   `json:"var,omitempty" doc:"The variable the snippet sets, such as OPENAI_API_KEY (the default). A thief finds a key by the name it is under."`
	BaseURL string   `json:"base_url,omitempty" doc:"kman's proxy URL, as an app would be told it. When given, the snippet sets the variable's base URL too, so a thief who finds the snippet alone can reach kman with it."`
	Tags    []string `json:"tags,omitempty" doc:"At most 32 labels of a-z, 0-9, '.', '_', ':' and '-'."`
	Ignore  []string `json:"ignore,omitempty" doc:"Addresses and networks (CIDR) whose use does not fire the canary. At most 100, none wider than an IPv4 /16 or an IPv6 /32."`
}

// CreatedCanary is a new canary, its secret and the snippet that plants it. The
// secret and the snippet, which holds it, are shown only this once.
type CreatedCanary struct {
	Canary  Canary `json:"canary"`
	Secret  Secret `json:"secret" doc:"The canary's key (kmn_...), in the standard format: nothing in it says it is a canary. kman keeps only its hash, so it is shown only this once."`
	Format  string `json:"format" doc:"The format of the snippet."`
	Snippet string `json:"snippet" doc:"What to plant, in the format asked for. It holds the secret, so it is shown only this once, and it says nothing of what it is: not the canary's name, where it is planted, or its tags."`
}

// CanaryList is every canary, oldest first.
type CanaryList struct {
	Canaries []Canary `json:"canaries"`
}

// CanaryRef names a canary by its key id.
type CanaryRef struct {
	ID string `json:"-" path:"id" doc:"The canary's key id."`
}

// CanaryUpdate changes what it gives of a canary, and leaves the rest.
type CanaryUpdate struct {
	ID      string    `json:"-" path:"id" doc:"The canary's key id."`
	Planted *string   `json:"planted,omitempty" doc:"A new place it was planted."`
	Tags    *[]string `json:"tags,omitempty" doc:"The new tags, which replace the old ones."`
	Ignore  *[]string `json:"ignore,omitempty" doc:"The new ignore list, which replaces the old one; empty clears it."`
}

func checkPlanted(s string) error {
	switch {
	case strings.TrimSpace(s) == "":
		return errorf(CodeInvalid, "say where the canary is planted: an alert names the place")
	case len(s) > maxPlanted:
		return errorf(CodeInvalid, "where it is planted is over %d bytes", maxPlanted)
	case strings.ContainsFunc(s, unicode.IsControl):
		return errorf(CodeInvalid, "where it is planted holds a control character, such as a line break")
	}
	return nil
}

// ignoreList validates an ignore list and returns it as it is stored: each
// entry an address or a network written canonically, and none twice.
func ignoreList(entries []string) ([]string, error) {
	if len(entries) > maxIgnore {
		return nil, errorf(CodeInvalid, "a canary ignores at most %d addresses", maxIgnore)
	}
	out := make([]string, 0, len(entries))
	for _, raw := range entries {
		e := strings.TrimSpace(raw)
		var canonical string
		if strings.Contains(e, "/") {
			p, err := netip.ParsePrefix(e)
			if err != nil {
				return nil, errorf(CodeInvalid, "ignore %q is not an address or a network such as 192.0.2.0/24", raw)
			}
			if p.Addr().Is4In6() {
				return nil, errorf(CodeInvalid, "ignore %q is an IPv4 network written as IPv6: write it as IPv4", raw)
			}
			if (p.Addr().Is4() && p.Bits() < minIgnoreBits4) || (p.Addr().Is6() && p.Bits() < minIgnoreBits6) {
				return nil, errorf(CodeInvalid, "ignore %q is too wide: a list that held that many addresses would silence the canary (no wider than an IPv4 /%d or an IPv6 /%d)",
					raw, minIgnoreBits4, minIgnoreBits6)
			}
			canonical = p.Masked().String()
		} else {
			a, err := netip.ParseAddr(e)
			if err != nil || a.Zone() != "" {
				return nil, errorf(CodeInvalid, "ignore %q is not an address or a network such as 192.0.2.0/24", raw)
			}
			canonical = a.Unmap().String()
		}
		for _, have := range out {
			if have == canonical {
				return nil, errorf(CodeInvalid, "ignore %q is given twice", canonical)
			}
		}
		out = append(out, canonical)
	}
	return out, nil
}

func marshalIgnore(list []string) (string, error) {
	raw, err := json.Marshal(list)
	if err != nil {
		return "", fmt.Errorf("admin: marshal ignore list: %w", err)
	}
	return string(raw), nil
}

// checkBaseURL refuses a base URL that is not plainly one: it goes into a
// snippet that is read by a shell or an env file, so nothing in it may be quoted.
func checkBaseURL(raw string) error {
	u, err := url.Parse(raw)
	switch {
	case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil:
		return errorf(CodeInvalid, "base_url %q is not an http or https URL without credentials", raw)
	case len(raw) > maxBaseURL:
		return errorf(CodeInvalid, "base_url is over %d bytes", maxBaseURL)
	case strings.ContainsFunc(raw, func(r rune) bool { return r <= ' ' || r == 0x7f || strings.ContainsRune(`'"\`+"`$", r) }):
		return errorf(CodeInvalid, "base_url %q holds a space, a quote or a character a shell or an env file would read", raw)
	}
	return nil
}

// baseVar is the variable an SDK reads the proxy URL from, given the one it
// reads the key from: OPENAI_API_KEY and OPENAI_BASE_URL.
func baseVar(keyVar string) string {
	return strings.TrimSuffix(keyVar, "_API_KEY") + "_BASE_URL"
}

// plantSnippet is the text to plant for a canary, in format. It holds the secret
// and, if given, the proxy's URL, and nothing else: whatever a thief reads in it
// must not say it is a canary, so it never names one.
func plantSnippet(format, variable, baseURL, secret string) string {
	switch format {
	case FormatMCP:
		env := map[string]string{variable: secret}
		if baseURL != "" {
			env[baseVar(variable)] = baseURL
		}
		server, _, _ := strings.Cut(strings.ToLower(variable), "_")
		doc := map[string]any{"mcpServers": map[string]any{server: map[string]any{"env": env}}}
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		_ = enc.Encode(doc) // strings and maps of strings cannot fail to encode
		return b.String()
	case FormatShell:
		var b strings.Builder
		if baseURL != "" {
			fmt.Fprintf(&b, "export %s=%s\n", baseVar(variable), shellQuote(baseURL))
		}
		fmt.Fprintf(&b, "export %s=%s\n", variable, secret)
		return b.String()
	case FormatCI:
		var b strings.Builder
		if baseURL != "" {
			fmt.Fprintf(&b, "gh variable set %s --body %s\n", baseVar(variable), shellQuote(baseURL))
		}
		fmt.Fprintf(&b, "gh secret set %s --body %s\n", variable, secret)
		return b.String()
	}
	var b strings.Builder
	if baseURL != "" {
		fmt.Fprintf(&b, "%s=%s\n", baseVar(variable), baseURL)
	}
	fmt.Fprintf(&b, "%s=%s\n", variable, secret)
	return b.String()
}

// canaryDTO is a canary as the API shows it. The address of its last use is
// given out only when expose is set.
func canaryDTO(k store.VirtualKey, c store.Canary, expose bool) (Canary, error) {
	tags := []string{}
	if err := json.Unmarshal([]byte(k.TagsJSON), &tags); err != nil {
		return Canary{}, fmt.Errorf("admin: canary %s: tags: %w", k.ID, err)
	}
	ignore := []string{}
	if err := json.Unmarshal([]byte(c.IgnoreJSON), &ignore); err != nil {
		return Canary{}, fmt.Errorf("admin: canary %s: ignore list: %w", k.ID, err)
	}
	state := CanaryArmed
	switch {
	case k.Status == store.KeyRevoked:
		state = CanaryRevoked
	case !c.FiredAt.IsZero():
		state = CanaryFired
	}
	out := Canary{ID: k.ID, Name: k.Name, Planted: c.Planted, Tags: tags, State: state, Ignore: ignore,
		CreatedAt: k.CreatedAt, ArmedAt: c.ArmedAt, FiredAt: timePtr(c.FiredAt), LastFiredAt: timePtr(c.LastFiredAt), Fires: c.Fires}
	if c.Fires > 0 {
		u := c.Last
		o := CanaryOrigin{Country: u.Country, ASN: u.ASN, ASOrg: u.ASOrg, Client: u.Client}
		if u.Address.IsValid() {
			if expose {
				o.Address = u.Address.String()
			} else {
				o.AddressWithheld = true
			}
		}
		out.LastUse = &o
	}
	return out, nil
}

// CreateCanary makes a canary: a key in the standard format that has no
// credential, so it forwards nothing, and no expiry, since an expired key answers
// key_expired and a canary must go on telling it was used. Its secret and the
// snippet to plant are returned once.
func (s *Service) CreateCanary(ctx context.Context, req CreateCanaryRequest) (CreatedCanary, error) {
	if req.Name != "" {
		if err := checkName("canary", req.Name); err != nil {
			return CreatedCanary{}, err
		}
	}
	if err := checkPlanted(req.Planted); err != nil {
		return CreatedCanary{}, err
	}
	format := cmp.Or(req.Format, FormatEnv)
	switch format {
	case FormatEnv, FormatMCP, FormatShell, FormatCI:
	default:
		return CreatedCanary{}, errorf(CodeInvalid, "format %q is not one of env, mcp, shell or ci", req.Format)
	}
	variable := cmp.Or(req.Var, defaultVar)
	if !envVar.MatchString(variable) {
		return CreatedCanary{}, errorf(CodeInvalid, "var %q: use capital letters, digits and '_', starting with a letter, as an environment variable is named", variable)
	}
	if req.BaseURL != "" {
		if err := checkBaseURL(req.BaseURL); err != nil {
			return CreatedCanary{}, err
		}
	}
	if err := checkTags(req.Tags); err != nil {
		return CreatedCanary{}, err
	}
	tags, err := marshalTags(req.Tags)
	if err != nil {
		return CreatedCanary{}, err
	}
	ignore, err := ignoreList(req.Ignore)
	if err != nil {
		return CreatedCanary{}, err
	}
	ignoreJSON, err := marshalIgnore(ignore)
	if err != nil {
		return CreatedCanary{}, err
	}
	version, policy, err := s.normalizePolicy([]byte(`{}`))
	if err != nil {
		return CreatedCanary{}, err
	}
	id, err := token.NewKeyIDFrom(s.rand)
	if err != nil {
		return CreatedCanary{}, err
	}
	minted, err := token.MintVirtualKeyFrom(id, s.rand)
	if err != nil {
		return CreatedCanary{}, err
	}
	var out CreatedCanary
	fx := &effects{}
	err = s.commandWith(ctx, fx, func(tx *store.Tx, now time.Time) ([]audit, error) {
		k, err := tx.CreateKey(store.VirtualKey{ID: string(id), Name: req.Name, Kind: store.KeyCanary, TagsJSON: tags,
			PolicyJSON: policy, PolicyVersion: version, CreatedAt: now}, minted.Hash[:])
		if err != nil {
			return nil, fromStore(err, "key "+cmp.Or(req.Name, string(id)))
		}
		c, err := tx.CreateCanary(store.Canary{KeyID: k.ID, Planted: req.Planted, IgnoreJSON: ignoreJSON, ArmedAt: now})
		if err != nil {
			return nil, fromStore(err, "canary "+k.ID)
		}
		published, err := publishedKey(tx, k)
		if err != nil {
			return nil, err
		}
		fx.change.Keys = append(fx.change.Keys, published)
		dto, err := canaryDTO(k, c, false)
		if err != nil {
			return nil, err
		}
		out = CreatedCanary{Canary: dto, Secret: Secret(minted.Reveal()), Format: format,
			Snippet: plantSnippet(format, variable, req.BaseURL, minted.Reveal())}
		return []audit{{action: "canary.create", kind: "canary", id: k.ID, after: dto}}, nil
	})
	if err != nil {
		return CreatedCanary{}, err
	}
	return out, nil
}

// ListCanaries returns every canary, oldest first, revoked ones too.
func (s *Service) ListCanaries(ctx context.Context) (CanaryList, error) {
	out := CanaryList{Canaries: []Canary{}}
	err := s.read(ctx, func(tx *store.Tx) error {
		settings, err := loadSettings(tx)
		if err != nil {
			return err
		}
		rows, err := tx.Canaries()
		if err != nil {
			return err
		}
		for _, c := range rows {
			k, err := tx.Key(c.KeyID)
			if err != nil {
				return err
			}
			dto, err := canaryDTO(k, c, settings.ExposeClientAddresses)
			if err != nil {
				return err
			}
			out.Canaries = append(out.Canaries, dto)
		}
		return nil
	})
	return out, err
}

// GetCanary returns one canary, by key id. A key that is not a canary is not
// found.
func (s *Service) GetCanary(ctx context.Context, req CanaryRef) (Canary, error) {
	var out Canary
	err := s.read(ctx, func(tx *store.Tx) error {
		settings, err := loadSettings(tx)
		if err != nil {
			return err
		}
		c, err := tx.Canary(req.ID)
		if err != nil {
			return fromStore(err, "canary "+req.ID)
		}
		k, err := tx.Key(req.ID)
		if err != nil {
			return err
		}
		out, err = canaryDTO(k, c, settings.ExposeClientAddresses)
		return err
	})
	return out, err
}

// ResetCanary arms a fired canary again, so that its next use raises an alert.
// What it has recorded of its use stays. Resetting an armed canary changes
// nothing, and a revoked canary is over.
func (s *Service) ResetCanary(ctx context.Context, req CanaryRef) (Canary, error) {
	var out Canary
	err := s.command(ctx, func(tx *store.Tx, now time.Time) ([]audit, error) {
		before, err := tx.Canary(req.ID)
		if err != nil {
			return nil, fromStore(err, "canary "+req.ID)
		}
		k, err := tx.Key(req.ID)
		if err != nil {
			return nil, err
		}
		if k.Status == store.KeyRevoked {
			return nil, errorf(CodeConflict, "canary %s is revoked, so it cannot be armed again: create a new one", req.ID)
		}
		settings, err := loadSettings(tx)
		if err != nil {
			return nil, err
		}
		changed, c, err := tx.ResetCanary(req.ID, now)
		if err != nil {
			return nil, fromStore(err, "canary "+req.ID)
		}
		if out, err = canaryDTO(k, c, settings.ExposeClientAddresses); err != nil {
			return nil, err
		}
		if !changed {
			return nil, nil
		}
		prior, err := canaryDTO(k, before, false)
		if err != nil {
			return nil, err
		}
		after, err := canaryDTO(k, c, false)
		if err != nil {
			return nil, err
		}
		return []audit{{action: "canary.reset", kind: "canary", id: k.ID, before: prior, after: after}}, nil
	})
	return out, err
}

// UpdateCanary changes where a canary is planted, its tags and its ignore list.
// An update that changes nothing records nothing, and a revoked canary is over.
func (s *Service) UpdateCanary(ctx context.Context, req CanaryUpdate) (Canary, error) {
	if req.Planted == nil && req.Tags == nil && req.Ignore == nil {
		return Canary{}, errorf(CodeInvalid, "the update changes nothing")
	}
	if req.Planted != nil {
		if err := checkPlanted(*req.Planted); err != nil {
			return Canary{}, err
		}
	}
	if req.Tags != nil {
		if err := checkTags(*req.Tags); err != nil {
			return Canary{}, err
		}
	}
	var ignore []string
	if req.Ignore != nil {
		var err error
		if ignore, err = ignoreList(*req.Ignore); err != nil {
			return Canary{}, err
		}
	}
	var out Canary
	fx := &effects{}
	err := s.commandWith(ctx, fx, func(tx *store.Tx, now time.Time) ([]audit, error) {
		before, err := tx.Canary(req.ID)
		if err != nil {
			return nil, fromStore(err, "canary "+req.ID)
		}
		k, err := tx.Key(req.ID)
		if err != nil {
			return nil, err
		}
		if k.Status == store.KeyRevoked {
			return nil, errorf(CodeConflict, "canary %s is revoked", req.ID)
		}
		settings, err := loadSettings(tx)
		if err != nil {
			return nil, err
		}
		prior, err := canaryDTO(k, before, false)
		if err != nil {
			return nil, err
		}
		planted, ignoreJSON, tagsJSON := before.Planted, before.IgnoreJSON, k.TagsJSON
		set(&planted, req.Planted)
		if req.Ignore != nil {
			if ignoreJSON, err = marshalIgnore(ignore); err != nil {
				return nil, err
			}
		}
		if req.Tags != nil {
			if tagsJSON, err = marshalTags(*req.Tags); err != nil {
				return nil, err
			}
		}
		if planted == before.Planted && ignoreJSON == before.IgnoreJSON && tagsJSON == k.TagsJSON {
			out, err = canaryDTO(k, before, settings.ExposeClientAddresses)
			return nil, err
		}
		if tagsJSON != k.TagsJSON {
			row := k
			row.TagsJSON = tagsJSON
			if k, err = tx.UpdateKey(row); err != nil {
				return nil, fromStore(err, "canary "+req.ID)
			}
			published, err := publishedKey(tx, k)
			if err != nil {
				return nil, err
			}
			fx.change.Keys = append(fx.change.Keys, published)
		}
		c, err := tx.UpdateCanary(req.ID, planted, ignoreJSON)
		if err != nil {
			return nil, fromStore(err, "canary "+req.ID)
		}
		after, err := canaryDTO(k, c, false)
		if err != nil {
			return nil, err
		}
		if out, err = canaryDTO(k, c, settings.ExposeClientAddresses); err != nil {
			return nil, err
		}
		return []audit{{action: "canary.update", kind: "canary", id: k.ID, before: prior, after: after}}, nil
	})
	return out, err
}

// refuseCanary is the error a key command gives for a canary: the key commands
// would disarm one or make it show what it is, so a canary has commands of its
// own. Revoking is the one the key commands share, and it retires a canary.
func refuseCanary(k store.VirtualKey) error {
	if k.Kind != store.KeyCanary {
		return nil
	}
	return errorf(CodeConflict, "key %s is a canary: manage it with kman canary list, update or reset, and retire it with kman key revoke", k.ID)
}
