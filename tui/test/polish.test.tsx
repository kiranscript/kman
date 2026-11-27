// The main screen's details: what leads a key, what is dim, what is never cut, and the
// words used.
import { describe, expect, it } from 'vitest';
import { App } from '../src/App.js';
import { requestsText } from '../src/format.js';
import { fakeClient, incident, place, tick, until } from './fakes.js';
import type { Fake } from './fakes.js';
import { render } from './render.js';

const SMALL = { columns: 80, rows: 24 };
const WIDE = { columns: 140, rows: 40 };
const ORG = 'Stiftung Erneuerbare Freiheit und Digitale Rechte';

function leaked(): Fake {
  const client = fakeClient();
  client.incidents = [incident({ id: 12, key_id: 'k_checkout', key_name: 'checkout' }), incident({ id: 14, key_id: 'k_ci', key_name: 'ci-evals', state: 'acknowledged' })];
  return client;
}

async function mount(client: Fake, size: typeof SMALL, ...keys: string[]) {
  const app = render(<App client={client} url="http://127.0.0.1:8081" size={size} intervalMs={60_000} />, size.columns);
  const screen = () => app.lastFrame() ?? '';
  await until(() => screen().includes('live'), 'the first load');
  for (const k of keys) {
    app.stdin.write(k);
    await tick(120);
  }
  await tick(120);
  return { ...app, screen, lines: () => screen().split('\n') };
}

describe('the head of a key’s detail', () => {
  it('is its incident, with the command dim under it, before everything else', async () => {
    for (const size of [SMALL, WIDE]) {
      const m = await mount(leaked(), size, '2');
      const lines = m.lines();
      const at = lines.findIndex((l) => l.includes('! incident #12 · Enter to answer'));
      expect(at).toBeGreaterThan(0);
      // The first line inside the detail.
      expect(lines[at - 1]).toContain('Key: checkout');
      expect(lines[at + 1]).toContain('kman incident show 12');
      // On a short screen what fired is beside the command, and a row is saved.
      expect(lines[at + (size === SMALL ? 1 : 2)]).toContain('high: new country, new ASN');
      // Said once.
      expect(m.screen().match(/incident #12/g)).toHaveLength(1);
      expect(m.screen().match(/kman incident show 12/g)).toHaveLength(1);
      m.unmount();
    }
  });

  it('marks an acknowledged one as such', async () => {
    const m = await mount(leaked(), WIDE, '2', 'j');
    expect(m.screen()).toContain('▲ incident #14 · Enter to answer');
    expect(m.screen()).toContain('high, acknowledged: new country, new ASN');
    m.unmount();
  });
});

describe('the Status title', () => {
  it('says how many incidents there are in words', async () => {
    const m = await mount(leaked(), WIDE);
    expect(m.lines()[0]).toContain('[1] Status 2 incidents');
    expect(m.lines()[0]).not.toMatch(/Status [!▲·]\d/);
    m.unmount();
    const one = leaked();
    one.incidents.pop();
    const n = await mount(one, WIDE);
    expect(n.lines()[0]).toMatch(/\[1\] Status 1 incident ─/);
    n.unmount();
  });

  it('does not say it twice on the one line a short screen leaves Status', async () => {
    const m = await mount(leaked(), SMALL, '2');
    const strip = m.lines().find((l) => l.includes('[1] Status'))!;
    expect(strip.match(/incidents/g)).toHaveLength(1);
    expect(strip).toContain('2 incidents');
    m.unmount();
  });
});

describe('an AS organisation', () => {
  const placed = () => {
    const client = fakeClient();
    client.places.k_checkout = {
      key_id: 'k_checkout',
      places: [place({ kind: 'country', value: 'DE', refused: 57 }), place({ kind: 'asn', value: '60729', label: ORG, refused: 57 }), place({ kind: 'client', value: 'curl/8' })],
    };
    return client;
  };

  it('is whole where the line has room for it, its counts under it if need be', async () => {
    for (const size of [{ columns: 130, rows: 40 }, { columns: 200, rows: 40 }, { columns: 80, rows: 40 }]) {
      const m = await mount(placed(), size, '2');
      expect(m.screen(), String(size.columns)).toContain(`AS60729 ${ORG}`);
      expect(m.screen()).toContain('57 refused');
      // Its neighbours keep their counts beside them.
      expect(m.lines().find((l) => l.includes('DE '))).toContain('57 refused');
      m.unmount();
    }
  });

  it('is cut only when even a line of its own is too short', async () => {
    const m = await mount(placed(), { columns: 100, rows: 40 }, '2');
    expect(m.screen()).toMatch(/AS60729 Stiftung[^│]*…/);
    m.unmount();
  });
});

describe('the words and the panels', () => {
  it('says requests everywhere', () => {
    expect([1, 424, 1200, 12_345, 1_200_000].map(requestsText)).toEqual(['1 request', '424 requests', '1.2k requests', '12k requests', '1.2M requests']);
  });

  it('says how old the prices are', async () => {
    const m = await mount(fakeClient(), WIDE);
    expect(m.screen()).toContain('prices 3d old');
    m.unmount();
  });
});
