package admin

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/kiranscript/kman/internal/events"
	"github.com/kiranscript/kman/internal/store"
)

// Limits on known sources, so that they cannot silence everything: a network no
// wider than an IPv4 /16 or an IPv6 /32 (the canary ignore list's bounds), a
// client that says what it is, and at most 200 for each scope.
const (
	minSourceBits4   = 16
	minSourceBits6   = 32
	maxSourceLabel   = 64
	maxKnownPerScope = 200
)

// KnownSource is a place an operator has said is legitimate: it never counts as
// a new location to the location rules, and never silences a canary.
type KnownSource struct {
	ID        int64     `json:"id" doc:"A plain integer."`
	Scope     string    `json:"scope,omitempty" doc:"The key id it applies to. Empty: every key."`
	KeyName   string    `json:"key_name,omitempty"`
	Kind      string    `json:"kind" doc:"cidr, country, asn or client."`
	Value     string    `json:"value" doc:"A network in CIDR form, an ISO 3166-1 country code, an AS number without the AS, or a client (a User-Agent reduced to a product and major version, such as curl/8)."`
	Label     string    `json:"label" doc:"What the operator calls it."`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}

// KnownSourceQuery picks known sources.
type KnownSourceQuery struct {
	Key string `json:"-" query:"key" doc:"Only those that apply to this key, by key id: the install-wide ones and its own."`
}

// KnownSourceList is the known sources, install-wide first.
type KnownSourceList struct {
	KnownSources []KnownSource `json:"known_sources"`
}

// KnownSourceRef names a known source.
type KnownSourceRef struct {
	ID int64 `json:"-" path:"id" doc:"The known source's id."`
}

// AddKnownSourceRequest is a new known source.
type AddKnownSourceRequest struct {
	Kind  string `json:"kind" doc:"cidr, country, asn or client."`
	Value string `json:"value" doc:"cidr: a network no wider than an IPv4 /16 or an IPv6 /32, or one address. country: two capital letters. asn: a number, with or without AS. client: a product and major version as events record it, such as curl/8; none and unknown are refused, since they would silence every request that does not say what it is."`
	Label string `json:"label" doc:"What it is, 1 to 64 characters with no control characters."`
	Key   string `json:"key,omitempty" doc:"The key id it applies to. Absent: every key. A canary has none."`
}

func checkSourceLabel(s string) error {
	switch {
	case strings.TrimSpace(s) == "":
		return errorf(CodeInvalid, "a known source needs a label: say what it is")
	case len([]rune(s)) > maxSourceLabel:
		return errorf(CodeInvalid, "the label is over %d characters", maxSourceLabel)
	case strings.ContainsFunc(s, unicode.IsControl):
		return errorf(CodeInvalid, "the label holds a control character, such as a line break")
	}
	return nil
}

// cleanLabel cuts a generated label to the limit.
func cleanLabel(s string) string {
	if r := []rune(s); len(r) > maxSourceLabel {
		return string(r[:maxSourceLabel-1]) + "…"
	}
	return s
}

// normalizeSource checks a known source's value and returns it as it is stored.
func normalizeSource(kind, value string) (string, error) {
	v := strings.TrimSpace(value)
	switch kind {
	case store.SourceCIDR:
		if strings.Contains(v, "/") {
			p, err := netip.ParsePrefix(v)
			if err != nil || p.Addr().Zone() != "" {
				return "", errorf(CodeInvalid, "%q is not a network such as 203.0.113.0/24", value)
			}
			if p.Addr().Is4In6() {
				return "", errorf(CodeInvalid, "%q is an IPv4 network written as IPv6: write it as IPv4", value)
			}
			if (p.Addr().Is4() && p.Bits() < minSourceBits4) || (p.Addr().Is6() && p.Bits() < minSourceBits6) {
				return "", errorf(CodeInvalid, "%q is too wide: a known source that held that many addresses would silence the location rules (no wider than an IPv4 /%d or an IPv6 /%d)",
					value, minSourceBits4, minSourceBits6)
			}
			return p.Masked().String(), nil
		}
		a, err := netip.ParseAddr(v)
		if err != nil || a.Zone() != "" {
			return "", errorf(CodeInvalid, "%q is not an address or a network such as 203.0.113.0/24", value)
		}
		a = a.Unmap()
		return netip.PrefixFrom(a, a.BitLen()).String(), nil
	case store.SourceCountry:
		if len(v) != 2 || v[0] < 'A' || v[0] > 'Z' || v[1] < 'A' || v[1] > 'Z' {
			return "", errorf(CodeInvalid, "%q is not an ISO 3166-1 alpha-2 country code in capitals, such as DE", value)
		}
		return v, nil
	case store.SourceASN:
		n, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(v), "as"), 10, 32)
		if err != nil || n == 0 {
			return "", errorf(CodeInvalid, "%q is not an AS number from 1 to 4294967295, such as 3320 or AS3320", value)
		}
		return strconv.FormatUint(n, 10), nil
	case store.SourceClient:
		v = strings.ToLower(v)
		if v == "none" || v == "unknown" {
			return "", errorf(CodeInvalid, "the client %q is what a request that does not say what it is is given: a known source for it would silence every such request", v)
		}
		if !events.IsClient(v) {
			return "", errorf(CodeInvalid, "%q is not a client as kman records one: a product and a major version, such as curl/8", value)
		}
		return v, nil
	}
	return "", errorf(CodeInvalid, "kind %q is not one of cidr, country, asn or client", kind)
}

func knownSourceDTO(tx *store.Tx, ks store.KnownSource) (KnownSource, error) {
	out := KnownSource{ID: ks.ID, Scope: ks.Scope, Kind: ks.Kind, Value: ks.Value, Label: ks.Label, CreatedBy: ks.CreatedBy, CreatedAt: ks.CreatedAt}
	if ks.Scope != "" {
		k, err := tx.Key(ks.Scope)
		switch {
		case err == nil:
			out.KeyName = k.Name
		case !errors.Is(err, store.ErrNotFound):
			return KnownSource{}, err
		}
	}
	return out, nil
}

// createKnownSource adds a known source in a transaction, within the scope's
// bound, and returns its audit entry (carrying incident, 0 for none).
func createKnownSource(tx *store.Tx, ks store.KnownSource, incident int64) (store.KnownSource, audit, error) {
	n, err := tx.CountKnownSources(ks.Scope)
	if err != nil {
		return store.KnownSource{}, audit{}, err
	}
	if n >= maxKnownPerScope {
		return store.KnownSource{}, audit{}, errorf(CodeConflict, "there are already %d known sources for this scope, the most there can be", maxKnownPerScope)
	}
	created, err := tx.CreateKnownSource(ks)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			return store.KnownSource{}, audit{}, errorf(CodeConflict, "%s %s is already a known source for this scope", ks.Kind, ks.Value)
		}
		return store.KnownSource{}, audit{}, err
	}
	return created, audit{action: "known_source.create", kind: "known_source", id: strconv.FormatInt(created.ID, 10), incident: incident}, nil
}

// ListKnownSources returns the known sources, the install-wide ones first.
func (s *Service) ListKnownSources(ctx context.Context, req KnownSourceQuery) (KnownSourceList, error) {
	out := KnownSourceList{KnownSources: []KnownSource{}}
	err := s.read(ctx, func(tx *store.Tx) error {
		var rows []store.KnownSource
		var err error
		if req.Key != "" {
			if _, err := tx.Key(req.Key); err != nil {
				return fromStore(err, "key "+req.Key)
			}
			rows, err = tx.KnownSourcesFor(req.Key)
		} else {
			rows, err = tx.KnownSources()
		}
		if err != nil {
			return err
		}
		for _, r := range rows {
			dto, err := knownSourceDTO(tx, r)
			if err != nil {
				return err
			}
			out.KnownSources = append(out.KnownSources, dto)
		}
		return nil
	})
	return out, err
}

// AddKnownSource adds a labelled CIDR, country, ASN or client the operator says
// is legitimate, for every key or one. It is audited. A canary takes none.
func (s *Service) AddKnownSource(ctx context.Context, req AddKnownSourceRequest) (KnownSource, error) {
	if err := checkSourceLabel(req.Label); err != nil {
		return KnownSource{}, err
	}
	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	value, err := normalizeSource(kind, req.Value)
	if err != nil {
		return KnownSource{}, err
	}
	c, err := caller(ctx)
	if err != nil {
		return KnownSource{}, err
	}
	var out KnownSource
	err = s.command(ctx, func(tx *store.Tx, now time.Time) ([]audit, error) {
		if req.Key != "" {
			k, err := tx.Key(req.Key)
			if err != nil {
				return nil, fromStore(err, "key "+req.Key)
			}
			if k.Kind == store.KeyCanary {
				return nil, errorf(CodeConflict, "key %s is a canary: no known source applies to a canary, its own ignore list is the only thing that silences it", req.Key)
			}
		}
		ks, entry, err := createKnownSource(tx, store.KnownSource{Scope: req.Key, Kind: kind, Value: value, Label: strings.TrimSpace(req.Label), CreatedBy: c.Actor, CreatedAt: now}, 0)
		if err != nil {
			return nil, err
		}
		if out, err = knownSourceDTO(tx, ks); err != nil {
			return nil, err
		}
		entry.after = out
		return []audit{entry}, nil
	})
	return out, err
}

// RemoveKnownSource removes a known source, and audits what it was.
func (s *Service) RemoveKnownSource(ctx context.Context, req KnownSourceRef) (KnownSource, error) {
	var out KnownSource
	err := s.command(ctx, func(tx *store.Tx, now time.Time) ([]audit, error) {
		ks, err := tx.KnownSource(req.ID)
		if err != nil {
			return nil, fromStore(err, fmt.Sprintf("known source %d", req.ID))
		}
		if out, err = knownSourceDTO(tx, ks); err != nil {
			return nil, err
		}
		if err := tx.DeleteKnownSource(req.ID); err != nil {
			return nil, fromStore(err, fmt.Sprintf("known source %d", req.ID))
		}
		return []audit{{action: "known_source.delete", kind: "known_source", id: strconv.FormatInt(req.ID, 10), before: out}}, nil
	})
	return out, err
}
