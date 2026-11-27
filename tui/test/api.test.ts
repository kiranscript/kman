import http from 'node:http';
import type { AddressInfo } from 'node:net';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { ApiError, createClient } from '../src/api.js';

let server: http.Server;
let seen: { method?: string; url?: string; auth?: string; body: string }[];
let respond: (req: http.IncomingMessage, res: http.ServerResponse) => void;
let url: string;

beforeEach(async () => {
  seen = [];
  respond = (_req, res) => res.writeHead(200).end('{}');
  server = http.createServer((req, res) => {
    let body = '';
    req.on('data', (c) => (body += c));
    req.on('end', () => {
      seen.push({ method: req.method, url: req.url, auth: req.headers.authorization, body });
      respond(req, res);
    });
  });
  await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
  url = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
});
afterEach(() => new Promise<void>((r) => server.close(() => r())));

/** A port nothing listens on: one that was just freed. */
async function closedPort(): Promise<number> {
  const s = http.createServer();
  await new Promise<void>((r) => s.listen(0, '127.0.0.1', r));
  const { port } = s.address() as AddressInfo;
  await new Promise<void>((r) => s.close(() => r()));
  return port;
}

const json = (res: http.ServerResponse, status: number, body: unknown) =>
  res.writeHead(status, { 'content-type': 'application/json' }).end(JSON.stringify(body));

describe('the admin client', () => {
  it('sends the admin token as a bearer token and nothing else secret', async () => {
    respond = (_r, res) => json(res, 200, { actor: 'owner' });
    expect(await createClient({ url, token: 'kma_abc' }).whoami()).toBe('owner');
    expect(seen[0]).toMatchObject({ method: 'GET', url: '/admin/v1/whoami', auth: 'Bearer kma_abc' });
  });

  it('takes control characters out of every string, so none can drive the terminal', async () => {
    respond = (_r, res) =>
      json(res, 200, { key_id: 'k', places: [{ kind: 'asn', value: '1', label: 'Evil\u001b[31m\nOrg\u009b\u202e' }] });
    const got = await createClient({ url, token: 't' }).keyPlaces('k');
    expect(got.places[0]?.label).toBe('Evil [31m Org  ');
  });

  it('reads every page of open incidents', async () => {
    respond = (r, res) =>
      r.url?.includes('before=')
        ? json(res, 200, { incidents: [{ id: 1, rules: null }], more: false })
        : json(res, 200, { incidents: [{ id: 3, rules: [] }, { id: 2, rules: [] }], more: true });
    const got = await createClient({ url, token: 't' }).openIncidents();
    expect(got.map((i) => i.id)).toEqual([3, 2, 1]);
    expect(got[2]?.rules).toEqual([]);
    expect(seen.map((s) => s.url)).toEqual(['/admin/v1/incidents?open=true&limit=500', '/admin/v1/incidents?open=true&limit=500&before=2']);
  });

  it('asks for the newest audit entries and returns the list', async () => {
    respond = (_r, res) => json(res, 200, { entries: [{ id: 2 }, { id: 1 }], more: false });
    const entries = await createClient({ url, token: 't' }).latestAudit(50);
    expect(seen[0]?.url).toBe('/admin/v1/audit?latest=true&limit=50');
    expect(entries).toHaveLength(2);
  });

  it('treats a null list as empty', async () => {
    respond = (_r, res) => json(res, 200, { keys: null });
    expect(await createClient({ url, token: 't' }).listKeys()).toEqual([]);
  });

  it('fills in what the server leaves out: no credentials, null tags', async () => {
    respond = (req, res) =>
      req.url === '/admin/v1/keys'
        ? json(res, 200, { keys: [{ id: 'k1', tags: null, policy: null }] })
        : json(res, 200, { upstreams: [{ id: 'u1', name: 'openai' }] });
    const client = createClient({ url, token: 't' });
    expect((await client.listUpstreams())[0]).toMatchObject({ credentials: [], path_allowlist: [] });
    expect((await client.listKeys())[0]).toMatchObject({ tags: [], policy: {} });
  });

  it('reads a key\'s client addresses by key id, and treats a null list as empty', async () => {
    respond = (_r, res) => json(res, 200, { key_id: 'k/1', clients: null, total: 0 });
    const got = await createClient({ url, token: 't' }).keyClients('k/1');
    expect(seen[0]).toMatchObject({ method: 'GET', url: '/admin/v1/keys/k%2F1/clients' });
    expect(got.clients).toEqual([]);
  });

  it('says the install does not give addresses out as a conflict', async () => {
    respond = (_r, res) => json(res, 409, { error: { code: 'conflict', message: 'turn it on: kman settings set expose_client_addresses true' } });
    await expect(createClient({ url, token: 't' }).keyClients('k1')).rejects.toMatchObject({ code: 'conflict', status: 409 });
  });

  it('posts a key change to the key by id, with no body', async () => {
    respond = (_r, res) => json(res, 200, { id: 'k/1', status: 'disabled' });
    await createClient({ url, token: 't' }).disableKey('k/1');
    expect(seen[0]).toMatchObject({ method: 'POST', url: '/admin/v1/keys/k%2F1/disable', body: '' });
  });

  it('creates a key with a JSON body', async () => {
    respond = (_r, res) => json(res, 201, { key: { id: 'k' }, secret: 'kmn_x' });
    const out = await createClient({ url, token: 't' }).createKey({ upstream: 'openai', name: 'a', ttl_seconds: 86400 });
    expect(JSON.parse(seen[0]!.body)).toEqual({ upstream: 'openai', name: 'a', ttl_seconds: 86400 });
    expect(out.secret).toBe('kmn_x');
  });

  it('turns an API error into its code and message', async () => {
    respond = (_r, res) => json(res, 409, { error: { code: 'conflict', message: 'name taken' } });
    await expect(createClient({ url, token: 't' }).revokeKey('k')).rejects.toMatchObject({
      name: 'ApiError',
      code: 'conflict',
      message: 'name taken',
      status: 409,
    });
  });

  it('reads a 401 without a body as unauthorized', async () => {
    respond = (_r, res) => res.writeHead(401).end();
    await expect(createClient({ url, token: 't' }).whoami()).rejects.toMatchObject({ code: 'unauthorized' });
  });

  it('says unreachable when nothing answers', async () => {
    const dead = `http://127.0.0.1:${await closedPort()}`;
    const err = await createClient({ url: dead, token: 't' }).whoami().catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err.code).toBe('unreachable');
    expect(err.message).toContain(dead);
    expect(err.message).toContain('connection refused');
  });

  it('never puts the token in an error', async () => {
    const dead = `http://127.0.0.1:${await closedPort()}`;
    const err = await createClient({ url: dead, token: 'kma_supersecret' }).whoami().catch((e) => e);
    expect(String(err.message)).not.toContain('kma_supersecret');
  });

  it('talks over a unix socket', async () => {
    const { mkdtempSync } = await import('node:fs');
    const { tmpdir } = await import('node:os');
    const sock = `${mkdtempSync(`${tmpdir()}/kman-`)}/admin.sock`;
    const s = http.createServer((_q, res) => json(res, 200, { actor: 'sock' }));
    await new Promise<void>((r) => s.listen(sock, r));
    try {
      expect(await createClient({ url: `unix:${sock}`, token: 't' }).whoami()).toBe('sock');
    } finally {
      await new Promise<void>((r) => s.close(() => r()));
    }
  });
});
