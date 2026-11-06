package admin

import (
	"context"
	"slices"
	"strconv"
	"time"

	"github.com/kirank55/kman/internal/registry"
	"github.com/kirank55/kman/internal/store"
)

// UpstreamChange is one difference a sync would apply. For the path allowlist
// there is one per rule: To alone for a rule added, From alone for one removed.
type UpstreamChange struct {
	Field string `json:"field" doc:"The upstream's field, by its name in the API."`
	From  string `json:"from,omitempty" doc:"What it holds now. Absent if nothing."`
	To    string `json:"to,omitempty" doc:"What the sync would set. Absent if nothing."`
}

// UpstreamSyncPlan is how an upstream differs from the preset it came from.
type UpstreamSyncPlan struct {
	Upstream    string           `json:"upstream"`
	Preset      string           `json:"preset"`
	FromVersion int              `json:"from_version" doc:"The version of the preset the upstream was last copied from."`
	ToVersion   int              `json:"to_version" doc:"The version of the preset this kman holds."`
	UpToDate    bool             `json:"up_to_date" doc:"Whether this kman holds no newer version, so there is nothing to sync."`
	Generation  int64            `json:"generation" doc:"The upstream's generation: pass it to the sync, which refuses if the upstream has changed since."`
	Changes     []UpstreamChange `json:"changes" doc:"What the sync would change, which undoes any edit made since the last copy. Empty when up to date, and possibly when only the version moves."`
}

// UpstreamSyncRequest applies a plan.
type UpstreamSyncRequest struct {
	Ref        string `json:"-" path:"ref" doc:"The upstream's name or id."`
	ToVersion  int    `json:"to_version" doc:"The plan's to_version."`
	Generation int64  `json:"generation" doc:"The plan's generation."`
}

// PlanUpstreamSync shows how an upstream differs from the preset it came from,
// changing nothing.
func (s *Service) PlanUpstreamSync(ctx context.Context, req UpstreamRef) (UpstreamSyncPlan, error) {
	var out UpstreamSyncPlan
	err := s.read(ctx, func(tx *store.Tx) error {
		u, err := upstreamByRef(tx, req.Ref)
		if err != nil {
			return err
		}
		p, err := publishedUpstream(u)
		if err != nil {
			return err
		}
		preset, err := s.presetOf(p)
		if err != nil {
			return err
		}
		out = UpstreamSyncPlan{Upstream: u.Name, Preset: preset.Name, FromVersion: u.PresetVersion, ToVersion: preset.Version,
			Generation: u.Generation, Changes: []UpstreamChange{}, UpToDate: preset.Version <= u.PresetVersion}
		if !out.UpToDate {
			out.Changes = diffSpecs(p.Spec, preset.Spec)
		}
		return nil
	})
	return out, err
}

// SyncUpstream copies the newer preset into the upstream, which keeps its id,
// name and credentials. It applies what the plan showed, or nothing: it
// refuses if the preset's version or the upstream has moved since.
func (s *Service) SyncUpstream(ctx context.Context, req UpstreamSyncRequest) (Upstream, error) {
	var out Upstream
	fx := &effects{}
	err := s.commandWith(ctx, fx, func(tx *store.Tx, now time.Time) ([]audit, error) {
		before, err := upstreamByRef(tx, req.Ref)
		if err != nil {
			return nil, err
		}
		p, err := publishedUpstream(before)
		if err != nil {
			return nil, err
		}
		preset, err := s.presetOf(p)
		if err != nil {
			return nil, err
		}
		switch {
		case preset.Version <= before.PresetVersion:
			return nil, errorf(CodeConflict, "upstream %s is at version %d of preset %s, and this kman holds nothing newer", before.Name, before.PresetVersion, preset.Name)
		case preset.Version != req.ToVersion:
			return nil, errorf(CodeConflict, "this kman holds version %d of preset %s, not %d: run the sync again", preset.Version, preset.Name, req.ToVersion)
		case before.Generation != req.Generation:
			return nil, errorf(CodeConflict, "upstream %s has changed since the plan: run the sync again", before.Name)
		}
		row := before
		row.PresetVersion, row.UpdatedAt = preset.Version, now
		if err := writeSpec(&row, preset.Spec); err != nil {
			return nil, err
		}
		u, err := tx.UpdateUpstream(row)
		if err != nil {
			return nil, fromStore(err, "upstream "+row.Name)
		}
		pub, err := publishedUpstream(u)
		if err != nil {
			return nil, err
		}
		fx.change.Upstreams = append(fx.change.Upstreams, pub)
		creds, err := tx.Credentials()
		if err != nil {
			return nil, err
		}
		if out, err = s.fullUpstream(u, creds); err != nil {
			return nil, err
		}
		return []audit{{action: "upstream.sync", kind: "upstream", id: u.ID, before: s.upstreamDTO(p, before), after: s.upstreamDTO(pub, u)}}, nil
	})
	return out, err
}

// presetOf is the preset an upstream came from, as this kman holds it.
func (s *Service) presetOf(p registry.Upstream) (registry.Preset, error) {
	if p.Preset == "" {
		return registry.Preset{}, errorf(CodeInvalid, "upstream %s was built by hand: it has no preset to sync with", p.Name)
	}
	preset, ok := s.preset(p.Preset)
	if !ok {
		return registry.Preset{}, errorf(CodeConflict, "upstream %s came from preset %s, which this kman does not hold", p.Name, p.Preset)
	}
	return preset, nil
}

// diffSpecs lists what changes from a to b, one field at a time.
func diffSpecs(a, b registry.Spec) []UpstreamChange {
	out := []UpstreamChange{}
	field := func(name, from, to string) {
		if from != to {
			out = append(out, UpstreamChange{Field: name, From: from, To: to})
		}
	}
	field("profile", a.Profile, b.Profile)
	field("host", a.Host, b.Host)
	field("port", strconv.Itoa(a.Port), strconv.Itoa(b.Port))
	field("tls", strconv.FormatBool(a.TLS), strconv.FormatBool(b.TLS))
	field("server_name", a.ServerName, b.ServerName)
	for _, rule := range a.Paths {
		if !slices.Contains(b.Paths, rule) {
			out = append(out, UpstreamChange{Field: "path_allowlist", From: rule})
		}
	}
	for _, rule := range b.Paths {
		if !slices.Contains(a.Paths, rule) {
			out = append(out, UpstreamChange{Field: "path_allowlist", To: rule})
		}
	}
	field("inject_usage", strconv.FormatBool(a.InjectUsage), strconv.FormatBool(b.InjectUsage))
	field("auth", authText(a.Auth), authText(b.Auth))
	field("sdk_env", a.SDKEnv, b.SDKEnv)
	field("sdk_base_path", a.SDKBasePath, b.SDKBasePath)
	return out
}

// authText is an auth shape as a diff shows it.
func authText(a registry.Auth) string {
	switch {
	case a.Style == registry.AuthHeader:
		return a.Header
	case a.BasicUsername != "":
		return "basic, username " + a.BasicUsername
	}
	return "basic, secret is user:password"
}
