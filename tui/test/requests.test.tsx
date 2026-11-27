// The requests panel in the app: the table, its keys, and how it holds its place as
// requests arrive.
import { describe, expect, it, vi } from 'vitest';
import { ApiError } from '../src/api.js';
import { App } from '../src/App.js';
import { columns } from '../src/views/requests.js';
import { fakeClient, incident, key, leak, NOW, request, tick, until } from './fakes.js';
import type { Fake } from './fakes.js';
import { render } from './render.js';

const SMALL = { columns: 80, rows: 24 };
const WIDE = { columns: 200, rows: 40 };
const ENTER = '\r';
const ESC = '\u001b';
const bad = { key_id: 'k_prod', key_name: 'prod-api', country: 'NL', asn: 60729, asn_label: 'Stiftung Erneuerbare Freiheit', client: 'curl/8', kind: 'refused' as const, status: 403, refusal_code: 'address_not_allowed' };

/** prod-api leaked, a canary used, a probe and some ordinary traffic. */
function traffic(): Fake {
  const client = fakeClient();
  client.keys.push(key({ id: 'k_prod', name: 'prod-api' }));
  client.incidents = [incident({ id: 12, key_id: 'k_prod', key_name: 'prod-api', rules: ['new_country', 'new_asn', 'new_client'] }), incident({ id: 13, key_id: 'k_wiki', key_name: 'wiki-canary', rules: ['canary'], severity: 'critical' })];
  client.recorded = { 12: leak() };
  client.canaryIds = ['k_wiki'];
  client.requests = [
    request({ id: 1, key_id: 'k_wiki', key_name: 'wiki-canary', kind: 'refused', status: 401, refusal_code: 'invalid_key', client: 'python-requests/2', country: 'RO', asn: 9009 }, 50_000),
    request({ id: 2 }, 40_000),
    request({ id: 3, kind: 'refused', status: 401, refusal_code: 'invalid_key', unidentified_key: true, key_id: undefined, key_name: undefined, country: undefined, asn: undefined, asn_label: undefined, client: 'curl/8' }, 30_000),
    request({ id: 4, ...bad }, 20_000),
  ];
  return client;
}

function mount(client = traffic(), size = SMALL, intervalMs = 60_000) {
  const app = render(<App client={client} url="http://127.0.0.1:8081" size={size} intervalMs={intervalMs} actionGuardMs={0} />, size.columns);
  const screen = () => app.lastFrame() ?? '';
  const settle = async () => {
    await tick(40);
    for (let same = 0, last = screen(); same < 2; ) {
      await tick(25);
      const now = screen();
      same = now === last ? same + 1 : 0;
      last = now;
    }
  };
  const press = async (...keys: string[]) => {
    for (const k of keys) {
      app.stdin.write(k);
      await settle();
    }
  };
  const lines = () => screen().split('\n');
  /** The table's rows, without the lines under the selected one. */
  const table = () => lines().filter((l) => /^│.{3}(\d\d-\d\d )?\d\d:\d\d/.test(l));
  return { ...app, press, settle, screen, lines, table, client };
}

type Mounted = ReturnType<typeof mount>;

async function onRequests(m: Mounted) {
  await until(() => m.screen().includes('live'), 'the first load');
  await m.press('5');
  await until(() => m.screen().includes('prod-api') && m.screen().includes('Requests · all keys'), 'the table');
  await until(() => !m.screen().includes('flags may be missing'), 'the incident detail');
  await m.settle();
}

/** The line under the selected row, which names its operation. */
const selectedAt = (m: Mounted) => m.lines().findIndex((l) => /^│ {4}(POST|GET|no operation)/.test(l)) - 1;
const selectedRow = (m: Mounted) => m.lines()[selectedAt(m)] ?? '';

describe('the requests table', () => {
  it('lists every key’s requests newest first, flagged, at 80x24', async () => {
    const m = mount();
    await onRequests(m);
    expect(m.lines().length).toBeLessThanOrEqual(24);
    for (const l of m.lines()) expect([...l].length).toBeLessThanOrEqual(80);
    expect(m.screen()).toMatch(/TIME +KEY +FROM +CLIENT +RESULT/);
    const rows = m.table();
    expect(rows).toHaveLength(4);
    expect(rows[0]).toMatch(/^│ ! .* prod-api +NL AS60729 curl\/8 +403 #12 address_not/);
    expect(rows[1]).toMatch(/^│ ▲ .* \(no key\) +— +curl\/8 +401 invalid_key/);
    expect(rows[2]).toMatch(/^│ {3}.* checkout +US AS16509 openai-node\/4 +200/);
    expect(rows[3]).toMatch(/^│ ! .* wiki-canary RO AS9009 +python-requests\/2 +401 #13 canary/);
    // Under the selected row: the whole operation, the model and why.
    expect(m.screen()).toContain('POST /v1/chat/completions · gpt-5.4 · Stiftung Erneuerbare Freiheit');
    expect(m.screen()).toContain('! like incident #12: new country, new ASN, new client');
    expect(m.screen()).toContain('(maybe yours)');
    expect(m.screen()).toContain('f all/flagged/refused · Enter open · g newest');
    expect(m.screen()).toContain('live ·');
    m.unmount();
  });

  it('adds the organisation, the operation and the model as the screen widens', async () => {
    const m = mount(traffic(), WIDE);
    await onRequests(m);
    expect(m.screen()).toMatch(/TIME +KEY +FROM +ORGANISATION +OPERATION +MODEL +CLIENT +RESULT/);
    expect(m.screen()).toContain('Unflagged:');
    for (const l of m.lines()) expect([...l].length).toBeLessThanOrEqual(200);
    m.unmount();
  });

  it('drops the model first, then the operation, then the organisation', () => {
    const rows = [{ e: request({ id: 1 }) }];
    const at = (width: number) => columns(rows, width, NOW, false).map((c) => c.column);
    const order = ['org', 'operation', 'model'];
    let kept = 3;
    for (let width = 200; width >= 60; width--) {
      const have = order.filter((c) => at(width).includes(c as never));
      // Always the first so many of the three, and never more as it narrows.
      expect(have, String(width)).toEqual(order.slice(0, have.length));
      expect(have.length).toBeLessThanOrEqual(kept);
      kept = have.length;
      expect(at(width)).toEqual(expect.arrayContaining(['time', 'key', 'from', 'client', 'result']));
      const used = columns(rows, width, NOW, false).reduce((n, c) => n + c.width + 1, 2);
      expect(used - 1, String(width)).toBeLessThanOrEqual(width);
    }
    expect(kept).toBe(0);
    expect(columns(rows, 80, NOW, true).map((c) => c.column)).not.toContain('key');
  });

  it('has an address column only while addresses are given out, and fits it at 80x24', async () => {
    for (const exposed of [true, false]) {
      const client = traffic();
      client.exposeClients = exposed;
      client.requests = client.requests.map((e) => ({ ...e, client_address: '2001:db8:1234:5678:9abc:def0:1234:5678' }));
      const m = mount(client);
      await onRequests(m);
      expect(m.screen().includes('ADDRESS'), String(exposed)).toBe(exposed);
      if (exposed) expect(m.table()[0]).toMatch(/2001:d.*…/);
      expect(m.table()[0]).toContain('403');
      for (const l of m.lines()) expect([...l].length).toBeLessThanOrEqual(80);
      m.unmount();
    }
  });

  it('keeps the selection on the same request as others arrive, and says how many are new', async () => {
    const m = mount(traffic(), SMALL, 100);
    await onRequests(m);
    await m.press('j', 'j');
    expect(selectedRow(m)).toContain('checkout');
    m.client.requests.push(request({ id: 5, ...bad }, 1000), request({ id: 6, ...bad }, 500));
    await until(() => m.screen().includes('2 new'), 'the new rows');
    await m.settle();
    expect(selectedRow(m)).toContain('checkout');
    expect(m.table()).toHaveLength(6);
    await m.press('g');
    expect(m.screen()).not.toContain('new ·');
    expect(selectedAt(m)).toBe(m.lines().findIndex((l) => l === m.table()[0]));
    // At the newest too it stays with its request: g goes to what has come since.
    m.client.requests.push(request({ id: 7 }, 100));
    await until(() => m.screen().includes('1 new'), 'one more');
    await m.settle();
    expect(selectedRow(m)).toContain('prod-api');
    m.unmount();
  });

  it('starts at the newest again on returning, unless it was left further down', async () => {
    const m = mount(traffic(), SMALL, 100);
    await onRequests(m);
    await m.press('2');
    m.client.requests.push(request({ id: 5, key_id: 'k_ci', key_name: 'ci-evals' }, 100));
    await tick(250);
    await m.press('5');
    expect(selectedRow(m)).toContain('ci-evals');
    await m.press('j', '2', '5');
    expect(selectedRow(m)).toContain('prod-api');
    m.unmount();
  });

  it('cycles all, flagged and refused with f, asking kman for the refused', async () => {
    const client = traffic();
    const asked = vi.spyOn(client, 'events');
    const m = mount(client);
    await onRequests(m);
    await m.press('f');
    expect(m.screen()).toContain('all keys · flagged');
    expect(m.table()).toHaveLength(3);
    await m.press('f');
    expect(m.screen()).toContain('all keys · refused');
    expect(asked.mock.calls.some(([q]) => q?.kind === 'refused')).toBe(true);
    expect(m.table()).toHaveLength(3);
    await m.press('f');
    expect(m.table()).toHaveLength(4);
    m.unmount();
  });

  it('loads older requests when moved past the end', async () => {
    const client = traffic();
    client.requests = Array.from({ length: 450 }, (_, i) => request({ id: i + 1 }, (450 - i) * 1000));
    const asked = vi.spyOn(client, 'events');
    const m = mount(client);
    await until(() => m.screen().includes('live'), 'the first load');
    await m.press('5');
    await until(() => m.screen().includes('200 loaded'), 'a page');
    await m.press('G');
    expect(m.screen()).toContain('loads older');
    await m.press('j');
    await until(() => m.screen().includes('400 loaded'), 'the next page');
    expect(asked.mock.calls.some(([q]) => q?.before === 251)).toBe(true);
    await m.press('G', 'j');
    await until(() => m.screen().includes('450 loaded'), 'the last page');
    await m.settle();
    expect(m.screen()).not.toContain('loads older');
    m.unmount();
  });

  it('reads back across more than a page of new requests, leaving no gap', async () => {
    const client = traffic();
    const m = mount(client, SMALL, 100);
    await onRequests(m);
    client.requests.push(...Array.from({ length: 500 }, (_, i) => request({ id: 5 + i }, 500 - i)));
    await until(() => m.screen().includes('504 loaded'), 'all of them', 4000);
    expect(m.screen()).toContain('500 new');
    m.unmount();
  });

  it('drops a slow answer from before the filter went away and came back', async () => {
    const client = traffic();
    const events = client.events.bind(client);
    let release = () => {};
    let held = false;
    client.events = async (query) => {
      const page = await events(query);
      // The first fetch of everything after the table is up is slow, and says what was so then.
      if (!held && !query?.kind && client.requests.length === 4 && slow) {
        held = true;
        await new Promise<void>((r) => (release = r));
      }
      return page;
    };
    let slow = false;
    const m = mount(client, SMALL, 100);
    await onRequests(m);
    slow = true;
    await until(() => held, 'the slow poll');
    client.requests.push(request({ id: 5, ...bad }, 1000));
    await m.press('f', 'f', 'f');
    await until(() => m.table().length === 5, 'the newest');
    client.events = async () => new Promise(() => {});
    release();
    await tick(200);
    expect(m.table()).toHaveLength(5);
    m.unmount();
  });

  it('counts nothing as new for a move made before the selection settled', async () => {
    const client = traffic();
    client.incident = async () => {
      throw new ApiError('internal', 'database is locked', 500);
    };
    const m = mount(client);
    await until(() => m.screen().includes('live'), 'the first load');
    await m.press('5', 'f');
    await until(() => m.table().length > 1, 'the flagged rows');
    await m.settle();
    await m.press('j');
    expect(m.screen()).toContain('all keys · flagged');
    expect(m.screen()).not.toMatch(/\d new/);
    m.unmount();
  });

  it('fetches an incident again when addresses are turned on, and drops them when off', async () => {
    const client = traffic();
    client.exposeClients = false;
    client.incidents = [client.incidents[0]!];
    const network = { detections: [{ id: 1, time: new Date(NOW).toISOString(), rule: 'new_network', severity: 'low' as const, evidence: { address_withheld: true } }] };
    client.recorded = { 12: network };
    client.requests = [request({ id: 1, ...bad, kind: 'forwarded', status: 200, refusal_code: undefined, client_address: '203.0.113.7' }, 1000)];
    const m = mount(client, SMALL, 100);
    await until(() => m.screen().includes('live'), 'the first load');
    await m.press('5');
    await until(() => m.table().length === 1, 'the row');
    expect(m.table()[0]).toMatch(/^│ {3}/);
    client.exposeClients = true;
    Object.assign(network.detections[0]!.evidence, { network: '203.0.113.0/24', address_withheld: false });
    await until(() => /^│ ! /.test(m.table()[0] ?? ''), 'the flag');
    expect(m.screen()).toContain('203.0.113.7');
    // Off again, and no more events answer to say so: the row held loses its address.
    client.events = () => new Promise(() => {});
    client.exposeClients = false;
    await until(() => !m.screen().includes('203.0.113.7'), 'it gone');
    expect(m.table()).toHaveLength(1);
    m.unmount();
  });

  it('reads the rows again when addresses are turned on, keeping the selected one', async () => {
    const client = traffic();
    client.exposeClients = false;
    client.incidents = [];
    client.requests = Array.from({ length: 450 }, (_, i) => request({ id: i + 1, client_address: '203.0.113.7' }, (450 - i) * 1000));
    const m = mount(client, SMALL, 100);
    await until(() => m.screen().includes('live'), 'the first load');
    await m.press('5');
    await until(() => m.screen().includes('200 loaded'), 'a page');
    await m.press('G', 'j');
    await until(() => m.screen().includes('400 loaded'), 'the next');
    await m.press('G');
    expect(m.screen()).not.toContain('ADDRESS');
    const asked = vi.spyOn(client, 'events');
    client.exposeClients = true;
    await until(() => m.screen().includes('ADDRESS'), 'the column');
    await m.settle();
    // The selected row, three pages down, has its address and is still the selected one.
    expect(selectedRow(m)).toContain('203.0.113.7');
    expect(asked.mock.calls.some(([q]) => q?.before === 251)).toBe(true);
    m.unmount();
  });

  it('asks for a short page once it holds rows', async () => {
    const client = traffic();
    const asked = vi.spyOn(client, 'events');
    const m = mount(client, SMALL, 100);
    await onRequests(m);
    await tick(350);
    const polls = asked.mock.calls.map(([q]) => q!).filter((q) => !q.key);
    expect(polls[0]!.limit).toBe(200);
    expect(polls.at(-1)!.limit).toBe(25);
    m.unmount();
  });

  it('puts the table under the panels where an address column would leave no room', async () => {
    for (const exposed of [true, false]) {
      const client = traffic();
      client.exposeClients = exposed;
      client.requests = client.requests.map((e) => ({ ...e, client_address: '203.0.113.7' }));
      const m = mount(client, WIDE);
      await onRequests(m);
      expect(m.screen(), String(exposed)).toMatch(/OPERATION +MODEL/);
      // Beside the panels the table's title shares its line with Status.
      const title = m.lines().find((l) => l.includes('Requests · all keys'))!;
      expect(title.indexOf('Requests · all keys') > 20, String(exposed)).toBe(!exposed);
      m.unmount();
    }
  });

  it('keeps its columns where they are while scrolling', async () => {
    const client = traffic();
    client.requests = Array.from({ length: 60 }, (_, i) => request({ id: i + 1, key_name: i < 5 ? 'a-much-longer-name' : 'ci' }, (60 - i) * 1000));
    const m = mount(client);
    await until(() => m.screen().includes('live'), 'the first load');
    await m.press('5');
    await until(() => m.screen().includes('RESULT'), 'the table');
    await m.settle();
    const head = () => m.lines().find((l) => l.includes('RESULT'));
    const before = head();
    await m.press('G');
    expect(head()).toBe(before);
    m.unmount();
  });

  it('says how many are loaded without an "of" when none is flagged', async () => {
    const client = traffic();
    client.incidents = [];
    client.canaryIds = [];
    client.requests = [request({ id: 1 })];
    for (const size of [SMALL, { columns: 140, rows: 40 }]) {
      const m = mount(client, size);
      await until(() => m.screen().includes('1 loaded'), 'it');
      expect(m.screen()).not.toContain('of 1 loaded');
      m.unmount();
    }
  });

  it('keeps a space after a column that is cut in a key’s last requests', async () => {
    const client = traffic();
    client.requests = [request({ id: 1, client: 'anthropic-typescript/123456', kind: 'refused', status: 403, refusal_code: 'address_not_allowed' })];
    for (const size of [{ columns: 100, rows: 40 }, { columns: 80, rows: 40 }]) {
      const m = mount(client, size);
      await until(() => m.screen().includes('live'), 'the first load');
      await m.press('2');
      await until(() => m.screen().includes('LAST REQUESTS'), 'them');
      await m.settle();
      expect(m.screen()).toMatch(/… 403|123456 403/);
      m.unmount();
    }
  });

  it('asks for no events until a poll has succeeded', async () => {
    for (const fail of [undefined, new ApiError('unreachable', 'connection refused')]) {
      const client = traffic();
      if (fail) client.fail = fail;
      else client.listKeys = () => new Promise(() => {});
      const asked = vi.spyOn(client, 'events');
      const m = mount(client, SMALL, 100);
      await tick(300);
      expect(asked, String(fail?.message)).not.toHaveBeenCalled();
      m.unmount();
    }
  });

  it('offers v on a canary’s row in Status, and names it while its requests load', async () => {
    const client = traffic();
    client.incidents = [client.incidents[1]!];
    const events = client.events.bind(client);
    client.events = async (query) => (query?.key ? new Promise(() => {}) : events(query));
    const m = mount(client, { columns: 140, rows: 40 });
    await until(() => m.screen().includes('live'), 'the first load');
    await m.settle();
    expect(m.screen()).toContain('v requests');
    await m.press('v');
    expect(m.screen()).toContain('Requests · wiki-canary');
    m.unmount();
  });

  it('opens the incident from a flagged row, and the key from another', async () => {
    const m = mount();
    await onRequests(m);
    await m.press(ENTER);
    expect(m.screen()).toContain('Incident #12 · prod-api');
    await m.press(ESC);
    expect(m.screen()).toContain('Requests · all keys');
    await m.press('j', ENTER);
    expect(m.screen()).toContain('It presented no key kman knows');
    await m.press('j', ENTER);
    expect(m.screen()).toContain('Key: checkout');
    await m.press('5', 'G', ENTER);
    expect(m.screen()).toContain('Incident #13 · wiki-canary');
    m.unmount();
  });

  it('opens filtered to a key with v, without the key column, and Esc shows all again', async () => {
    const m = mount();
    await until(() => m.screen().includes('live'), 'the first load');
    await m.press('2', 'j', 'j', 'v');
    await until(() => m.screen().includes('Requests · prod-api') && m.screen().includes('403'), 'the table');
    await m.settle();
    expect(m.screen()).not.toMatch(/TIME +KEY/);
    expect(m.table()).toHaveLength(1);
    expect(m.screen()).toContain('Esc all keys');
    await m.press(ESC);
    await until(() => m.screen().includes('Requests · all keys') && m.table().length === 4, 'all of them');
    m.unmount();
  });

  it('shows a key’s last five requests in its detail', async () => {
    const client = traffic();
    client.requests = Array.from({ length: 8 }, (_, i) => request({ id: i + 1, status: 200 + i }, (8 - i) * 1000));
    const m = mount(client, { columns: 140, rows: 40 });
    await until(() => m.screen().includes('live'), 'the first load');
    await m.press('2');
    await until(() => m.screen().includes('LAST REQUESTS'), 'them');
    await m.settle();
    for (const status of [203, 204, 205, 206, 207]) expect(m.screen()).toMatch(new RegExp(`openai-node/4 +${status}`));
    expect(m.screen()).not.toMatch(/openai-node\/4 +202/);
    m.unmount();
  });

  it('leaves the panel out on a kman without events', async () => {
    const client = traffic();
    client.events = async () => {
      throw new ApiError('internal', 'the admin API answered 404', 404);
    };
    const m = mount(client, { columns: 140, rows: 40 });
    await until(() => m.screen().includes('live') && !m.screen().includes('[5] Requests'), 'no panel');
    await m.settle();
    expect(m.screen()).toContain('1-4 panels');
    await m.press('5');
    expect(m.screen()).not.toContain('Requests ·');
    await m.press('2', 'v');
    expect(m.screen()).not.toContain('Requests');
    expect(m.screen()).not.toContain('LAST REQUESTS');
    await m.press('?');
    expect(m.screen()).toContain('1-4  h l');
    expect(m.screen()).not.toContain('requests');
    m.unmount();
  });

  it('keeps the last rows, marked old, when they stop loading', async () => {
    const client = traffic();
    const m = mount(client, SMALL, 100);
    await onRequests(m);
    const events = client.events.bind(client);
    client.events = async () => {
      throw new ApiError('internal', 'database is locked', 500);
    };
    await until(() => m.screen().includes('Not updated: database is locked'), 'the failure');
    await m.settle();
    expect(m.table()).toHaveLength(4);
    expect(m.screen()).toContain('old ·');
    client.events = events;
    await until(() => m.screen().includes('live ·'), 'it again');
    client.fail = new ApiError('unreachable', 'cannot reach it: connection refused');
    await until(() => m.screen().includes('old ·'), 'offline');
    expect(m.table()).toHaveLength(4);
    m.unmount();
  });

  it('says flags may be missing while an incident’s detail does not load, and asks again only when it changes', async () => {
    const client = traffic();
    const detail = client.incident.bind(client);
    client.incident = async () => {
      throw new ApiError('internal', 'database is locked', 500);
    };
    const m = mount(client, SMALL, 100);
    await until(() => m.screen().includes('live'), 'the first load');
    await m.press('5');
    await until(() => m.screen().includes('flags may be missing'), 'the warning');
    const asked: number[] = [];
    client.incident = async (id) => (asked.push(id), detail(id));
    await until(() => !m.screen().includes('flags may be missing'), 'the detail');
    await tick(350);
    // A canary needs none, and an unchanged incident is not fetched again.
    expect(asked).toEqual([12]);
    client.incidents[0]!.detection_count = 9;
    await until(() => asked.length === 2, 'it again');
    m.unmount();
  });

  it('lists the keys and what the flags cannot tell in the help, within 80x24', async () => {
    const m = mount();
    await onRequests(m);
    await m.press('?');
    expect(m.lines().length).toBeLessThanOrEqual(24);
    expect(m.screen()).toContain('╭─ Keybindings');
    expect(m.screen()).toContain('all, flagged, refused');
    expect(m.screen()).toContain('past the 100 listed');
    expect(m.screen()).toContain('MARKS');
    m.unmount();
  });

  it('keeps the marks without colour', async () => {
    vi.stubEnv('NO_COLOR', '1');
    vi.resetModules();
    try {
      const { App: Plain } = await import('../src/App.js');
      const client = traffic();
      const app = render(<Plain client={client} url="http://127.0.0.1:8081" size={SMALL} intervalMs={60_000} />, 80);
      await until(() => (app.lastFrame() ?? '').includes('live'), 'the first load');
      app.stdin.write('5');
      await until(() => (app.lastFrame() ?? '').includes('#12'), 'the table');
      expect(app.lastFrame()).toMatch(/! .*prod-api/);
      expect(app.lastFrame()).toMatch(/▲ .*\(no key\)/);
      app.unmount();
    } finally {
      vi.unstubAllEnvs();
      vi.resetModules();
    }
  });
});
