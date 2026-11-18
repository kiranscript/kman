import { render } from 'ink-testing-library';
import { describe, expect, it } from 'vitest';
import { ApiError } from '../src/api.js';
import { App } from '../src/App.js';
import { fakeClient, key, tick, until } from './fakes.js';

// ink-testing-library's stdout is 100 columns wide.
const SIZE = { columns: 100, rows: 34 };
const ENTER = '\r';
const ESC = '\u001b';
const CTRL_S = '\u0013';

function mount(client = fakeClient(), size = SIZE, copy?: (text: string) => void, noticeMs?: number) {
  const element = (sz: typeof SIZE) => (
    <App client={client} url="http://127.0.0.1:8081" size={sz} intervalMs={60_000} copy={copy} noticeMs={noticeMs} />
  );
  const app = render(element(size));
  const resize = (sz: typeof SIZE) => app.rerender(element(sz));
  const screen = () => app.lastFrame() ?? '';
  // Ink draws at most ~30 times a second, so after a key wait for the frame to stop changing.
  const press = async (keys: string) => {
    app.stdin.write(keys);
    await tick(40);
    for (let same = 0, last = screen(); same < 2; ) {
      await tick(25);
      const now = screen();
      same = now === last ? same + 1 : 0;
      last = now;
    }
  };
  return { ...app, press, screen, client, resize };
}

async function loaded(m: ReturnType<typeof mount>) {
  await until(() => m.screen().includes('live'), 'the first load');
}

describe('App', () => {
  it('shows the install at a glance, with what needs attention', async () => {
    const m = mount();
    await loaded(m);
    const s = m.screen();
    expect(s).toContain('owner @ http://127.0.0.1:8081');
    expect(s).toContain('2 active');
    expect(s).toContain('NEEDS ATTENTION');
    expect(s).toContain('ci-evals');
    expect(s).toContain('90% of its daily cap');
    m.unmount();
  });

  it('lists keys with status words as well as colour', async () => {
    const m = mount();
    await loaded(m);
    await m.press('2');
    const s = m.screen();
    expect(s).toContain('● active');
    // The list says it in a glyph and a word, the detail pane in both.
    expect(s.split('\n').find((l) => l.includes('old-proto'))).toMatch(/◐.*disabled/);
    expect(s).toContain('$0.108 / $5');
    m.unmount();
  });

  it('disables the selected key only after a yes', async () => {
    const m = mount();
    await loaded(m);
    await m.press('2');
    await m.press('d');
    expect(m.screen()).toContain('Disable this key?');
    await m.press('n');
    expect(m.client.calls).toEqual([]);
    await m.press('d');
    await m.press('y');
    await until(() => m.client.calls.length > 0, 'the disable call');
    expect(m.client.calls).toEqual(['disable k_checkout']);
    await until(() => m.screen().includes('Disabled checkout.'), 'the notice');
    m.unmount();
  });

  it('keeps the selection on the key when the list re-sorts', async () => {
    const m = mount();
    await loaded(m);
    await m.press('2');
    await m.press('d');
    await m.press('y');
    await until(() => m.screen().includes('Disabled checkout.'), 'the notice');
    // checkout is now the second of three; the selection followed it.
    expect(m.screen()).toContain('Key: checkout');
    expect(m.screen()).toContain('2 of 3');
    m.unmount();
  });

  it('will not revoke without the key name typed in full', async () => {
    const m = mount();
    await loaded(m);
    await m.press('2');
    await m.press('x');
    expect(m.screen()).toContain('Revoke checkout?');
    await m.press('chec');
    await m.press(ENTER);
    expect(m.client.calls).toEqual([]);
    await m.press('kout');
    await m.press(ENTER);
    await until(() => m.client.calls.length > 0, 'the revoke call');
    expect(m.client.calls).toEqual(['revoke k_checkout']);
    m.unmount();
  });

  it('shows an API refusal inside the revoke dialog and stays open', async () => {
    const client = fakeClient();
    client.revokeKey = async () => {
      throw new ApiError('conflict', 'something is in the way');
    };
    const m = mount(client);
    await loaded(m);
    await m.press('2');
    await m.press('x');
    await m.press('checkout');
    await m.press(ENTER);
    await until(() => m.screen().includes('something is in the way'), 'the error');
    expect(m.screen()).toContain('Revoke checkout?');
    m.unmount();
  });

  it('creates a key, shows its secret once, and clears it', async () => {
    const m = mount();
    await loaded(m);
    await m.press('n');
    expect(m.screen()).toContain('New virtual key');
    await m.press('web');
    await m.press('\t'); // name -> upstream
    await m.press('\t'); // upstream -> days
    await m.press('7');
    await m.press('\t');
    await m.press('2');
    await m.press(CTRL_S);
    await until(() => m.screen().includes('kmn_secretsecretsecret'), 'the secret');
    expect(m.client.calls[0]).toBe(
      `create ${JSON.stringify({ upstream: 'openai', name: 'web', ttl_seconds: 604800, policy: { daily_cap_micro: 2_000_000 } })}`,
    );
    expect(m.screen()).toContain('export OPENAI_API_KEY=kmn_secretsecretsecret');
    await m.press(ENTER);
    await until(() => !m.screen().includes('kmn_secretsecretsecret'), 'the secret to be cleared');
    m.unmount();
  });

  it('refuses a bad value in the form and does not call the API', async () => {
    const m = mount();
    await loaded(m);
    await m.press('n');
    await m.press('.leading-dot');
    await m.press(CTRL_S);
    expect(m.screen()).toContain('A name starts with a letter or digit.');
    expect(m.client.calls).toEqual([]);
    m.unmount();
  });

  it('rejects a monthly cap below the daily cap', async () => {
    const m = mount();
    await loaded(m);
    await m.press('n');
    await m.press('\t');
    await m.press('\t');
    await m.press('\t'); // daily cap
    await m.press('5');
    await m.press('\t'); // monthly cap
    await m.press('2');
    await m.press(CTRL_S);
    expect(m.screen()).toContain('at least the daily cap');
    expect(m.client.calls).toEqual([]);
    m.unmount();
  });

  it('filters keys', async () => {
    const m = mount();
    await loaded(m);
    await m.press('2');
    await m.press('/');
    await m.press('ci-');
    const s = m.screen();
    expect(s).toContain('Keys (1/3)');
    expect(s).toContain('/ ci-');
    expect(s).not.toContain('old-proto');
    await m.press(ESC);
    expect(m.screen()).toContain('old-proto');
    m.unmount();
  });

  it('keeps showing data, marked as old, when the connection drops', async () => {
    const client = fakeClient();
    const m = mount(client);
    await loaded(m);
    client.fail = new ApiError('unreachable', 'cannot reach http://127.0.0.1:8081: connection refused');
    await m.press('r');
    await until(() => m.screen().includes('offline'), 'the lost-connection flag');
    expect(m.screen()).toContain('ci-evals');
    client.fail = undefined;
    await m.press('r');
    await until(() => m.screen().includes('live'), 'recovery');
    m.unmount();
  });

  it('explains a refused token and offers only quit', async () => {
    const client = fakeClient();
    client.fail = new ApiError('unauthorized', 'the admin token is unknown or revoked', 401);
    const m = mount(client);
    await until(() => m.screen().includes('refused this token'), 'the refusal');
    expect(m.screen()).toContain('kman login');
    expect(m.screen()).not.toContain('new key');
    m.unmount();
  });

  it('explains when kman is not running', async () => {
    const client = fakeClient();
    client.fail = new ApiError('unreachable', 'cannot reach http://127.0.0.1:8081: connection refused');
    const m = mount(client);
    await until(() => m.screen().includes('Cannot reach kman'), 'the explanation');
    expect(m.screen()).toContain('kman serve');
    m.unmount();
  });

  it('asks for a bigger terminal instead of drawing a broken one', async () => {
    const m = mount(fakeClient(), { columns: 60, rows: 12 });
    expect(m.screen()).toContain('Terminal too small');
    m.unmount();
  });

  it('fits an 80x24 terminal: every line within 80 columns and the footer intact', async () => {
    const m = mount(fakeClient(), { columns: 80, rows: 24 });
    await loaded(m);
    for (const tab of ['1', '2', '3', '4']) {
      await m.press(tab);
      const lines = m.screen().split('\n');
      expect(lines.length).toBeLessThanOrEqual(24);
      for (const l of lines) expect([...l].length).toBeLessThanOrEqual(80);
      expect(lines.join('\n')).toContain('q quit');
    }
    m.unmount();
  });

  it('shows no revoke or disable hint on a revoked key', async () => {
    const client = fakeClient();
    client.keys[0]!.status = 'revoked';
    const m = mount(client);
    await loaded(m);
    await m.press('2');
    // Revoked sorts last; go there.
    await m.press('G');
    const s = m.screen();
    expect(s.split('\n').at(-1)).not.toContain('revoke');
    await m.press('d');
    expect(s).not.toContain('Disable');
    m.unmount();
  });

  describe('review fixes', () => {
    it('does nothing behind the "too small" screen, then shows the prompt again', async () => {
      const m = mount();
      await loaded(m);
      await m.press('2');
      await m.press('d');
      expect(m.screen()).toContain('Disable this key?');
      m.resize({ columns: 60, rows: 12 });
      await until(() => m.screen().includes('Terminal too small'), 'the size gate');
      await m.press('y'); // would confirm the disable if it were let through
      await m.press('n');
      await m.press('x');
      expect(m.client.calls).toEqual([]);
      m.resize(SIZE);
      await until(() => m.screen().includes('Disable this key?'), 'the prompt to come back');
      expect(m.client.calls).toEqual([]);
      m.unmount();
    });

    it('keeps the one-time secret on Esc, and only Enter closes it', async () => {
      const m = mount();
      await loaded(m);
      await m.press('n');
      await m.press(CTRL_S);
      await until(() => m.screen().includes('kmn_secretsecretsecret'), 'the secret');
      await m.press(ESC);
      expect(m.screen()).toContain('kmn_secretsecretsecret');
      expect(m.screen()).toContain('Esc does not close this');
      await m.press(ENTER);
      await until(() => !m.screen().includes('kmn_secretsecretsecret'), 'the secret to be cleared');
      m.unmount();
    });

    it('shows the secret and the export line unboxed, so they cannot wrap or pick up a border', async () => {
      const m = mount(fakeClient(), { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('n');
      await m.press(CTRL_S);
      await until(() => m.screen().includes('kmn_secretsecretsecret'), 'the secret');
      const lines = m.screen().split('\n');
      const secretLine = lines.find((l) => l.includes('kmn_secretsecretsecret') && !l.includes('export'));
      const exportLine = lines.find((l) => l.includes('export OPENAI_API_KEY='));
      expect(secretLine).not.toMatch(/[│╭╮╰╯]/);
      expect(exportLine).toContain('export OPENAI_API_KEY=kmn_secretsecretsecret');
      expect(exportLine).not.toMatch(/[│╭╮╰╯]/);
      m.unmount();
    });

    it('copies the secret on c', async () => {
      const copied: string[] = [];
      const m = mount(fakeClient(), SIZE, (t) => copied.push(t));
      await loaded(m);
      await m.press('n');
      await m.press(CTRL_S);
      await until(() => m.screen().includes('kmn_secretsecretsecret'), 'the secret');
      await m.press('c');
      expect(copied).toEqual(['kmn_secretsecretsecret']);
      expect(m.screen()).toContain('clipboard');
      m.unmount();
    });

    it('counts every row of Needs attention, in the panel and on the tab', async () => {
      const client = fakeClient();
      client.keys.push({ ...client.keys[0]!, id: 'k_nocap', name: 'uncapped', spend: undefined });
      const m = mount(client);
      await loaded(m);
      const s = m.screen();
      // ci-evals (90% of its cap) and uncapped (no cap): two rows, so two everywhere.
      expect(s).toContain('NEEDS ATTENTION (2)');
      expect(s).toContain('[1] Status ▲1');
      expect(s).toContain('1 of 2');
      expect(s).toContain('uncapped');
      expect(s).toContain('no cap');
      m.unmount();
    });

    it('opens the selected Needs attention key with Enter', async () => {
      const m = mount();
      await loaded(m);
      await m.press(ENTER);
      await until(() => m.screen().includes('Key: ci-evals'), 'the key to open');
      expect(m.screen()).toContain('2 of 3');
      m.unmount();
    });

    it('says when keys are scrolled out of sight', async () => {
      const client = fakeClient();
      for (let i = 0; i < 14; i++) client.keys.push({ ...client.keys[0]!, id: `k_${i}`, name: `extra-${String(i).padStart(2, '0')}` });
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('2');
      expect(m.screen()).toContain('1 of 17');
      await m.press('G');
      expect(m.screen()).toContain('17 of 17');
      expect(m.screen()).toContain('Key: ');
      m.unmount();
    });

    it('lets only valid characters into the form, and a lone Esc does not discard it', async () => {
      const m = mount();
      await loaded(m);
      await m.press('n');
      await m.press('My-KEY!');
      expect(m.screen()).toContain('[my-key');
      await m.press('\t');
      await m.press('\t');
      await m.press('12ab');
      expect(m.screen()).toContain('[12 ');
      await m.press(ESC);
      expect(m.screen()).toContain('New virtual key');
      expect(m.screen()).toContain('Esc again discards');
      await m.press(ESC);
      await until(() => !m.screen().includes('New virtual key'), 'the form to close');
      m.unmount();
    });

    it('shows the install default expiry in the form', async () => {
      const m = mount();
      await loaded(m);
      await m.press('n');
      expect(m.screen()).toContain('default 90');
      m.unmount();
    });

    it('labels the Upstreams KEYS column as active keys', async () => {
      const m = mount();
      await loaded(m);
      await m.press('3');
      expect(m.screen()).toContain('2 active');
      m.unmount();
    });

    it('does not repeat an unnamed key id in the audit detail', async () => {
      const client = fakeClient();
      client.keys[0]!.name = undefined;
      const m = mount(client);
      await loaded(m);
      await m.press('4');
      // The audit fixture targets k_web, which is not a known key: id shown once.
      const detail = m.screen().split('\n').filter((l) => l.includes('key.create')).join(' ');
      expect(detail).not.toMatch(/k_web.*\(k_web\)/);
      m.unmount();
    });
  });

  it('never wraps the secret: a too-long export line gives way to an instruction', async () => {
    const long = `kmn_${'x'.repeat(60)}`;
    const client = fakeClient();
    const create = client.createKey;
    client.createKey = async (i) => ({ ...(await create(i)), secret: long });
    const m = mount(client, { columns: 80, rows: 24 });
    await loaded(m);
    await m.press('n');
    await m.press(CTRL_S);
    await until(() => m.screen().includes(long), 'the secret');
    const lines = m.screen().split('\n');
    expect(lines.filter((l) => l.includes(long))).toHaveLength(1);
    expect(m.screen()).not.toContain(`export OPENAI_API_KEY=`);
    expect(m.screen()).toContain('Set OPENAI_API_KEY to the secret above.');
    expect(m.screen()).toContain('Enter done');
    m.unmount();
  });

  it('ignores every key but q and r before there is anything to show', async () => {
    const client = fakeClient();
    client.fail = new ApiError('unreachable', 'cannot reach http://127.0.0.1:8081: connection refused');
    const m = mount(client);
    await until(() => m.screen().includes('Cannot reach kman'), 'the explanation');
    await m.press('?');
    await m.press('2');
    await m.press('n');
    client.fail = undefined;
    await m.press('r');
    await until(() => m.screen().includes('live'), 'recovery');
    // None of those keys took effect while nothing could be seen.
    expect(m.screen()).not.toContain('Keybindings');
    expect(m.screen()).not.toContain('New virtual key');
    expect(m.screen()).toContain('NEEDS ATTENTION');
    m.unmount();
  });

  it('tells the operator when the revoke name is wrong, and keeps a long name on its own line', async () => {
    const client = fakeClient();
    const long = 'tui-test-a-very-long-service-name-for-checking-truncation-xyz';
    client.keys[0]!.name = long;
    const m = mount(client, { columns: 80, rows: 24 });
    await loaded(m);
    await m.press('2');
    await m.press('j'); // active keys sort by name: ci-evals, then the long one
    await m.press('x');
    expect(m.screen().split('\n').some((l) => l.includes(long))).toBe(true);
    await m.press('nope');
    await m.press(ENTER);
    expect(m.screen()).toContain('That is not the name');
    expect(m.client.calls).toEqual([]);
    m.unmount();
  });

  it('lists what moves and what the marks mean in help', async () => {
    const m = mount(fakeClient(), { columns: 80, rows: 24 });
    await loaded(m);
    await m.press('?');
    const s = m.screen();
    expect(s).toContain('PgUp PgDn');
    expect(s).toContain('MARKS');
    expect(s.split('\n').length).toBeLessThanOrEqual(24);
    expect(s).toContain('Keybindings');
    m.unmount();
  });

  it('keeps a failure on screen until the next key press, while a success fades', async () => {
    const client = fakeClient();
    const m = mount(client, SIZE, undefined, 60);
    await loaded(m);
    await m.press('2');
    await m.press('d');
    await m.press('y');
    await until(() => m.screen().includes('Disabled checkout.'), 'the success notice');
    await until(() => !m.screen().includes('Disabled checkout.'), 'the success notice to fade');

    client.disableKey = async () => {
      throw new ApiError('unreachable', 'cannot reach http://127.0.0.1:8081: connection refused');
    };
    await m.press('G'); // not the disabled one: pick an active key
    await m.press('g');
    await m.press('d');
    await m.press('y');
    await until(() => m.screen().includes('Could not disable'), 'the failure notice');
    await tick(400); // well past the 60 ms a success would last
    expect(m.screen()).toContain('Could not disable');
    await m.press('j');
    await until(() => !m.screen().includes('Could not disable'), 'the failure to clear on a key press');
    m.unmount();
  });

  it('always says where the app should point, even with no base path', async () => {
    const m = mount(fakeClient(), { columns: 80, rows: 24 });
    await loaded(m);
    await m.press('n');
    await m.press(CTRL_S);
    await until(() => m.screen().includes('kmn_secretsecretsecret'), 'the secret');
    expect(m.screen()).toContain("Base URL: kman's proxy address + /v1 (http://127.0.0.1:8080 by default)");
    m.unmount();

    const client = fakeClient();
    const create = client.createKey;
    client.createKey = async (i) => ({ ...(await create(i)), sdk_base_path: undefined });
    const m2 = mount(client, { columns: 80, rows: 24 });
    await loaded(m2);
    await m2.press('n');
    await m2.press(CTRL_S);
    await until(() => m2.screen().includes('kmn_secretsecretsecret'), 'the secret');
    expect(m2.screen()).toContain("Base URL: kman's proxy address (http://127.0.0.1:8080 by default)");
    m2.unmount();
  });

  it('shows where the selection is in a long list, on the border, as "n of m"', async () => {
    const client = fakeClient();
    for (let i = 0; i < 12; i++) {
      client.keys.push({ ...client.keys[0]!, id: `k_${i}`, name: `uncapped-${i}`, spend: undefined });
    }
    const m = mount(client, { columns: 80, rows: 24 });
    await loaded(m);
    const lines = m.screen().split('\n');
    const bottom = lines.findIndex((l) => l.includes('1 of 13'));
    expect(lines[bottom]).toMatch(/^[│╰┃┗]/);
    // And not as a line under the list.
    expect(m.screen()).not.toMatch(/\d+ (above|below|more)/);
    m.unmount();
  });

  it('says a refresh failed when it did, and keeps saying so until a key press', async () => {
    const client = fakeClient();
    const m = mount(client);
    await loaded(m);
    client.fail = new ApiError('unreachable', 'cannot reach http://127.0.0.1:8081: connection refused');
    await m.press('r');
    await until(() => m.screen().includes('Could not refresh'), 'the failure');
    expect(m.screen()).not.toContain('Refreshed.');
    client.fail = undefined;
    await m.press('r');
    await until(() => m.screen().includes('Refreshed.'), 'a good refresh');
    m.unmount();
  });
});

describe('review round 1', () => {
  const SMALL = { columns: 80, rows: 24 };

  it('keeps every field of an upstream reachable at 80x24: the routes and the key count scroll into view', async () => {
    const m = mount(fakeClient(), SMALL);
    await loaded(m);
    await m.press('3');
    expect(m.screen()).toContain('↓ J more');
    expect(m.screen()).toContain('J/K scroll');
    expect(m.screen()).not.toContain('POST /v1/chat/completions');
    await m.press('J');
    expect(m.screen()).toContain('POST /v1/chat/completions');
    expect(m.screen()).toContain('3 keys');
    await m.press('K');
    expect(m.screen()).toContain('address');
    m.unmount();
  });

  it('keeps the client address of an audit entry reachable at 80x24', async () => {
    const client = fakeClient();
    client.latestAudit = async () => [
      { id: 1, time: '2026-11-17T12:00:00Z', actor: 'owner', action: 'key.create', target_kind: 'key', target_id: 'k_web', client_ip: '203.0.113.7' },
    ];
    const m = mount(client, SMALL);
    await loaded(m);
    await m.press('4');
    if (!m.screen().includes('203.0.113.7')) await m.press('J');
    expect(m.screen()).toContain('203.0.113.7');
    m.unmount();
  });

  it('shows a key note at 80x24, scrolling if it has to', async () => {
    const client = fakeClient();
    client.keys[0]!.note = 'owned by the checkout team';
    const m = mount(client, SMALL);
    await loaded(m);
    await m.press('2');
    if (!m.screen().includes('owned by the checkout team')) await m.press('J');
    expect(m.screen()).toContain('owned by the checkout team');
    m.unmount();
  });

  it('shows the price data age in the Status panel, at every size, and says when it is stale', async () => {
    for (const [size, text] of [
      [SMALL, /price 3d/],
      [{ columns: 100, rows: 24 }, /price (3d|data 3 days old)/],
      [SIZE, /price data 3 days old/],
    ] as const) {
      const m = mount(fakeClient(), size);
      await loaded(m);
      expect(m.screen()).toMatch(text);
      m.unmount();
    }
    for (const size of [SMALL, { columns: 100, rows: 24 }, SIZE]) {
      const client = fakeClient();
      client.priceStatus = async () => ({ age_days: 70, generated_at: '2026-09-01T00:00:00Z', stale: true });
      const m = mount(client, size);
      await loaded(m);
      expect(m.screen()).toMatch(/▲ price stale/);
      m.unmount();
    }
  });

  it('shows the counts and the spend at 80x24 and 100x24, where Status has room for one line', async () => {
    for (const size of [SMALL, { columns: 100, rows: 24 }]) {
      const m = mount(fakeClient(), size);
      await loaded(m);
      expect(m.screen()).toContain('2 active');
      expect(m.screen()).toContain('today');
      m.unmount();
    }
  });

  it('draws an empty filter result and the filter on separate rows at 80x24, while typing and after Enter', async () => {
    const m = mount(fakeClient(), SMALL);
    await loaded(m);
    await m.press('2');
    await m.press('/');
    await m.press('zzz');
    const rows = () => m.screen().split('\n');
    expect(rows().some((l) => l.includes('No key matches.'))).toBe(true);
    expect(rows().find((l) => l.includes('/ zzz'))).not.toContain('No key matches');
    await m.press(ENTER);
    expect(rows().some((l) => l.includes('No key matches.'))).toBe(true);
    expect(rows().find((l) => l.includes('/ zzz'))).not.toContain('No key matches');
    m.unmount();
  });

  it('keeps the cursor in view for a wide-character query', async () => {
    const m = mount();
    await loaded(m);
    await m.press('2');
    await m.press('/');
    await m.press('界'.repeat(30));
    const line = m.screen().split('\n').find((l) => l.includes('/ ')) ?? '';
    expect(line).toContain('…');
    expect(line.endsWith('│')).toBe(true);
    m.unmount();
  });

  it('names the most urgent risk of a key, with its own glyph', async () => {
    const client = fakeClient();
    client.keys[0] = {
      ...client.keys[0]!,
      expires_at: new Date(Date.parse('2026-11-17T12:30:00Z') + 4 * 86_400_000).toISOString(),
      spend: {
        today: { period: 'd', cost_micro: 5_250_000, requests: 9, cap_micro: 5_000_000 },
        month: { period: 'm', cost_micro: 5_250_000, requests: 9, cap_micro: 50_000_000 },
      },
    };
    const m = mount(client);
    await loaded(m);
    await m.press('2');
    const row = m.screen().split('\n').find((l) => /^[│┃] checkout/.test(l)) ?? '';
    expect(row).toContain('✖ at cap');
    expect(row).not.toContain('expiring');
    m.unmount();
  });

  it('keeps the whole of a long note and the last policy values reachable at 80x24', async () => {
    const client = fakeClient();
    const note = 'first part of the note then ' + 'word '.repeat(30) + 'THE-END';
    client.keys[0]!.note = note;
    client.keys[0]!.policy = { allowed_models: ['gpt-a', 'gpt-b'], allowed_paths: ['POST /v1/last-path'], rpm_limit: 30 };
    client.keys[0]!.tags = ['prod'];
    const m = mount(client, SMALL);
    await loaded(m);
    await m.press('2');
    const seen = new Set<string>();
    for (let i = 0; i < 8; i++) {
      for (const w of ['gpt-b', 'POST /v1/last-path', '30 requests/min', 'prod', 'created', 'THE-END', 'id k_checkout']) {
        if (m.screen().includes(w)) seen.add(w);
      }
      await m.press('J');
    }
    expect([...seen].sort()).toEqual(['30 requests/min', 'POST /v1/last-path', 'THE-END', 'created', 'gpt-b', 'id k_checkout', 'prod'].sort());
    m.unmount();
  });

  it('keeps an upstream warning on its row when the name is long', async () => {
    const client = fakeClient();
    client.listUpstreams = async () => [
      { ...(await fakeClient().listUpstreams())[0]!, name: 'test-upstream-with-a-long-name', credentials: [] },
    ];
    const m = mount(client);
    await loaded(m);
    await m.press('3');
    const row = m.screen().split('\n').find((l) => /^[│┃] test-upstream/.test(l)) ?? '';
    expect(row).toContain('▲ no credential');
    m.unmount();
  });

  it('keeps the stale price warning on the collapsed Status line while another panel has focus', async () => {
    for (const size of [{ columns: 100, rows: 24 }, SIZE, { columns: 120, rows: 40 }]) {
      const client = fakeClient();
      client.priceStatus = async () => ({ age_days: 70, generated_at: '2026-09-01T00:00:00Z', stale: true });
      const m = mount(client, size);
      await loaded(m);
      await m.press('2');
      expect(m.screen()).toContain('▲ price stale');
      m.unmount();
    }
  });

  it('keeps a key warning on its row beside a long name, ahead of the spend', async () => {
    const client = fakeClient();
    const name = 'checkout-service-with-a-fairly-long-name';
    client.keys[0] = {
      ...client.keys[0]!,
      name,
      spend: {
        today: { period: 'd', cost_micro: 108_000, requests: 3, cap_micro: 5_000_000, unmetered_requests: 2 },
        month: { period: 'm', cost_micro: 108_000, requests: 3, cap_micro: 50_000_000 },
      },
    };
    const m = mount(client);
    await loaded(m);
    await m.press('2');
    const row = m.screen().split('\n').find((l) => /^[│┃] checkout-/.test(l)) ?? '';
    expect(row).toContain('● active');
    expect(row).toContain('▲ unmetered');
    m.unmount();
  });

  it('shows the full name of a key in its detail, even when two names differ only at the end', async () => {
    const client = fakeClient();
    const base = 'service-with-a-very-long-name-that-goes-on-and-on-and-on-and-on';
    client.keys[0]!.name = `${base}-a`;
    client.keys[1]!.name = `${base}-b`;
    const m = mount(client, { columns: 100, rows: 24 });
    await loaded(m);
    await m.press('2');
    // Only the main panel, which starts after the 38-column sidebar.
    const flat = () => m.screen().split('\n').map((l) => l.slice(38)).join('').replace(/[\s│┃]/g, '');
    expect(flat()).toContain(`${base}-a`);
    await m.press('j');
    expect(flat()).toContain(`${base}-b`);
    m.unmount();
  });

  it('stops offering J/K when a filter leaves no key to show', async () => {
    const m = mount(fakeClient(), SMALL);
    await loaded(m);
    await m.press('2');
    expect(m.screen()).toContain('J/K scroll');
    await m.press('/');
    await m.press('zzz');
    await m.press(ENTER);
    expect(m.screen()).toContain('No key selected.');
    expect(m.screen()).not.toContain('J/K scroll');
    m.unmount();
  });

  it('keeps the account totals on a short screen with 0, 1 or many keys needing attention, and says the price is stale once', async () => {
    for (const size of [SMALL, { columns: 100, rows: 24 }]) {
      for (const extra of [0, 1, 12]) {
        for (const stale of [false, true]) {
          const client = fakeClient();
          client.keys = [
            key({ id: 'k_a', name: 'a', spend: { today: { period: 'd', cost_micro: 200_000, requests: 1, cap_micro: 5_000_000 }, month: { period: 'm', cost_micro: 12_340_000, requests: 1, cap_micro: 50_000_000 } } }),
            key({ id: 'k_b', name: 'b', status: 'disabled', spend: undefined }),
            key({ id: 'k_c', name: 'c', status: 'revoked', spend: undefined }),
            // No spend cap: each one needs attention.
            ...Array.from({ length: extra }, (_, i) => key({ id: `k_u${i}`, name: `uncapped-${i}`, spend: undefined })),
          ];
          client.priceStatus = async () => ({ age_days: stale ? 70 : 3, generated_at: '2026-09-01T00:00:00Z', stale });
          const m = mount(client, size);
          await loaded(m);
          const s = m.screen();
          const label = `${size.columns}x${size.rows} extra=${extra} stale=${stale}`;
          expect(s, label).toContain('$12.34');
          expect(s, label).toContain('1 disabled');
          expect(s, label).toContain('1 revoked');
          expect(s, label).toContain(stale ? '▲ price' : 'price');
          expect(s.match(/▲ price/g) ?? [], label).toHaveLength(stale ? 1 : 0);
          if (extra > 0) expect(s, label).toContain('need attention');
          m.unmount();
        }
      }
    }
  });

  it('prints a key name in full in the detail from the length where the title starts to clip', async () => {
    for (const len of [50, 51, 52, 53, 54, 55]) {
      const client = fakeClient();
      const base = 'x'.repeat(len - 1);
      client.keys[0]!.name = `${base}a`;
      client.keys[1]!.name = `${base}b`;
      const m = mount(client, { columns: 100, rows: 24 });
      await loaded(m);
      await m.press('2');
      const flat = () => m.screen().split('\n').map((l) => l.slice(38)).join('').replace(/[\s│┃]/g, '');
      expect(flat()).toContain(`Key:${base}a`.length > 0 ? `${base}a` : '');
      // Either the title carries the whole name, or the body does.
      const title = m.screen().split('\n')[0]!.slice(38);
      expect(title.includes(`${base}a`) || flat().replace(/Key:[^╮]*/, '').includes(`${base}a`)).toBe(true);
      m.unmount();
    }
  });

  it('keeps / filter in the footer at 80 columns', async () => {
    const m = mount(fakeClient(), SMALL);
    await loaded(m);
    await m.press('2');
    expect(m.screen().split('\n').at(-1)).toContain('/ filter');
    m.unmount();
  });

  it('leads the help with the focused panel\'s keys', async () => {
    const m = mount(fakeClient(), SMALL);
    await loaded(m);
    await m.press('2');
    await m.press('?');
    const s = m.screen();
    expect(s.indexOf('KEYS (this panel)')).toBeGreaterThan(-1);
    expect(s.indexOf('KEYS (this panel)')).toBeLessThan(s.indexOf('GLOBAL'));
    expect(s.split('\n').length).toBeLessThanOrEqual(24);
    m.unmount();
  });

  it('stops offering J/K on a panel that does not scroll', async () => {
    const client = fakeClient();
    client.keys = client.keys.map((k) => ({ ...k, spend: { ...k.spend! } }));
    client.keys.forEach((k) => (k.spend!.today = { ...k.spend!.today, cap_micro: 1_000_000_000 }));
    client.keys.forEach((k) => (k.spend!.month = { ...k.spend!.month, cap_micro: 1_000_000_000 }));
    const m = mount(client, SMALL);
    await loaded(m);
    await m.press('3');
    expect(m.screen()).toContain('J/K scroll');
    await m.press('1');
    expect(m.screen()).not.toContain('J/K scroll');
    m.unmount();
  });

  it('names the whole key in the disable confirmation, even when two names differ only at the end', async () => {
    const client = fakeClient();
    const base = 'service-with-a-very-long-name-that-goes-on-and-on-and-on-and-on';
    client.keys[0]!.name = `${base}-a`;
    client.keys[1]!.name = `${base}-b`;
    const m = mount(client, SMALL);
    await loaded(m);
    await m.press('2');
    await m.press('d');
    const first = m.screen();
    expect(first).toContain('Disable this key?');
    expect(first.replace(/\s+/g, '')).toContain(`${base}-a`.replace(/\s+/g, ''));
    await m.press('n');
    await m.press('j');
    await m.press('d');
    expect(m.screen().replace(/\s+/g, '')).toContain(`${base}-b`.replace(/\s+/g, ''));
    m.unmount();
  });

  it('pairs each status mark with its word in the Keys list', async () => {
    const m = mount();
    await loaded(m);
    await m.press('2');
    const rows = m.screen().split('\n');
    expect(rows.find((l) => /^[│┃] checkout/.test(l))).toContain('● active');
    expect(rows.find((l) => /^[│┃] ci-evals/.test(l))).toMatch(/▲ near cap/);
    expect(rows.find((l) => /^[│┃] old-proto/.test(l))).toContain('◐ disabled');
    m.unmount();
  });

  it('keeps the end of a long filter and its cursor in view', async () => {
    const m = mount();
    await loaded(m);
    await m.press('2');
    await m.press('/');
    await m.press('abcdefghijklmnopqrstuvwxyz0123456789END');
    const line = m.screen().split('\n').find((l) => l.includes('/ ')) ?? '';
    expect(line).toContain('END');
    expect(line).toContain('…');
    m.unmount();
  });

  it('shows the keys each state really takes in the bottom bar', async () => {
    const m = mount();
    await loaded(m);
    await m.press('n');
    expect(m.screen()).toContain('Ctrl+S create');
    expect(m.screen()).toContain('Esc cancel');
    await m.press(ESC);
    await m.press(ESC);
    await m.press('2');
    await m.press('x');
    expect(m.screen()).toContain('Enter revoke');
    m.unmount();

    const client = fakeClient();
    client.fail = new ApiError('unauthorized', 'the admin token is unknown or revoked', 401);
    const denied = mount(client);
    await until(() => denied.screen().includes('refused this token'), 'the refusal');
    expect(denied.screen()).toContain('q quit');
    expect(denied.screen()).not.toContain('r retry');
    denied.unmount();
  });

  it('scrolls only when there is something to scroll, and starts again from the top for the next item', async () => {
    const m = mount(fakeClient(), SMALL);
    await loaded(m);
    await m.press('3');
    await m.press('J');
    expect(m.screen()).toContain('↑ K back');
    await m.press('4');
    expect(m.screen()).not.toContain('↑ K back');
    m.unmount();
  });
});

describe('NO_COLOR', () => {
  it('removes every colour from the theme and leaves the rest of the styling to the app', async () => {
    const { vi } = await import('vitest');
    vi.resetModules();
    process.env.NO_COLOR = '1';
    try {
      const { color, ratioColor } = await import('../src/ui/theme.js');
      expect(Object.values(color).every((v) => v === undefined)).toBe(true);
      expect(ratioColor(1.2)).toBeUndefined();
    } finally {
      delete process.env.NO_COLOR;
      vi.resetModules();
    }
  });
});