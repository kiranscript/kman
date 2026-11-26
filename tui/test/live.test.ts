// The client against a real kman, so the hand-written types in src/api.ts are checked
// against the real API and not only against our own fake. It runs only when pointed at
// a server (CI starts one; see ADR 0009 and README.md):
//   KMAN_TUI_LIVE_URL=http://127.0.0.1:8081 KMAN_TUI_LIVE_TOKEN=kma_... npm test
// The server needs at least one upstream with a credential. The test creates a key,
// walks it through disable, enable and revoke, and leaves it revoked.
import { beforeAll, describe, expect, it } from 'vitest';
import { ApiError, createClient } from '../src/api.js';

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

  it('refuses a wrong token as unauthorized', async () => {
    const bad = createClient({ url: url!, token: 'kma_not-a-real-token' });
    await expect(bad.whoami()).rejects.toMatchObject({ code: 'unauthorized' });
  });
});
