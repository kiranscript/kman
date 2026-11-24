package cli

import (
	"encoding/json"
	"time"

	"github.com/kirank55/kman/internal/admin"
)

// demoData is the sample data kman tui --demo browses instead of a server's,
// for trying the terminal view without an install. It touches no files.
type demoData struct {
	keys      []admin.Key
	exposure  admin.Exposure
	audit     []admin.AuditEntry
	upstreams []admin.Upstream
	places    map[string][]admin.Place // by key id

	// clients is each key's client addresses, as if expose_client_addresses were on.
	clients map[string]admin.KeyClients
}

func newDemoData(now time.Time) *demoData {
	day := 24 * time.Hour
	spend := func(today, month, cap int64, reqs int64) *admin.KeySpend {
		return &admin.KeySpend{
			Today: admin.SpendPeriod{Period: now.UTC().Format("2006-01-02"), CostMicro: today, Requests: reqs / 10},
			Month: admin.SpendPeriod{Period: now.UTC().Format("2006-01"), CostMicro: month, CapMicro: cap, Requests: reqs},
		}
	}
	policy := json.RawMessage(`{"allowed_cidrs":["10.0.0.0/8"]}`)
	empty := json.RawMessage(`{}`)
	key := func(id, name, upstream, cred, status string, ttl time.Duration, tags []string, p json.RawMessage, s *admin.KeySpend) admin.Key {
		return admin.Key{ID: id, Name: name, Upstream: upstream, Credential: cred, Status: status,
			Expired: ttl < 0, Tags: tags, Policy: p, PolicyVersion: 1,
			ExpiresAt: now.Add(ttl), CreatedAt: now.Add(-30 * day), Spend: s}
	}
	d := &demoData{}
	d.keys = []admin.Key{
		key("k_9f2a1c", "ci-runner", "openai", "main", admin.KeyActive, 60*day, []string{"ci", "team:platform"}, policy, spend(1_820_000, 41_300_000, 100_000_000, 2140)),
		key("k_3b7d40", "support-bot", "anthropic", "prod", admin.KeyActive, 12*day, []string{"support"}, policy, spend(6_450_000, 212_900_000, 250_000_000, 9310)),
		key("k_c81e55", "dev-laptop", "openai", "main", admin.KeyActive, 90*day, nil, empty, spend(210_000, 3_900_000, 0, 87)),
		key("k_0a44f9", "staging-eval", "anthropic", "staging", admin.KeyDisabled, 30*day, []string{"eval"}, policy, spend(0, 15_000_000, 50_000_000, 420)),
		key("k_77d2be", "old-notebook", "openai", "main", admin.KeyActive, -3*day, []string{"legacy"}, policy, spend(0, 0, 0, 0)),
		key("k_e15c08", "leaked-demo", "openai", "main", admin.KeyRevoked, 45*day, []string{"incident"}, policy, spend(0, 880_000, 0, 31)),
	}
	d.keys = append(d.keys[:5:5], key("k_5d19e3", "batch-export", "anthropic", "prod", admin.KeyActive, 20*day, []string{"batch"}, policy, spend(0, 0, 0, 0)), d.keys[5])
	client := func(addr string, reqs, blocked int64, ago time.Duration, country string, asn int64) admin.KeyClient {
		return admin.KeyClient{Address: addr, Requests: reqs, Blocked: blocked, FirstSeen: now.Add(-ago - 20*day), LastSeen: now.Add(-ago), Country: country, ASN: asn}
	}
	d.clients = map[string]admin.KeyClients{
		"k_9f2a1c": {KeyID: "k_9f2a1c", Total: 3, Clients: []admin.KeyClient{
			client("10.0.4.17", 1920, 0, 2*time.Minute, "", 0),
			client("10.0.4.18", 205, 0, 3*time.Hour, "", 0),
			client("203.0.113.44", 15, 15, 26*time.Hour, "DE", 3320),
		}},
		"k_0a44f9": {KeyID: "k_0a44f9", Total: 1, Clients: []admin.KeyClient{
			client("185.220.101.9", 0, 57, 40*time.Minute, "DE", 60729),
		}},
		"k_3b7d40": {KeyID: "k_3b7d40", Total: 2, Clients: []admin.KeyClient{
			client("198.51.100.23", 8800, 0, time.Minute, "US", 16509),
			client("2001:db8:85a3::8a2e:370:7334", 510, 2, 5*time.Hour, "NL", 1136),
		}},
	}
	d.exposure = admin.Exposure{KeysChecked: 4, Findings: []admin.ExposureFinding{
		{KeyID: "k_c81e55", Name: "dev-laptop", Rule: "open_to_every_address",
			Message: "It is open to every address: its policy has no allowed_cidrs and no allowed_countries, so anyone who holds it can use it from anywhere. Replace <network/bits> with the network it is used from.",
			Fix:     "kman key update dev-laptop --policy '{\"allowed_cidrs\":[\"<network/bits>\"]}'"},
		{KeyID: "k_5d19e3", Name: "batch-export", Rule: "unused",
			Message: "It has never been used, and it was created 30 days ago. Disable it, and enable it again if something needs it.",
			Fix:     "kman key disable batch-export"},
	}}
	e := func(id int64, ago time.Duration, actor, action, kind, target string) admin.AuditEntry {
		return admin.AuditEntry{ID: id, Time: now.Add(-ago), Actor: actor, Action: action, TargetKind: kind, TargetID: target}
	}
	// Oldest first, as the admin API returns them.
	d.audit = []admin.AuditEntry{
		e(1, 30*day, "system:init", "admin_token.create", "admin_token", "owner"),
		e(2, 30*day, "owner", "upstream.add", "upstream", "openai"),
		e(3, 30*day, "owner", "upstream.add", "upstream", "anthropic"),
		e(4, 29*day, "owner", "key.create", "key", "k_9f2a1c"),
		e(5, 20*day, "owner", "key.create", "key", "k_3b7d40"),
		e(6, 9*day, "owner", "key.disable", "key", "k_0a44f9"),
		e(7, 5*day, "owner", "key.revoke", "key", "k_e15c08"),
		e(8, 2*time.Hour, "owner", "settings.set", "settings", "default_key_ttl"),
	}
	up := func(id, name, preset, profile, host string, paths ...string) admin.Upstream {
		return admin.Upstream{ID: id, Name: name, Preset: preset, Profile: profile, Host: host, Port: 443, TLS: true,
			Paths: paths, PriceMultiplierPPM: 1_000_000, CreatedAt: now.Add(-30 * day), UpdatedAt: now.Add(-30 * day)}
	}
	place := func(kind, value, label string, first, last time.Duration, fwd, refused int64) admin.Place {
		return admin.Place{Kind: kind, Value: value, Label: label, FirstSeen: now.Add(-first), LastSeen: now.Add(-last), Requests: fwd, Refused: refused}
	}
	d.places = map[string][]admin.Place{
		"k_9f2a1c": {
			place("client", "openai-python/3", "", 29*day, 2*time.Minute, 2125, 0),
			place("network", "10.0.4.0/24", "", 29*day, 2*time.Minute, 2125, 0),
			place("client", "curl/8", "", 29*day, 26*time.Hour, 0, 15),
			place("network", "203.0.113.0/24", "", 29*day, 26*time.Hour, 0, 15),
			place("asn", "3320", "Deutsche Telekom AG", 29*day, 26*time.Hour, 0, 15),
			place("country", "DE", "", 29*day, 26*time.Hour, 0, 15),
		},
		"k_0a44f9": {
			place("client", "curl/8", "", 2*day, 40*time.Minute, 0, 57),
			place("network", "185.220.101.0/24", "", 2*day, 40*time.Minute, 0, 57),
			place("asn", "60729", "Stiftung Erneuerbare Freiheit", 2*day, 40*time.Minute, 0, 57),
			place("country", "DE", "", 2*day, 40*time.Minute, 0, 57),
			place("client", "anthropic-python/2", "", 25*day, 9*day, 420, 0),
			place("network", "10.20.4.0/24", "", 25*day, 9*day, 420, 0),
			place("asn", "16509", "Amazon.com, Inc.", 25*day, 9*day, 420, 0),
			place("country", "US", "", 25*day, 9*day, 420, 0),
		},
	}
	d.upstreams = []admin.Upstream{
		up("u_anthropic", "anthropic", "anthropic", "anthropic", "api.anthropic.com", "POST /v1/messages", "POST /v1/messages/count_tokens"),
		up("u_openai", "openai", "openai", "openai", "api.openai.com", "POST /v1/chat/completions", "POST /v1/responses", "GET /v1/models"),
	}
	return d
}
