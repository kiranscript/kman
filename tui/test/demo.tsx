// A demo of the TUI against the in-memory fake admin API: no kman server needed.
//   cd tui && npx tsx test/demo.tsx
//
// It tells a leak: prod-api, locked to its own network, turned up from an exit
// node abroad and was refused; a canary planted in the wiki was used; a
// contractor's key works from any address; a scratch key was never used. One key
// is near its daily cap, which matters less.
import { render } from 'ink';
import type { Place } from '../src/api.js';
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
render(<App client={client} url="http://127.0.0.1:8081" intervalMs={3000} />);
