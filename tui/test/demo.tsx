// A demo of the TUI against the in-memory fake admin API: no kman server needed.
//   cd tui && npx tsx test/demo.tsx
//
// It tells a leak: prod-api, locked to its own network, turned up from an exit
// node abroad and was refused; a canary planted in the wiki was used; a
// contractor's key works from any address; a scratch key was never used. One key
// is near its daily cap, which matters less.
import { render } from 'ink';
import type { Place, RequestEvent } from '../src/api.js';
import { App } from '../src/App.js';
import { fakeClient, incident, key, NOW } from './fakes.js';

const client = fakeClient();
const day = 86_400_000;
const min = 60_000;
const at = (ago: number) => new Date(Date.now() - ago).toISOString();
const place = (kind: Place['kind'], value: string, first: number, last: number, requests: number, refused: number, label?: string): Place => ({
  kind,
  value,
  label,
  first_seen: at(first),
  last_seen: at(last),
  requests,
  refused,
});

client.keys[0]!.policy = { allowed_cidrs: ['10.0.0.0/8'] };
client.keys[1]!.policy = { allowed_cidrs: ['10.0.0.0/8'] };
client.keys.push(
  key({ id: 'k_prod', name: 'prod-api', policy: { allowed_cidrs: ['10.20.0.0/16'] }, spend: { today: { period: 'd', cost_micro: 1_300_000, requests: 812, cap_micro: 5_000_000 }, month: { period: 'm', cost_micro: 61_000_000, requests: 9100, cap_micro: 200_000_000 } } }),
  key({ id: 'k_soon', name: 'contractor', expires_at: new Date(NOW + 3 * day).toISOString() }),
  key({ id: 'k_nocap', name: 'scratch', spend: undefined, policy: { allowed_cidrs: ['10.0.0.0/8'] } }),
  key({ id: 'k_rev', name: 'leaked-demo', status: 'revoked' }),
);
// Keep the same keys near expiry every time a participant starts the demo.
const shift = Date.now() - NOW;
for (const k of client.keys) {
  k.created_at = new Date(Date.parse(k.created_at) + shift).toISOString();
  k.expires_at = new Date(Date.parse(k.expires_at) + shift).toISOString();
}

client.incidents = [
  incident({ id: 12, key_id: 'k_prod', key_name: 'prod-api', severity: 'high', rules: ['new_country', 'new_asn', 'new_client'], detection_count: 3, opened_at: at(40 * min), last_fired_at: at(4 * min) }),
  incident({ id: 13, key_id: 'k_wiki', key_name: 'wiki-canary', severity: 'critical', rules: ['canary'], detection_count: 1, opened_at: at(2 * 3600_000), last_fired_at: at(2 * 3600_000) }),
];
// What each incident recorded, for its view.
const detection = (id: number, rule: string, ago: number, evidence: object) => ({ id, time: at(ago), rule, severity: 'high' as const, evidence });
const from = {
  country: 'NL',
  asn: 60729,
  as_org: 'Stiftung Erneuerbare Freiheit',
  client: 'curl/8',
  address_withheld: true,
  detail: 'the request was refused (address_not_allowed)',
};
client.recorded = {
  12: {
    detections: [
      detection(1, 'new_country', 40 * min, { ...from, baseline: 'US (9100 requests)' }),
      detection(2, 'new_asn', 40 * min, { ...from, baseline: 'AS16509 Amazon.com, Inc. (9100 requests)' }),
      detection(3, 'new_client', 4 * min, { ...from, baseline: 'openai-python/1 (9100 requests)' }),
    ],
    actions: [{ id: 41, time: at(40 * min), actor: 'system:detect', action: 'incident.open', target_kind: 'incident', target_id: '12' }],
    known_source_candidates: [
      { kind: 'country', value: 'NL' },
      { kind: 'asn', value: '60729', label: 'Stiftung Erneuerbare Freiheit' },
      { kind: 'client', value: 'curl/8' },
      { kind: 'cidr', withheld: true },
    ],
    addresses_withheld: true,
  },
  13: {
    detections: [
      detection(4, 'canary', 2 * 3600_000, { country: 'RO', asn: 9009, as_org: 'M247 Europe SRL', client: 'python-requests/2', address_withheld: true, detail: 'planted: the deploy notes in the team wiki' }),
    ],
    actions: [{ id: 40, time: at(2 * 3600_000), actor: 'system:detect', action: 'incident.open', target_kind: 'incident', target_id: '13' }],
    addresses_withheld: true,
  },
};
client.findings = [
  {
    key_id: 'k_soon',
    name: 'contractor',
    rule: 'open_to_every_address',
    message: 'It is open to every address.',
    fix: `kman key update contractor --policy '{"allowed_cidrs":["<network/bits>"]}'`,
  },
  { key_id: 'k_nocap', name: 'scratch', rule: 'unused', message: 'It has never been used.', fix: 'kman key disable scratch' },
];
client.places = {
  k_prod: {
    key_id: 'k_prod',
    networks_withheld: true,
    places: [
      place('country', 'NL', 40 * min, 4 * min, 0, 57),
      place('asn', '60729', 40 * min, 4 * min, 0, 57, 'Stiftung Erneuerbare Freiheit'),
      place('client', 'curl/8', 40 * min, 4 * min, 0, 57),
      place('country', 'US', 25 * day, min, 9100, 0),
      place('asn', '16509', 25 * day, min, 9100, 0, 'Amazon.com, Inc.'),
      place('client', 'openai-python/1', 25 * day, min, 9100, 0),
    ],
  },
  k_soon: {
    key_id: 'k_soon',
    networks_withheld: true,
    places: [
      place('country', 'IN', 12 * day, 20 * min, 310, 0),
      place('asn', '24560', 12 * day, 20 * min, 310, 0, 'Bharti Airtel Ltd.'),
      place('client', 'python-requests/2', 12 * day, 20 * min, 310, 0),
    ],
  },
  k_checkout: {
    key_id: 'k_checkout',
    networks_withheld: true,
    places: [
      place('country', 'US', 16 * day, 2 * min, 3, 0),
      place('asn', '16509', 16 * day, 2 * min, 3, 0, 'Amazon.com, Inc.'),
      place('client', 'openai-node/4', 16 * day, 2 * min, 3, 0),
    ],
  },
  k_ci: {
    key_id: 'k_ci',
    networks_withheld: true,
    places: [
      place('country', 'US', 9 * day, 30 * min, 9, 0),
      place('client', 'openai-python/1', 9 * day, 30 * min, 9, 0),
    ],
  },
};
// The requests behind it: the usual traffic, a probe, the leak refused, and the canary used.
client.canaryIds = ['k_wiki'];
let eventId = 1;
const seen = (ago: number, over: Partial<RequestEvent>) =>
  client.requests.push({ id: eventId++, time: at(ago), kind: 'forwarded', status: 200, operation: 'POST /v1/chat/completions', model: 'gpt-5.4', ...over });
const usual = { key_id: 'k_checkout', key_name: 'checkout', country: 'US', asn: 16509, asn_label: 'Amazon.com, Inc.', client: 'openai-node/4' };
const prod = { key_id: 'k_prod', key_name: 'prod-api', country: 'US', asn: 16509, asn_label: 'Amazon.com, Inc.', client: 'openai-python/1' };
const leak = { kind: 'refused' as const, status: 403, refusal_code: 'address_not_allowed', key_id: 'k_prod', key_name: 'prod-api', country: 'NL', asn: 60729, asn_label: 'Stiftung Erneuerbare Freiheit', client: 'curl/8', client_address_withheld: true };
// Oldest first, as kman stores them.
seen(3 * 3600_000, usual);
seen(2 * 3600_000, { kind: 'refused', status: 401, refusal_code: 'invalid_key', key_id: 'k_wiki', key_name: 'wiki-canary', country: 'RO', asn: 9009, asn_label: 'M247 Europe SRL', client: 'python-requests/2', model: undefined });
seen(50 * min, prod);
seen(40 * min, leak);
seen(30 * min, { ...usual, key_id: 'k_ci', key_name: 'ci-evals', client: 'openai-python/1', asn: undefined, asn_label: undefined });
seen(12 * min, { kind: 'refused', status: 401, refusal_code: 'invalid_key', unidentified_key: true, client: 'curl/8', model: undefined, operation: 'GET /v1/models' });
seen(4 * min, leak);
seen(3 * min, prod);
seen(min, usual);
render(<App client={client} url="http://127.0.0.1:8081" intervalMs={3000} />);
