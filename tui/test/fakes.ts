import type {
  AdminClient,
  AuditEntry,
  Candidate,
  Detection,
  ExposureFinding,
  Incident,
  IncidentDetail,
  Key,
  KeyClients,
  KeyPlaces,
  Place,
  RequestEvent,
  Resolution,
  Upstream,
} from '../src/api.js';
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

export function detection(id: number, rule: string, evidence: Detection['evidence'], ago = 40 * 60_000): Detection {
  return { id, time: new Date(NOW - ago).toISOString(), rule, severity: 'high', evidence };
}

/** What an incident recorded, beside what the list says of it. */
export type Recorded = Partial<Pick<IncidentDetail, 'detections' | 'actions' | 'known_source_candidates' | 'addresses_withheld'>>;

/** A new country, ASN and client, as the location rules record them, with the network withheld. */
export function leak(): Recorded {
  const evidence = { country: 'NL', asn: 60729, as_org: 'Stiftung Erneuerbare Freiheit', client: 'curl/8', address_withheld: true, detail: 'the request was refused (address_not_allowed)' };
  const candidates: Candidate[] = [
    { kind: 'country', value: 'NL' },
    { kind: 'asn', value: '60729', label: 'Stiftung Erneuerbare Freiheit' },
    { kind: 'client', value: 'curl/8' },
    { kind: 'cidr', withheld: true },
  ];
  return {
    detections: [
      detection(1, 'new_country', { ...evidence, observed: 'new country NL', baseline: 'US (9100 requests)' }),
      detection(2, 'new_asn', { ...evidence, observed: 'new ASN AS60729', baseline: 'AS16509 Amazon.com (9100 requests)' }),
      detection(3, 'new_client', { ...evidence, observed: 'new client curl/8', baseline: 'openai-python/1 (9100 requests)' }),
    ],
    actions: [{ id: 100, time: new Date(NOW - 40 * 60_000).toISOString(), actor: 'system:detect', action: 'incident.open', target_kind: 'incident', target_id: '12' }],
    known_source_candidates: candidates,
    addresses_withheld: true,
  };
}

/** A forwarded request from checkout's usual place, `ago` before NOW; the newest has the highest id. */
export function request(over: Partial<RequestEvent> & { id: number }, ago = 60_000): RequestEvent {
  return {
    time: new Date(NOW - ago).toISOString(),
    kind: 'forwarded',
    key_id: 'k_checkout',
    key_name: 'checkout',
    country: 'US',
    asn: 16509,
    asn_label: 'Amazon.com, Inc.',
    client: 'openai-node/4',
    operation: 'POST /v1/chat/completions',
    model: 'gpt-5.4',
    status: 200,
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
  /** What each incident recorded, by id. */
  recorded: Record<number, Recorded>;
  /** The audit entries the changes made, newest last: each with the incident it was for. */
  audited: AuditEntry[];
  canaryIds: string[];
  /** Stored requests, in any order. */
  requests: RequestEvent[];
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
  const findIncident = (id: number) => {
    const i = fake.incidents.find((x) => x.id === id);
    if (!i) throw new ApiError('not_found', `incident ${id} does not exist`, 404);
    return i;
  };
  const audited = (action: string, target: string, incident?: number) => {
    const entry: AuditEntry = { id: 1000 + fake.audited.length, time: new Date(NOW).toISOString(), actor: 'owner', action, target_kind: action.split('.')[0]!, target_id: target, incident_id: incident };
    fake.audited.push(entry);
    if (incident !== undefined) {
      const r = (fake.recorded[incident] ??= {});
      r.actions = [...(r.actions ?? []), entry];
    }
  };
  /** As the server does: the incident named must be about the key and unresolved. */
  const unresolved = (id: number, key?: string) => {
    const i = findIncident(id);
    if (i.state === 'resolved') throw new ApiError('conflict', `incident ${id} is already resolved (${i.resolution?.replace('_', ' ')})`, 409);
    if (key !== undefined && i.key_id !== key) throw new ApiError('conflict', `incident ${id} is about key ${i.key_id}, not key ${key}`, 409);
    return i;
  };
  const end = (i: Incident, as: Resolution, action: string) => {
    Object.assign(i, { state: 'resolved', resolution: as, resolved_by: 'owner', resolved_at: new Date(NOW).toISOString() });
    audited(action, String(i.id), i.id);
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
    recorded: {},
    audited: [],
    canaryIds: [],
    requests: [],
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
      return { default_key_ttl_seconds: 90 * 86_400, max_key_lifetime_seconds: 365 * 86_400, expose_client_addresses: fake.exposeClients };
    },
    async createKey(input) {
      guard();
      fake.calls.push(`create ${JSON.stringify(input)}`);
      const k = key({ id: 'k_new', name: input.name ?? 'k_new', policy: input.policy ?? {} });
      fake.keys.push(k);
      return { key: k, secret: 'kmn_secretsecretsecret', sdk_env: 'OPENAI_API_KEY', sdk_base_path: '/v1' };
    },
    async disableKey(id, incident) {
      guard();
      fake.calls.push(`disable ${id}${incident === undefined ? '' : ` ${incident}`}`);
      if (incident !== undefined) unresolved(incident, id);
      const k = find(id);
      k.status = 'disabled';
      audited('key.disable', id, incident);
      return k;
    },
    async enableKey(id) {
      guard();
      fake.calls.push(`enable ${id}`);
      const k = find(id);
      k.status = 'active';
      audited('key.enable', id);
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
    async revokeKey(id, incident) {
      guard();
      fake.calls.push(`revoke ${id}${incident === undefined ? '' : ` ${incident}`}`);
      const i = incident === undefined ? undefined : unresolved(incident, id);
      const k = find(id);
      k.status = 'revoked';
      audited('key.revoke', id, incident);
      if (i) end(i, 'confirmed', 'incident.resolve');
      return k;
    },
    async rotateKey(id, incident) {
      guard();
      fake.calls.push(`rotate ${id} ${incident}`);
      const i = unresolved(incident, id);
      const k = find(id);
      audited('key.rotate', id, incident);
      end(i, 'confirmed', 'incident.resolve');
      return { key: k, secret: 'kmn_rotatedrotatedrotated', sdk_env: 'OPENAI_API_KEY' };
    },
    async incident(id) {
      guard();
      const i = findIncident(id);
      return structuredClone({ ...i, detections: [], actions: [], known_source_candidates: [], ...fake.recorded[id] });
    },
    async acknowledgeIncident(id) {
      guard();
      fake.calls.push(`acknowledge ${id}`);
      const i = unresolved(id);
      Object.assign(i, { state: 'acknowledged', acknowledged_by: 'owner', acknowledged_at: new Date(NOW).toISOString() });
      audited('incident.acknowledge', String(id), id);
      return structuredClone(i);
    },
    async resolveIncident(id, as) {
      guard();
      fake.calls.push(`resolve ${id} ${as}`);
      const was = findIncident(id);
      if (was.state === 'resolved' && was.resolution === as) return structuredClone(was);
      const i = unresolved(id);
      end(i, as, 'incident.resolve');
      return structuredClone(i);
    },
    async thisWasMe(id, input) {
      guard();
      fake.calls.push(`this-was-me ${id} ${JSON.stringify(input)}`);
      const i = unresolved(id);
      end(i, 'false_positive', 'incident.this_was_me');
      const picked = input.add_known_sources
        ? (fake.recorded[id]?.known_source_candidates ?? []).filter((c) => !c.withheld && (!input.kinds || input.kinds.includes(c.kind === 'cidr' ? 'network' : c.kind)))
        : [];
      return { incident: structuredClone(i), added: picked.map((c, n) => ({ id: n + 1, kind: c.kind, value: c.value!, label: 'this was me' })) };
    },
    async canaries() {
      guard();
      return fake.canaryIds.map((id) => ({ id, state: 'armed' }));
    },
    async events(query = {}) {
      guard();
      if (query.key !== undefined && !fake.canaryIds.includes(query.key)) find(query.key);
      const matching = fake.requests
        .filter((e) => (query.key === undefined || e.key_id === query.key) && (query.kind === undefined || e.kind === query.kind) && (query.before === undefined || e.id < query.before))
        .sort((a, b) => b.id - a.id);
      const events = matching.slice(0, query.limit ?? 100);
      const more = matching.length > events.length;
      const withheld = !fake.exposeClients && events.some((e) => e.client_address !== undefined);
      return structuredClone({
        events: withheld ? events.map(({ client_address, ...e }) => ({ ...e, client_address_withheld: client_address !== undefined })) : events,
        more,
        ...(more ? { next_before: events.at(-1)!.id } : {}),
        ...(withheld ? { client_addresses_withheld: true } : {}),
      });
    },
    async resetCanary(id) {
      guard();
      fake.calls.push(`reset ${id}`);
      audited('canary.reset', id);
      return { id, state: 'armed' };
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
