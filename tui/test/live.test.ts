// The client against a real kman, so the hand-written types in src/api.ts are checked
// against the real API and not only against our own fake. It runs only when pointed at
// a server (CI starts one; see ADR 0009 and README.md):
//   KMAN_TUI_LIVE_URL=http://127.0.0.1:8081 KMAN_TUI_LIVE_TOKEN=kma_... npm test
// The server needs at least one upstream with a credential. The test creates a key,
// walks it through disable, enable and revoke, and leaves it revoked. For incidents it
// also needs to run on the same machine as kman: it adds an upstream of its own that
// listens on loopback, and makes keys and a canary that raise incidents, which it
// resolves. Use a throwaway install.
import { createServer } from 'node:http';
import type { Server } from 'node:http';
import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { ApiError, createClient } from '../src/api.js';
import type { Incident } from '../src/api.js';

const url = process.env.KMAN_TUI_LIVE_URL;
const token = process.env.KMAN_TUI_LIVE_TOKEN;

describe.skipIf(!url || !token)('against a real kman', () => {
  // describe.skipIf still runs this body to collect the tests, so nothing here may need the server.
  let client: ReturnType<typeof createClient>;
  beforeAll(() => {
    client = createClient({ url: url!, token: token! });
  });

  it('names the admin token, and reads settings and price data in the shapes the TUI expects', async () => {
    expect(await client.whoami()).toEqual(expect.any(String));
    const settings = await client.settings();
    expect(settings.default_key_ttl_seconds).toBeGreaterThan(0);
    const price = await client.priceStatus();
    expect(price.age_days).toEqual(expect.any(Number));
    expect(price.stale).toEqual(expect.any(Boolean));
  });

  it('creates a key, walks it through disable, enable and revoke, and audits each step', async () => {
    const upstream = (await client.listUpstreams()).find((u) => u.credentials.length > 0);
    expect(upstream, 'the server needs an upstream with a credential').toBeDefined();
    expect(upstream!.path_allowlist).toEqual(expect.any(Array));

    const name = `tui-live-${Date.now()}`;
    const created = await client.createKey({
      upstream: upstream!.name,
      name,
      ttl_seconds: 3600,
      policy: { daily_cap_micro: 1_000_000, monthly_cap_micro: 5_000_000 },
    });
    const proxy = new URL(created.proxy_url!);
    expect(['http:', 'https:']).toContain(proxy.protocol);
    expect(proxy.port).not.toBe('0');
    expect(created.secret).toMatch(/^kmn_/);
    expect(created.key.status).toBe('active');

    const listed = (await client.listKeys()).find((k) => k.id === created.key.id);
    expect(listed).toMatchObject({ name, status: 'active', expired: false, upstream: upstream!.name });
    expect(listed!.policy).toEqual({ daily_cap_micro: 1_000_000, monthly_cap_micro: 5_000_000 });
    expect(listed!.spend?.today).toMatchObject({ cost_micro: 0, requests: 0, cap_micro: 1_000_000 });
    expect(listed!.spend?.month.cap_micro).toBe(5_000_000);
    expect(JSON.stringify(listed)).not.toContain(created.secret);

    expect((await client.disableKey(created.key.id)).status).toBe('disabled');
    expect((await client.enableKey(created.key.id)).status).toBe('active');
    expect((await client.revokeKey(created.key.id)).status).toBe('revoked');
    await expect(client.enableKey(created.key.id)).rejects.toBeInstanceOf(ApiError);

    const audit = await client.latestAudit(50);
    const actions = audit.filter((e) => e.target_id === created.key.id).map((e) => e.action);
    expect(actions).toEqual(expect.arrayContaining(['key.create', 'key.disable', 'key.enable', 'key.revoke']));
    // Oldest first, as the API sends them and the TUI reverses them.
    expect(audit.map((e) => e.id)).toEqual([...audit.map((e) => e.id)].sort((a, b) => a - b));
  });

  it('reads a key\'s client addresses in the expected shape, or is refused until the install turns them on', async () => {
    const key = (await client.listKeys())[0];
    expect(key, 'the server needs a key').toBeDefined();
    const outcome = await client.keyClients(key!.id).then(
      (got) => ({ got }),
      (err: unknown) => ({ err }),
    );
    if ('err' in outcome) {
      // The install's default: off. The refusal says how to turn it on.
      expect(outcome.err).toMatchObject({ code: 'conflict', status: 409 });
      expect((outcome.err as Error).message).toContain('expose_client_addresses');
      return;
    }
    const { got } = outcome;
    expect(got.key_id).toBe(key!.id);
    expect(got.total).toEqual(expect.any(Number));
    expect(got.clients.length).toBeLessThanOrEqual(got.total);
    for (const c of got.clients) {
      expect(c).toMatchObject({
        address: expect.any(String),
        requests: expect.any(Number),
        blocked: expect.any(Number),
        first_seen: expect.any(String),
        last_seen: expect.any(String),
        cost_micro: expect.any(Number),
      });
    }
  });

  it('reads open incidents, exposure findings and a key\'s places in the shapes the TUI expects', async () => {
    const incidents = await client.openIncidents();
    expect(incidents).toEqual(expect.any(Array));
    for (const i of incidents) {
      expect(i).toMatchObject({ id: expect.any(Number), key_id: expect.any(String), severity: expect.any(String), rules: expect.any(Array) });
      expect(i.state).not.toBe('resolved');
    }
    const upstream = (await client.listUpstreams()).find((u) => u.credentials.length > 0);
    // A key with no address lock, so there is a finding to read.
    const { key } = await client.createKey({ upstream: upstream!.name, name: `tui-live-open-${Date.now()}`, ttl_seconds: 3600, policy: {} });
    try {
      const finding = (await client.exposure()).find((f) => f.key_id === key.id);
      expect(finding).toMatchObject({ rule: 'open_to_every_address', fix: expect.stringContaining('kman key update') });
      const places = await client.keyPlaces(key.id);
      expect(places.key_id).toBe(key.id);
      expect(places.places).toEqual([]);
    } finally {
      await client.revokeKey(key.id);
    }
  });

  describe('incidents', () => {
    let upstreamServer: Server;
    const upstreamName = `tui-live-up-${Date.now()}`;

    /** A request to kman's own admin API for what the TUI's client does not do. */
    async function admin<T>(method: string, path: string, body?: unknown): Promise<T> {
      const res = await fetch(`${url}/admin/v1${path}`, {
        method,
        headers: { authorization: `Bearer ${token}`, ...(body ? { 'content-type': 'application/json' } : {}) },
        body: body ? JSON.stringify(body) : undefined,
      });
      expect(res.ok, `${method} ${path}: ${res.status}`).toBe(true);
      return (await res.json()) as T;
    }

    const use = (proxy: string, secret: string, agent: string) =>
      fetch(`${proxy}/v1/models`, { headers: { authorization: `Bearer ${secret}`, 'user-agent': agent } });

    /** The key's unresolved incident, once the events that raise it are written. */
    async function incidentOf(keyId: string): Promise<Incident> {
      for (let n = 0; n < 150; n++) {
        const found = (await client.openIncidents()).find((i) => i.key_id === keyId);
        if (found) return found;
        await new Promise((r) => setTimeout(r, 200));
      }
      throw new Error(`no incident for ${keyId}`);
    }

    /** A key that has an incident: used by one client, then by another once it has learned. */
    async function leakedKey(tag: string) {
      const created = await client.createKey({ upstream: upstreamName, name: `tui-live-${tag}-${Date.now()}`, ttl_seconds: 3600, policy: { learning_period: '1s' } });
      expect((await use(created.proxy_url!, created.secret, 'usual-app/1')).status).toBe(200);
      // The first forwarded request is the baseline once its event is written.
      for (let n = 0; n < 150 && (await client.keyPlaces(created.key.id)).places.length === 0; n++) await new Promise((r) => setTimeout(r, 200));
      await new Promise((r) => setTimeout(r, 1100));
      await use(created.proxy_url!, created.secret, 'curl/8');
      return { ...created, incident: await incidentOf(created.key.id) };
    }

    beforeAll(async () => {
      upstreamServer = createServer((_req, res) => res.writeHead(200, { 'content-type': 'application/json' }).end('{"object":"list","data":[]}'));
      await new Promise<void>((r) => upstreamServer.listen(0, '127.0.0.1', r));
      const { port } = upstreamServer.address() as { port: number };
      await admin('POST', '/upstreams', {
        name: upstreamName,
        host: '127.0.0.1',
        port,
        tls: false,
        profile: 'openai',
        path_allowlist: ['GET /v1/models'],
        sdk_env: 'OPENAI_API_KEY',
        credential: { name: upstreamName, secret: 'sk-live-test-not-real' },
      });
    });

    afterAll(() => void upstreamServer?.close());

    it('reads an incident in full, and acknowledges, disables for it and rotates, each audited with its id', async () => {
      const { key, incident } = await leakedKey('rotate');
      const detail = await client.incident(incident.id);
      expect(detail).toMatchObject({ id: incident.id, key_id: key.id, state: 'open', severity: expect.any(String), rules: expect.arrayContaining(['new_client']), detection_count: expect.any(Number) });
      expect(detail.detections[0]).toMatchObject({ id: expect.any(Number), time: expect.any(String), rule: 'new_client', evidence: { client: 'curl/8', baseline: expect.any(String), address_withheld: true } });
      expect(detail.addresses_withheld).toBe(true);
      expect(detail.known_source_candidates).toEqual(expect.arrayContaining([{ kind: 'client', value: 'curl/8' }, { kind: 'cidr', withheld: true }]));
      expect(detail.actions[0]).toMatchObject({ action: 'incident.open', actor: 'system:detect', incident_id: incident.id });
      // Reading it acknowledged nothing.
      expect((await client.incident(incident.id)).state).toBe('open');

      expect(await client.acknowledgeIncident(incident.id)).toMatchObject({ state: 'acknowledged', acknowledged_by: expect.any(String), acknowledged_at: expect.any(String) });
      expect((await client.disableKey(key.id, incident.id)).status).toBe('disabled');
      expect((await client.enableKey(key.id)).status).toBe('active');
      const rotated = await client.rotateKey(key.id, incident.id);
      expect(rotated).toMatchObject({ key: { id: key.id }, secret: expect.stringMatching(/^kmn_/), sdk_env: expect.any(String) });
      const done = await client.incident(incident.id);
      expect(done).toMatchObject({ state: 'resolved', resolution: 'confirmed', resolved_by: expect.any(String), resolved_at: expect.any(String) });
      expect(done.actions.map((a) => a.action)).toEqual(expect.arrayContaining(['incident.acknowledge', 'key.disable', 'key.rotate', 'incident.resolve']));
      expect(done.actions.every((a) => a.incident_id === incident.id)).toBe(true);
      await expect(client.disableKey(key.id, incident.id)).rejects.toMatchObject({ code: 'conflict' });
      await client.revokeKey(key.id);
    }, 90_000);

    it('revokes for an incident, and answers another with this was me', async () => {
      const first = await leakedKey('revoke');
      expect((await client.revokeKey(first.key.id, first.incident.id)).status).toBe('revoked');
      expect(await client.incident(first.incident.id)).toMatchObject({ state: 'resolved', resolution: 'confirmed' });

      const second = await leakedKey('me');
      const got = await client.thisWasMe(second.incident.id, { add_known_sources: true, kinds: ['client'] });
      expect(got.incident).toMatchObject({ state: 'resolved', resolution: 'false_positive' });
      expect(got.added).toEqual([expect.objectContaining({ kind: 'client', value: 'curl/8', id: expect.any(Number), label: expect.any(String) })]);
      await client.revokeKey(second.key.id);

      const third = await leakedKey('plain');
      expect((await client.thisWasMe(third.incident.id, { add_known_sources: false })).added).toEqual([]);
      await client.revokeKey(third.key.id);
    }, 90_000);

    it('resolves a canary incident and arms the canary again', async () => {
      const proxy = (await client.createKey({ upstream: upstreamName, name: `tui-live-proxy-${Date.now()}`, ttl_seconds: 3600 }));
      const canary = await admin<{ canary: { id: string }; secret: string }>('POST', '/canaries', { name: `tui-live-canary-${Date.now()}`, planted: 'the live test', format: 'env' });
      await use(proxy.proxy_url!, canary.secret, 'curl/8');
      const incident = await incidentOf(canary.canary.id);
      const detail = await client.incident(incident.id);
      expect(detail).toMatchObject({ rules: ['canary'], severity: 'critical', known_source_candidates: [] });
      expect(detail.detections[0]!.evidence.detail).toContain('the live test');
      expect(await client.resolveIncident(incident.id, 'false_positive')).toMatchObject({ state: 'resolved', resolution: 'false_positive' });
      expect(await client.resetCanary(canary.canary.id)).toMatchObject({ id: canary.canary.id, state: expect.any(String) });
      await client.revokeKey(canary.canary.id);
      await client.revokeKey(proxy.key.id);
    }, 90_000);
  });

  it('refuses a wrong token as unauthorized', async () => {
    const bad = createClient({ url: url!, token: 'kma_not-a-real-token' });
    await expect(bad.whoami()).rejects.toMatchObject({ code: 'unauthorized' });
  });
});
