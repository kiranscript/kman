import type { AdminClient, AuditEntry, ExposureFinding, Incident, Key, KeyClients, KeyPlaces, Place, Upstream } from '../src/api.js';
import { ApiError } from '../src/api.js';

export const NOW = Date.parse('2026-11-17T12:30:00Z');
const day = 86_400_000;

export function key(over: Partial<Key> & { id: string }): Key {
  return {
    name: over.id,
    status: 'active',
    expired: false,
    upstream: 'openai',
    tags: [],
    policy: {},
    created_at: '2026-11-01T00:00:00Z',
    expires_at: new Date(NOW + 60 * day).toISOString(),
    spend: {
      today: { period: '2026-11-17', cost_micro: 108_000, requests: 3, cap_micro: 5_000_000 },
      month: { period: '2026-11', cost_micro: 108_000, requests: 3, cap_micro: 50_000_000 },
    },
    ...over,
  };
}

export function incident(over: Partial<Incident> & { id: number; key_id: string }): Incident {
  return {
    state: 'open',
    severity: 'high',
    rules: ['new_country', 'new_asn'],
    detection_count: 1,
    opened_at: new Date(NOW - 40 * 60_000).toISOString(),
    last_fired_at: new Date(NOW - 40 * 60_000).toISOString(),
    ...over,
  };
}

export function place(over: Partial<Place> & Pick<Place, 'kind' | 'value'>): Place {
  return {
    first_seen: new Date(NOW - 20 * day).toISOString(),
    last_seen: new Date(NOW - 60_000).toISOString(),
    requests: 10,
    refused: 0,
    ...over,
  };
}

export const upstream: Upstream = {
  id: 'up_1',
  name: 'openai',
  host: 'api.openai.com',
  port: 443,
  tls: true,
  profile: 'openai',
  preset: 'openai',
  preset_version: 1,
  credentials: [{ id: 'cr_1', name: 'openai', last4: '1234', created_at: '2026-11-01T00:00:00Z' }],
  path_allowlist: ['POST /v1/chat/completions'],
  sdk_env: 'OPENAI_API_KEY',
  created_at: '2026-11-01T00:00:00Z',
};

export const audit: AuditEntry[] = [
  { id: 1, time: '2026-11-17T12:00:00Z', actor: 'owner', action: 'key.create', target_kind: 'key', target_id: 'k_web' },
];

export interface Fake extends AdminClient {
  calls: string[];
  keys: Key[];
  /** Make every call fail with this until cleared. */
  fail?: ApiError;
  upstreams: Upstream[];
  /** Each key's client addresses, by key id. */
  clients: Record<string, KeyClients>;
  /** Whether the install gives addresses out (expose_client_addresses). */
  exposeClients: boolean;
  /** Each key's places, by key id. */
  places: Record<string, KeyPlaces>;
  /** Incidents; openIncidents leaves out the resolved ones. */
  incidents: Incident[];
  findings: ExposureFinding[];
}

/** An in-memory admin API that records the changes it is asked to make. */
export function fakeClient(keys: Key[] = defaultKeys()): Fake {
  const find = (id: string) => {
    const k = fake.keys.find((x) => x.id === id);
    if (!k) throw new ApiError('not_found', `no key ${id}`, 404);
    return k;
  };
  const guard = () => {
    if (fake.fail) throw fake.fail;
  };
  const fake: Fake = {
    calls: [],
    keys,
    upstreams: [upstream],
    clients: {},
    exposeClients: true,
    places: {},
    incidents: [],
    findings: [],
    async whoami() {
      guard();
      return 'owner';
    },
    async listKeys() {
      guard();
      return structuredClone(fake.keys);
    },
    async listUpstreams() {
      guard();
      return structuredClone(fake.upstreams);
    },
    async latestAudit() {
      guard();
      return audit;
    },
    async priceStatus() {
      guard();
      return { age_days: 3, generated_at: '2026-11-14T00:00:00Z', stale: false };
    },
    async settings() {
      guard();
      return { default_key_ttl_seconds: 90 * 86_400, max_key_lifetime_seconds: 365 * 86_400 };
    },
    async createKey(input) {
      guard();
      fake.calls.push(`create ${JSON.stringify(input)}`);
      const k = key({ id: 'k_new', name: input.name ?? 'k_new', policy: input.policy ?? {} });
      fake.keys.push(k);
      return { key: k, secret: 'kmn_secretsecretsecret', sdk_env: 'OPENAI_API_KEY', sdk_base_path: '/v1' };
    },
    async disableKey(id) {
      guard();
      fake.calls.push(`disable ${id}`);
      const k = find(id);
      k.status = 'disabled';
      return k;
    },
    async enableKey(id) {
      guard();
      fake.calls.push(`enable ${id}`);
      const k = find(id);
      k.status = 'active';
      return k;
    },
    async keyClients(id) {
      guard();
      fake.calls.push(`clients ${id}`);
      if (!fake.exposeClients) {
        throw new ApiError(
          'conflict',
          'the admin API does not give out client addresses until you turn that on: kman settings set expose_client_addresses true',
          409,
        );
      }
      find(id);
      return structuredClone(fake.clients[id] ?? { key_id: id, clients: [], total: 0 });
    },
    async keyPlaces(id) {
      guard();
      find(id);
      return structuredClone(fake.places[id] ?? { key_id: id, places: [] });
    },
    async openIncidents() {
      guard();
      return structuredClone(fake.incidents.filter((i) => i.state !== 'resolved'));
    },
    async exposure() {
      guard();
      return structuredClone(fake.findings);
    },
    async revokeKey(id) {
      guard();
      fake.calls.push(`revoke ${id}`);
      const k = find(id);
      k.status = 'revoked';
      return k;
    },
  };
  return fake;
}

export function defaultKeys(): Key[] {
  return [
    key({ id: 'k_checkout', name: 'checkout' }),
    key({
      id: 'k_ci',
      name: 'ci-evals',
      spend: {
        today: { period: 'd', cost_micro: 900_000, requests: 9, cap_micro: 1_000_000 },
        month: { period: 'm', cost_micro: 900_000, requests: 9, cap_micro: 10_000_000 },
      },
    }),
    key({ id: 'k_off', name: 'old-proto', status: 'disabled' }),
  ];
}

export const tick = (ms = 30) => new Promise((r) => setTimeout(r, ms));

/** Polls until `check` passes, so a test waits for the UI instead of guessing how long. */
export async function until(check: () => boolean, what: string, timeout = 2000): Promise<void> {
  const start = Date.now();
  while (!check()) {
    if (Date.now() - start > timeout) throw new Error(`timed out waiting for ${what}`);
    await tick(10);
  }
}
