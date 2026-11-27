// The admin API (ADR 0004), as a typed client. This is the only module that
// talks to the network, and the only place the admin token is used: the TUI is
// a thin client, so every change it makes is one request here.
import http from 'node:http';
import https from 'node:https';

export type KeyStatus = 'active' | 'disabled' | 'revoked';

export interface SpendPeriod {
  period: string;
  cost_micro: number;
  estimated_micro?: number;
  reserved_micro?: number;
  requests: number;
  unmetered_requests?: number;
  /** Absent for no cap. */
  cap_micro?: number;
}

export interface KeySpend {
  today: SpendPeriod;
  month: SpendPeriod;
}

export interface KeyPolicy {
  daily_cap_micro?: number;
  monthly_cap_micro?: number;
  rpm_limit?: number;
  allowed_models?: string[];
  allowed_paths?: string[];
  /** Where the key may be used from. With neither, it works from any address. */
  allowed_cidrs?: string[];
  allowed_countries?: string[];
  /** How long the location rules only record, such as "7d"; absent for the install's setting. */
  learning_period?: string;
}

export interface Key {
  id: string;
  name?: string;
  note?: string;
  status: KeyStatus;
  expired: boolean;
  upstream?: string;
  credential?: string;
  tags: string[];
  policy: KeyPolicy;
  created_at: string;
  expires_at: string;
  spend?: KeySpend;
}

export interface Credential {
  id: string;
  name: string;
  last4?: string;
  created_at: string;
  rotated_at?: string;
}

export interface Upstream {
  id: string;
  name: string;
  host: string;
  port: number;
  profile: string;
  preset?: string;
  preset_version?: number;
  credentials: Credential[];
  path_allowlist: string[];
  tls?: boolean;
  sdk_env?: string;
  /** A newer version of its preset exists. */
  update_available?: boolean;
  created_at: string;
}

export interface AuditEntry {
  id: number;
  time: string;
  actor: string;
  action: string;
  target_kind: string;
  target_id: string;
  client_ip?: string;
  /** The incident the change was made for. */
  incident_id?: number;
}

export interface PriceDataStatus {
  age_days: number;
  generated_at: string;
  stale: boolean;
  notice?: string;
}

export interface Settings {
  default_key_ttl_seconds: number;
  max_key_lifetime_seconds: number;
}

export interface CreateKeyInput {
  name?: string;
  upstream: string;
  /** A credential's name. Needed when the upstream has more than one. */
  credential?: string;
  ttl_seconds?: number;
  policy?: KeyPolicy;
}

/** What one client address did with a key (GET /keys/{id}/clients). */
export interface KeyClient {
  address: string;
  requests: number;
  blocked: number;
  first_seen: string;
  last_seen: string;
  country?: string;
  asn?: number;
  /** The known cost of its requests, in microdollars. */
  cost_micro: number;
  /** Forwarded requests whose cost is not known, which cost_micro leaves out. */
  unmetered_requests?: number;
}

/** The addresses a key was used from, newest first; `total` can be more than are listed. */
export interface KeyClients {
  key_id: string;
  clients: KeyClient[];
  total: number;
}

export type PlaceKind = 'country' | 'asn' | 'network' | 'client';

/** One place a key was used from, or one client that used it (GET /keys/{id}/places). */
export interface Place {
  kind: PlaceKind;
  /** A country code, an AS number, a /24 or /64, or a client such as openai-python/3. */
  value: string;
  /** Counts every place of its kind past the first 1,000. */
  other?: boolean;
  /** An AS's organisation, when kman has the ASN database. */
  label?: string;
  first_seen: string;
  last_seen: string;
  /** Forwarded. */
  requests: number;
  refused: number;
}

/** Where a key was used from, most recently seen first. */
export interface KeyPlaces {
  key_id: string;
  places: Place[];
  /** Networks were left out: expose_client_addresses is off. */
  networks_withheld?: boolean;
}

export type IncidentSeverity = 'low' | 'medium' | 'high' | 'critical';

/** The case kman opens for a key when a detection rule fires or a canary is used. */
export interface Incident {
  id: number;
  key_id: string;
  key_name?: string;
  state: 'open' | 'acknowledged' | 'resolved';
  /** Nothing has fired for an hour. */
  quiet?: boolean;
  severity: IncidentSeverity;
  /** new_country, new_asn, new_network, new_client or canary, in the order they first fired. */
  rules: string[];
  detection_count: number;
  opened_at: string;
  last_fired_at: string;
  acknowledged_at?: string;
  acknowledged_by?: string;
  resolved_at?: string;
  /** An admin token's name, or system:detect when it expired. */
  resolved_by?: string;
  resolution?: Resolution | 'expired';
}

/** How an operator ends an incident: it was real, or it was not. */
export type Resolution = 'confirmed' | 'false_positive';

/** What a detection saw. The address and networks are left out unless expose_client_addresses is on. */
export interface Evidence {
  address?: string;
  /** For a new network, the networks the key normally uses. */
  usual?: string;
  network?: string;
  /** There is an address or network, which was left out. */
  address_withheld?: boolean;
  country?: string;
  asn?: number;
  as_org?: string;
  client?: string;
  /** What the rule saw, for a rule that compares. */
  observed?: string;
  /** What the key normally does. */
  baseline?: string;
  /** Such as that the request was refused, or where a canary was planted. */
  detail?: string;
}

export interface Detection {
  id: number;
  time: string;
  rule: string;
  severity: IncidentSeverity;
  evidence: Evidence;
}

export type SourceKind = 'cidr' | 'country' | 'asn' | 'client';

/** A place an incident saw that "this was me" can add as a known source. */
export interface Candidate {
  kind: SourceKind;
  /** Empty for a network that is withheld. */
  value?: string;
  label?: string;
  withheld?: boolean;
}

/** An incident with what it recorded and what was done about it (GET /incidents/{id}). */
export interface IncidentDetail extends Incident {
  /** Oldest first, at most 100; detection_count says how many there were. */
  detections: Detection[];
  /** The audit entries that carry the incident's id, oldest first. */
  actions: AuditEntry[];
  /** None for a canary. */
  known_source_candidates: Candidate[];
  addresses_withheld?: boolean;
}

/** The kinds "this was me" picks by; a network is the candidate kind cidr. */
export type PickKind = 'country' | 'asn' | 'client' | 'network';

export interface ThisWasMeInput {
  /** With no `kinds` this adds every kind, so it is only sent with some. */
  add_known_sources: boolean;
  /** For every key, not just this one. */
  install_wide?: boolean;
  kinds?: PickKind[];
}

export interface ThisWasMeResult {
  incident: Incident;
  added: { id: number; kind: SourceKind; value: string; label: string }[];
  already_known?: number;
  /** Networks not added, as adding one would show it while addresses are off. */
  withheld_networks?: number;
}

/** A key after rotation, with its new secret, which is shown only this once. */
export interface RotatedKey {
  key: Key;
  secret: string;
  previous_valid_until?: string;
  sdk_env?: string;
}

/** A canary, as far as the TUI reads one. */
export interface Canary {
  id: string;
  name?: string;
  state: string;
}

/** One rule a working key breaks that would make a leak easy to abuse (GET /exposure). */
export interface ExposureFinding {
  key_id: string;
  name?: string;
  /** open_to_every_address, never_expires or unused. */
  rule: string;
  message: string;
  last_used_at?: string;
  /** The kman command that fixes it. */
  fix: string;
}

export interface CreatedKey {
  proxy_url?: string;
  key: Key;
  /** The virtual key itself. Shown once: kman keeps only its hash. */
  secret: string;
  sdk_env?: string;
  sdk_base_path?: string;
}

export type ErrorCode = 'invalid' | 'not_found' | 'conflict' | 'unauthorized' | 'internal';

/** An error the admin API answered with, or `unreachable` when it did not answer. */
export class ApiError extends Error {
  constructor(
    readonly code: ErrorCode | 'unreachable',
    message: string,
    readonly status?: number,
  ) {
    super(message);
    this.name = 'ApiError';
  }
}

/** The server has no such route (an older kman), as against a request that failed. */
export const isMissingRoute = (err: unknown) => err instanceof ApiError && (err.code === 'not_found' || err.status === 404);

/**
 * Control characters, and the marks that reorder text. Some strings come from outside kman
 * (an AS's organisation is whatever the ASN database says), and none may move the cursor,
 * restyle the screen or add a line.
 */
const UNSAFE = /[\u0000-\u001f\u007f-\u009f\u2028\u2029\u202a-\u202e\u2066-\u2069]/g;

/** Parses an answer, with every string in it made safe to draw. */
const parseJson = (text: string): unknown =>
  JSON.parse(text, (_, v: unknown) => (typeof v === 'string' ? v.replace(UNSAFE, ' ') : v));

export interface Connection {
  /** http://host:port, https://host:port or unix:/path/to/socket. */
  url: string;
  token: string;
}

export interface AdminClient {
  whoami(): Promise<string>;
  listKeys(): Promise<Key[]>;
  listUpstreams(): Promise<Upstream[]>;
  /** The newest entries, oldest first. */
  latestAudit(limit: number): Promise<AuditEntry[]>;
  priceStatus(): Promise<PriceDataStatus>;
  settings(): Promise<Settings>;
  createKey(input: CreateKeyInput): Promise<CreatedKey>;
  /** With an incident, the change is audited with its id, and the incident stays open. */
  disableKey(id: string, incident?: number): Promise<Key>;
  enableKey(id: string): Promise<Key>;
  /** With an incident, which it resolves as confirmed. */
  revokeKey(id: string, incident?: number): Promise<Key>;
  /** A new secret at once, with no grace, for an incident it resolves as confirmed. */
  rotateKey(id: string, incident: number): Promise<RotatedKey>;
  incident(id: number): Promise<IncidentDetail>;
  acknowledgeIncident(id: number): Promise<Incident>;
  resolveIncident(id: number, as: Resolution): Promise<Incident>;
  thisWasMe(id: number, input: ThisWasMeInput): Promise<ThisWasMeResult>;
  /** Arms a fired canary again, by its key id. */
  resetCanary(id: string): Promise<Canary>;
  /**
   * The client addresses a key was used from. Refused with `conflict` until the install turns
   * on expose_client_addresses: an address is personal data.
   */
  keyClients(id: string): Promise<KeyClients>;
  /** Where a key was used from, and by which clients. */
  keyPlaces(id: string): Promise<KeyPlaces>;
  /** The incidents not yet resolved, newest first. */
  openIncidents(): Promise<Incident[]>;
  /** The working keys that would be easy to abuse if they leaked. */
  exposure(): Promise<ExposureFinding[]>;
}

const TIMEOUT_MS = 5000;

export function createClient(conn: Connection): AdminClient {
  const target = parseTarget(conn.url);

  function request<T>(method: string, path: string, body?: unknown): Promise<T> {
    return new Promise<T>((resolve, reject) => {
      const payload = body === undefined ? undefined : JSON.stringify(body);
      const headers: Record<string, string> = {
        authorization: `Bearer ${conn.token}`,
        accept: 'application/json',
      };
      if (payload !== undefined) {
        headers['content-type'] = 'application/json';
        headers['content-length'] = String(Buffer.byteLength(payload));
      } else if (method !== 'GET') {
        headers['content-length'] = '0';
      }
      const req = target.transport.request(
        { ...target.options, method, path, headers, timeout: TIMEOUT_MS },
        (res) => {
          const chunks: Buffer[] = [];
          // A connection dropped part way through a reply gives no 'end', and no error on
          // the request: without this the reply is waited on for ever.
          const cut = () => {
            if (!res.complete) reject(new ApiError('unreachable', `the reply from ${conn.url} was cut off`));
          };
          res.on('error', cut);
          res.on('close', cut);
          res.on('data', (c: Buffer) => chunks.push(c));
          res.on('end', () => {
            const text = Buffer.concat(chunks).toString('utf8');
            const status = res.statusCode ?? 0;
            if (status >= 200 && status < 300) {
              try {
                resolve(parseJson(text) as T);
              } catch {
                reject(new ApiError('internal', 'the admin API sent a reply that is not JSON', status));
              }
              return;
            }
            reject(parseError(text, status));
          });
        },
      );
      req.on('timeout', () => req.destroy(new Error('timed out')));
      req.on('error', (err) => {
        const code = (err as NodeJS.ErrnoException).code;
        const why = code === 'ECONNREFUSED' ? 'connection refused' : (code ?? err.message);
        reject(new ApiError('unreachable', `cannot reach ${conn.url}: ${why}`));
      });
      if (payload !== undefined) req.write(payload);
      req.end();
    });
  }

  const keyPath = (id: string) => `/admin/v1/keys/${encodeURIComponent(id)}`;
  const incidentPath = (id: number) => `/admin/v1/incidents/${id}`;
  const forIncident = (id: number | undefined) => (id === undefined ? undefined : { incident_id: id });
  /** The server sends an incident that has no rules yet as null. */
  const withRules = <T extends Incident>(i: T): T => ({ ...i, rules: i.rules ?? [] });

  return {
    async whoami() {
      return (await request<{ actor: string }>('GET', '/admin/v1/whoami')).actor;
    },
    async listKeys() {
      const keys = (await request<{ keys: Key[] | null }>('GET', '/admin/v1/keys')).keys ?? [];
      // The server sends a key with no tags as null.
      return keys.map((k) => ({ ...k, tags: k.tags ?? [], policy: k.policy ?? {} }));
    },
    async listUpstreams() {
      const list = (await request<{ upstreams: Upstream[] | null }>('GET', '/admin/v1/upstreams')).upstreams ?? [];
      // The server leaves out `credentials` for an upstream that has none.
      return list.map((u) => ({ ...u, credentials: u.credentials ?? [], path_allowlist: u.path_allowlist ?? [] }));
    },
    async latestAudit(limit) {
      const page = await request<{ entries: AuditEntry[] }>(
        'GET',
        `/admin/v1/audit?latest=true&limit=${limit}`,
      );
      return page.entries ?? [];
    },
    priceStatus: () => request<PriceDataStatus>('GET', '/admin/v1/prices/status'),
    settings: () => request<Settings>('GET', '/admin/v1/settings'),
    createKey: (input) => request<CreatedKey>('POST', '/admin/v1/keys', input),
    disableKey: (id, incident) => request<Key>('POST', `${keyPath(id)}/disable`, forIncident(incident)),
    enableKey: (id) => request<Key>('POST', `${keyPath(id)}/enable`),
    revokeKey: (id, incident) => request<Key>('POST', `${keyPath(id)}/revoke`, forIncident(incident)),
    rotateKey: (id, incident) => request<RotatedKey>('POST', `${keyPath(id)}/rotate`, forIncident(incident)),
    async incident(id) {
      const got = await request<IncidentDetail>('GET', incidentPath(id));
      return withRules({
        ...got,
        detections: (got.detections ?? []).map((d) => ({ ...d, evidence: d.evidence ?? {} })),
        actions: got.actions ?? [],
        known_source_candidates: got.known_source_candidates ?? [],
      });
    },
    async acknowledgeIncident(id) {
      return withRules(await request<Incident>('POST', `${incidentPath(id)}/acknowledge`));
    },
    async resolveIncident(id, as) {
      return withRules(await request<Incident>('POST', `${incidentPath(id)}/resolve`, { as }));
    },
    async thisWasMe(id, input) {
      const got = await request<ThisWasMeResult>('POST', `${incidentPath(id)}/this-was-me`, input);
      return { ...got, added: got.added ?? [] };
    },
    resetCanary: (id) => request<Canary>('POST', `/admin/v1/canaries/${encodeURIComponent(id)}/reset`),
    async keyClients(id) {
      const got = await request<KeyClients>('GET', `${keyPath(id)}/clients`);
      return { ...got, clients: got.clients ?? [] };
    },
    async keyPlaces(id) {
      const got = await request<KeyPlaces>('GET', `${keyPath(id)}/places`);
      return { ...got, places: got.places ?? [] };
    },
    async openIncidents() {
      const all: Incident[] = [];
      let before: number | undefined;
      // Newest first, a page at a time: an old canary incident must not fall off the end.
      for (;;) {
        const cursor = before === undefined ? '' : `&before=${before}`;
        const page = await request<{ incidents: Incident[] | null; more?: boolean }>('GET', `/admin/v1/incidents?open=true&limit=500${cursor}`);
        const list = page.incidents ?? [];
        all.push(...list.map(withRules));
        const last = list[list.length - 1]?.id;
        if (!page.more || last === undefined || (before !== undefined && last >= before)) return all;
        before = last;
      }
    },
    async exposure() {
      return (await request<{ findings: ExposureFinding[] | null }>('GET', '/admin/v1/exposure')).findings ?? [];
    },
  };
}

interface Target {
  transport: typeof http | typeof https;
  options: http.RequestOptions;
}

function parseTarget(url: string): Target {
  if (url.startsWith('unix:')) {
    return { transport: http, options: { socketPath: url.slice('unix:'.length), headers: { host: 'kman' } } };
  }
  const u = new URL(url);
  const secure = u.protocol === 'https:';
  return {
    transport: secure ? https : http,
    options: { hostname: u.hostname.replace(/^\[|\]$/g, ''), port: u.port || (secure ? 443 : 80) },
  };
}

function parseError(text: string, status: number): ApiError {
  try {
    const e = (parseJson(text) as { error?: { code?: ErrorCode; message?: string } }).error;
    if (e?.message) return new ApiError(e.code ?? 'internal', e.message, status);
  } catch {
    /* not JSON: fall through */
  }
  return new ApiError(status === 401 ? 'unauthorized' : 'internal', `the admin API answered ${status}`, status);
}
