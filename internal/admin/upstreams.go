package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/kirank55/kman/internal/registry"
	"github.com/kirank55/kman/internal/store"
)

// Auth is how a credential is presented to an upstream.
type Auth struct {
	Style         string `json:"style" doc:"header or basic."`
	Header        string `json:"header,omitempty" doc:"For the header style, the template: Name: value {key}."`
	BasicUsername string `json:"basic_username,omitempty" doc:"For the basic style, the username. When absent, a credential's secret is the whole user:password."`
}

// Upstream is an upstream as the admin API shows it.
type Upstream struct {
	ID              string       `json:"id"`
	Name            string       `json:"name"`
	Preset          string       `json:"preset,omitempty" doc:"The preset it was copied from. Absent for a custom upstream."`
	PresetVersion   int          `json:"preset_version,omitempty" doc:"The version of the preset it was last copied from."`
	UpdateAvailable bool         `json:"update_available,omitempty" doc:"Whether this kman holds a newer version of the preset: see the sync operation."`
	Profile         string       `json:"profile" doc:"openai, anthropic or generic."`
	Host            string       `json:"host"`
	Port            int          `json:"port"`
	TLS             bool         `json:"tls"`
	ServerName      string       `json:"server_name,omitempty" doc:"The TLS server name, when it is not the host."`
	Paths           []string     `json:"path_allowlist" doc:"The rules a request must match, as METHOD /path: * is any method or one segment, and a last segment of ** is the rest of the path."`
	InjectUsage     bool         `json:"inject_usage" doc:"Whether OpenAI-style streams get stream_options.include_usage."`
	Auth            Auth         `json:"auth"`
	SDKEnv          string       `json:"sdk_env,omitempty" doc:"The variable an SDK for this upstream reads its key from."`
	SDKBasePath     string       `json:"sdk_base_path,omitempty" doc:"Appended to kman's proxy URL to make an SDK's base URL."`
	Credentials     []Credential `json:"credentials,omitempty" doc:"Its credentials, by name. Never a secret."`
	CreatedAt       time.Time    `json:"created_at"`
	UpdatedAt       time.Time    `json:"updated_at"`
}

// UpstreamList is every upstream that is not deleted, by name.
type UpstreamList struct {
	Upstreams []Upstream `json:"upstreams"`
}

// UpstreamRef names an upstream.
type UpstreamRef struct {
	Ref string `json:"-" path:"ref" doc:"The upstream's name or id."`
}

// UpstreamSettings are what can be set on an upstream. Each is left as it is
// when absent: as the preset or the default has it when adding, and unchanged
// when updating.
type UpstreamSettings struct {
	Profile     *string   `json:"profile,omitempty" doc:"openai, anthropic or generic. A custom upstream defaults to generic."`
	Host        *string   `json:"host,omitempty" doc:"A host name or IP address, with no scheme, port or path. A custom upstream needs one."`
	Port        *int      `json:"port,omitempty" doc:"A custom upstream defaults to 443 with TLS and 80 without."`
	TLS         *bool     `json:"tls,omitempty" doc:"A custom upstream defaults to true."`
	ServerName  *string   `json:"server_name,omitempty" doc:"The TLS server name, when it is not the host."`
	Paths       *[]string `json:"path_allowlist,omitempty" doc:"The rules a request must match, as METHOD /path. A custom upstream needs at least one; * /** allows everything."`
	InjectUsage *bool     `json:"inject_usage,omitempty" doc:"Only for the openai profile."`
	Auth        *Auth     `json:"auth,omitempty" doc:"A custom upstream defaults to Authorization: Bearer {key}, or x-api-key: {key} on the anthropic profile."`
	SDKEnv      *string   `json:"sdk_env,omitempty"`
	SDKBasePath *string   `json:"sdk_base_path,omitempty"`
}

// AddUpstreamRequest is a new upstream, from a preset or built by hand.
type AddUpstreamRequest struct {
	Name   string `json:"name,omitempty" doc:"1 to 63 of a-z, 0-9, '.', '_' and '-', starting with a letter or digit. With a preset, it defaults to the preset's name."`
	Preset string `json:"preset,omitempty" doc:"The preset to copy. Absent to build a custom upstream from the settings."`
	UpstreamSettings
}

// UpstreamUpdate changes an upstream.
type UpstreamUpdate struct {
	Ref  string  `json:"-" path:"ref" doc:"The upstream's name or id."`
	Name *string `json:"name,omitempty" doc:"A new name."`
	UpstreamSettings
}

// checkName is the rule for the names of upstreams and credentials, which is also
// the rule for admin tokens.
func checkName(kind, name string) error {
	if !tokenName.MatchString(name) {
		return errorf(CodeInvalid, "%s name %q: use 1 to 63 of a-z, 0-9, '.', '_' and '-', starting with a letter or digit", kind, name)
	}
	return nil
}

// apply sets on spec the settings that are given.
func (u UpstreamSettings) apply(spec *registry.Spec) {
	set(&spec.Profile, u.Profile)
	set(&spec.Host, u.Host)
	set(&spec.Port, u.Port)
	set(&spec.TLS, u.TLS)
	set(&spec.ServerName, u.ServerName)
	if u.Paths != nil {
		spec.Paths = slices.Clone(*u.Paths)
	}
	set(&spec.InjectUsage, u.InjectUsage)
	if u.Auth != nil {
		spec.Auth = registry.Auth(*u.Auth)
	}
	set(&spec.SDKEnv, u.SDKEnv)
	set(&spec.SDKBasePath, u.SDKBasePath)
}

func set[T any](dst *T, v *T) {
	if v != nil {
		*dst = *v
	}
}

func (s *Service) preset(name string) (registry.Preset, bool) {
	i := slices.IndexFunc(s.presets, func(p registry.Preset) bool { return p.Name == name })
	if i < 0 {
		return registry.Preset{}, false
	}
	return s.presets[i], true
}

func (s *Service) presetNames() string {
	names := make([]string, 0, len(s.presets))
	for _, p := range s.presets {
		names = append(names, p.Name)
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
}

// newSpec is the spec req describes, valid, and the preset version it copied.
func (s *Service) newSpec(req *AddUpstreamRequest) (registry.Spec, int, error) {
	var spec registry.Spec
	version := 0
	if req.Preset != "" {
		p, ok := s.preset(req.Preset)
		if !ok {
			return spec, 0, errorf(CodeInvalid, "no preset %q; the presets are %s", req.Preset, s.presetNames())
		}
		spec, version = p.Spec, p.Version
		if req.Name == "" {
			req.Name = p.Name
		}
	} else {
		spec = registry.Spec{Profile: registry.ProfileGeneric, TLS: true,
			Auth: registry.Auth{Style: registry.AuthHeader, Header: "Authorization: Bearer {key}"}}
	}
	req.UpstreamSettings.apply(&spec)
	if req.Preset == "" {
		if req.Auth == nil && spec.Profile == registry.ProfileAnthropic {
			spec.Auth.Header = "x-api-key: {key}"
		}
		if spec.Port == 0 {
			spec.Port = 80
			if spec.TLS {
				spec.Port = 443
			}
		}
	}
	if err := spec.Validate(); err != nil {
		return spec, 0, errorf(CodeInvalid, "%v", err)
	}
	return spec, version, nil
}

// AddUpstream adds an upstream, copying its preset if it names one.
func (s *Service) AddUpstream(ctx context.Context, req AddUpstreamRequest) (Upstream, error) {
	spec, version, err := s.newSpec(&req)
	if err != nil {
		return Upstream{}, err
	}
	if err := checkName("upstream", req.Name); err != nil {
		return Upstream{}, err
	}
	var out Upstream
	fx := &effects{}
	err = s.commandWith(ctx, fx, func(tx *store.Tx, now time.Time) ([]audit, error) {
		row := store.Upstream{Name: req.Name, Preset: req.Preset, PresetVersion: version, PriceMultiplierPPM: defaultPriceMultiplierPPM, CreatedAt: now, UpdatedAt: now}
		if err := writeSpec(&row, spec); err != nil {
			return nil, err
		}
		u, err := tx.CreateUpstream(row)
		if err != nil {
			return nil, fromStore(err, "upstream "+req.Name)
		}
		pub, err := publishedUpstream(u)
		if err != nil {
			return nil, err
		}
		fx.change.Upstreams = append(fx.change.Upstreams, pub)
		out = s.upstreamDTO(pub, u)
		return []audit{{action: "upstream.create", kind: "upstream", id: u.ID, after: out}}, nil
	})
	return out, err
}

// defaultPriceMultiplierPPM is ×1, in parts per million.
const defaultPriceMultiplierPPM = 1_000_000

// ListUpstreams returns every upstream with its credentials, by name.
func (s *Service) ListUpstreams(ctx context.Context) (UpstreamList, error) {
	out := UpstreamList{Upstreams: []Upstream{}}
	err := s.read(ctx, func(tx *store.Tx) error {
		rows, err := tx.Upstreams()
		if err != nil {
			return err
		}
		creds, err := tx.Credentials()
		if err != nil {
			return err
		}
		for _, u := range rows {
			dto, err := s.fullUpstream(u, creds)
			if err != nil {
				return err
			}
			out.Upstreams = append(out.Upstreams, dto)
		}
		return nil
	})
	return out, err
}

// GetUpstream returns one upstream with its credentials.
func (s *Service) GetUpstream(ctx context.Context, req UpstreamRef) (Upstream, error) {
	var out Upstream
	err := s.read(ctx, func(tx *store.Tx) error {
		u, err := upstreamByRef(tx, req.Ref)
		if err != nil {
			return err
		}
		creds, err := tx.Credentials()
		if err != nil {
			return err
		}
		out, err = s.fullUpstream(u, creds)
		return err
	})
	return out, err
}

// UpdateUpstream changes what req gives. It never touches the preset's record:
// a later sync shows the operator what it would undo.
func (s *Service) UpdateUpstream(ctx context.Context, req UpstreamUpdate) (Upstream, error) {
	if req.Name != nil {
		if err := checkName("upstream", *req.Name); err != nil {
			return Upstream{}, err
		}
	}
	var out Upstream
	fx := &effects{}
	err := s.commandWith(ctx, fx, func(tx *store.Tx, now time.Time) ([]audit, error) {
		before, err := upstreamByRef(tx, req.Ref)
		if err != nil {
			return nil, err
		}
		pub, err := publishedUpstream(before)
		if err != nil {
			return nil, err
		}
		prior := s.upstreamDTO(pub, before)
		spec := pub.Spec
		req.UpstreamSettings.apply(&spec)
		if err := spec.Validate(); err != nil {
			return nil, errorf(CodeInvalid, "%v", err)
		}
		row := before
		set(&row.Name, req.Name)
		row.UpdatedAt = now
		if err := writeSpec(&row, spec); err != nil {
			return nil, err
		}
		if sameUpstream(before, row) {
			creds, err := tx.Credentials()
			if err != nil {
				return nil, err
			}
			out, err = s.fullUpstream(before, creds)
			return nil, err
		}
		u, err := tx.UpdateUpstream(row)
		if err != nil {
			return nil, fromStore(err, "upstream "+row.Name)
		}
		pub, err = publishedUpstream(u)
		if err != nil {
			return nil, err
		}
		fx.change.Upstreams = append(fx.change.Upstreams, pub)
		after := s.upstreamDTO(pub, u)
		creds, err := tx.Credentials()
		if err != nil {
			return nil, err
		}
		if out, err = s.fullUpstream(u, creds); err != nil {
			return nil, err
		}
		return []audit{{action: "upstream.update", kind: "upstream", id: u.ID, before: prior, after: after}}, nil
	})
	return out, err
}

// RemoveUpstream deletes an upstream and its credentials, unless a virtual key
// that is not revoked depends on one of them.
func (s *Service) RemoveUpstream(ctx context.Context, req UpstreamRef) (Upstream, error) {
	var out Upstream
	fx := &effects{}
	err := s.commandWith(ctx, fx, func(tx *store.Tx, now time.Time) ([]audit, error) {
		u, err := upstreamByRef(tx, req.Ref)
		if err != nil {
			return nil, err
		}
		all, err := tx.Credentials()
		if err != nil {
			return nil, err
		}
		if out, err = s.fullUpstream(u, all); err != nil {
			return nil, err
		}
		var entries []audit
		for _, c := range out.Credentials {
			if err := refuseIfKeysDepend(tx, c.ID, "upstream "+u.Name); err != nil {
				return nil, err
			}
			if err := tx.DeleteCredential(c.ID, now); err != nil {
				return nil, fromStore(err, "credential "+c.Name)
			}
			fx.change.DeletedCredentials = append(fx.change.DeletedCredentials, c.ID)
			fx.wipe = append(fx.wipe, c.ID)
			entries = append(entries, audit{action: "credential.delete", kind: "credential", id: c.ID, before: c})
		}
		if err := tx.DeleteUpstream(u.ID, now); err != nil {
			return nil, fromStore(err, "upstream "+req.Ref)
		}
		fx.change.DeletedUpstreams = append(fx.change.DeletedUpstreams, u.ID)
		before := out
		before.Credentials = nil
		return append(entries, audit{action: "upstream.delete", kind: "upstream", id: u.ID, before: before}), nil
	})
	return out, err
}

// upstreamByRef finds a live upstream by id or by name.
func upstreamByRef(tx *store.Tx, ref string) (store.Upstream, error) {
	u, err := tx.Upstream(ref)
	if err == nil {
		return u, nil
	}
	if !isNotFound(err) {
		return store.Upstream{}, err
	}
	u, err = tx.UpstreamByName(ref)
	if err != nil {
		return store.Upstream{}, fromStore(err, "upstream "+ref)
	}
	return u, nil
}

// writeSpec stores spec on row.
func writeSpec(row *store.Upstream, spec registry.Spec) error {
	paths, err := json.Marshal(spec.Paths)
	if err != nil {
		return fmt.Errorf("admin: marshal paths: %w", err)
	}
	auth, err := json.Marshal(Auth(spec.Auth))
	if err != nil {
		return fmt.Errorf("admin: marshal auth: %w", err)
	}
	row.Profile, row.Host, row.Port, row.TLS, row.ServerName = spec.Profile, spec.Host, spec.Port, spec.TLS, spec.ServerName
	row.PathAllowlistJSON, row.InjectUsage, row.AuthJSON = string(paths), spec.InjectUsage, string(auth)
	row.SDKEnv, row.SDKBasePath = spec.SDKEnv, spec.SDKBasePath
	return nil
}

// publishedUpstream is the row as the request path reads it.
func publishedUpstream(u store.Upstream) (registry.Upstream, error) {
	spec := registry.Spec{Profile: u.Profile, Host: u.Host, Port: u.Port, TLS: u.TLS, ServerName: u.ServerName,
		InjectUsage: u.InjectUsage, SDKEnv: u.SDKEnv, SDKBasePath: u.SDKBasePath}
	if err := json.Unmarshal([]byte(u.PathAllowlistJSON), &spec.Paths); err != nil {
		return registry.Upstream{}, fmt.Errorf("admin: upstream %s: path allowlist: %w", u.ID, err)
	}
	var auth Auth
	if err := json.Unmarshal([]byte(u.AuthJSON), &auth); err != nil {
		return registry.Upstream{}, fmt.Errorf("admin: upstream %s: auth: %w", u.ID, err)
	}
	spec.Auth = registry.Auth(auth)
	return registry.Upstream{ID: u.ID, Name: u.Name, Preset: u.Preset, PresetVersion: u.PresetVersion, Spec: spec,
		PriceMultiplierPPM: u.PriceMultiplierPPM, Generation: u.Generation}, nil
}

// sameUpstream reports whether two rows differ in nothing an operator set.
func sameUpstream(a, b store.Upstream) bool {
	a.UpdatedAt, b.UpdatedAt = time.Time{}, time.Time{}
	return a == b
}

func (s *Service) upstreamDTO(p registry.Upstream, u store.Upstream) Upstream {
	out := Upstream{
		ID: p.ID, Name: p.Name, Preset: p.Preset, PresetVersion: p.PresetVersion,
		Profile: p.Profile, Host: p.Host, Port: p.Port, TLS: p.TLS, ServerName: p.ServerName,
		Paths: slices.Clone(p.Paths), InjectUsage: p.InjectUsage, Auth: Auth(p.Auth),
		SDKEnv: p.SDKEnv, SDKBasePath: p.SDKBasePath, CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt,
	}
	if preset, ok := s.preset(p.Preset); ok && preset.Version > p.PresetVersion {
		out.UpdateAvailable = true
	}
	return out
}

// fullUpstream is the row as the API shows it, with its credentials, which
// come from creds.
func (s *Service) fullUpstream(u store.Upstream, creds []store.Credential) (Upstream, error) {
	p, err := publishedUpstream(u)
	if err != nil {
		return Upstream{}, err
	}
	out := s.upstreamDTO(p, u)
	for _, c := range creds {
		if c.UpstreamID == u.ID {
			out.Credentials = append(out.Credentials, credentialDTO(c, u.Name))
		}
	}
	return out, nil
}
