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

export interface CreatedKey {
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
  disableKey(id: string): Promise<Key>;
  enableKey(id: string): Promise<Key>;
  revokeKey(id: string): Promise<Key>;
  /**
   * The client addresses a key was used from. Refused with `conflict` until the install turns
   * on expose_client_addresses: an address is personal data.
   */
  keyClients(id: string): Promise<KeyClients>;
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
          res.on('data', (c: Buffer) => chunks.push(c));
          res.on('end', () => {
            const text = Buffer.concat(chunks).toString('utf8');
            const status = res.statusCode ?? 0;
            if (status >= 200 && status < 300) {
              try {
                resolve(JSON.parse(text) as T);
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
    disableKey: (id) => request<Key>('POST', `${keyPath(id)}/disable`),
    enableKey: (id) => request<Key>('POST', `${keyPath(id)}/enable`),
    revokeKey: (id) => request<Key>('POST', `${keyPath(id)}/revoke`),
    async keyClients(id) {
      const got = await request<KeyClients>('GET', `${keyPath(id)}/clients`);
      return { ...got, clients: got.clients ?? [] };
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
    const e = (JSON.parse(text) as { error?: { code?: ErrorCode; message?: string } }).error;
    if (e?.message) return new ApiError(e.code ?? 'internal', e.message, status);
  } catch {
    /* not JSON: fall through */
  }
  return new ApiError(status === 401 ? 'unauthorized' : 'internal', `the admin API answered ${status}`, status);
}
