import { render } from 'ink-testing-library';
import { describe, expect, it } from 'vitest';
import { ApiError } from '../src/api.js';
import { App } from '../src/App.js';
import { fakeClient, tick, until } from './fakes.js';

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
    expect(s).toContain('Needs attention');
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
    expect(s).toContain('◐ disabled');
    expect(s).toContain('$0.108 / $5');
    m.unmount();
  });

  it('disables the selected key only after a yes', async () => {
    const m = mount();
    await loaded(m);
    await m.press('2');
    await m.press('d');
    expect(m.screen()).toContain('Disable checkout?');
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
    const row = m.screen().split('\n').find((l) => l.includes('checkout') && l.includes('disabled'));
    expect(row).toContain('▌');
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
    expect(s).toContain('1 of 3 keys');
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
    await until(() => m.screen().includes('connection lost'), 'the lost-connection flag');
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
      expect(m.screen()).toContain('Disable checkout?');
      m.resize({ columns: 60, rows: 12 });
      await until(() => m.screen().includes('Terminal too small'), 'the size gate');
      await m.press('y'); // would confirm the disable if it were let through
      await m.press('n');
      await m.press('x');
      expect(m.client.calls).toEqual([]);
      m.resize(SIZE);
      await until(() => m.screen().includes('Disable checkout?'), 'the prompt to come back');
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
      expect(s).toContain('Needs attention · 2');
      expect(s).toContain('Overview ▲1 ○1');
      expect(s).toContain('no spend cap');
      m.unmount();
    });

    it('opens the selected Needs attention key with Enter', async () => {
      const m = mount();
      await loaded(m);
      await m.press(ENTER);
      await until(() => m.screen().includes('STATUS'), 'the keys table');
      const row = m.screen().split('\n').find((l) => l.includes('ci-evals') && l.includes('▌'));
      expect(row).toBeDefined();
      m.unmount();
    });

    it('says when keys are scrolled out of sight', async () => {
      const client = fakeClient();
      for (let i = 0; i < 14; i++) client.keys.push({ ...client.keys[0]!, id: `k_${i}`, name: `extra-${String(i).padStart(2, '0')}` });
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('2');
      expect(m.screen()).toMatch(/↓ \d+ more/);
      await m.press('G');
      expect(m.screen()).toMatch(/↑ \d+ more/);
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
    expect(m.screen()).not.toContain('Keyboard');
    expect(m.screen()).not.toContain('New virtual key');
    expect(m.screen()).toContain('Needs attention');
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

  it('shows what is scrolled out the same way in every view: on the title row, with arrows', async () => {
    const client = fakeClient();
    for (let i = 0; i < 12; i++) {
      client.keys.push({ ...client.keys[0]!, id: `k_${i}`, name: `uncapped-${i}`, spend: undefined });
    }
    const m = mount(client, { columns: 80, rows: 24 });
    await loaded(m);
    const lines = m.screen().split('\n');
    const title = lines.findIndex((l) => l.includes('Needs attention'));
    expect(lines[title]).toMatch(/↓ \d+ more/);
    // And not as a line under the list.
    expect(m.screen()).not.toMatch(/\d+ (above|below)/);
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

describe('NO_COLOR', () => {
  it('removes every colour from the theme and leaves the rest of the styling to the app', async () => {
    const { vi } = await import('vitest');
    vi.resetModules();
    process.env.NO_COLOR = '1';
    try {
      const { color, ratioColor } = await import('../src/ui/theme.js');
      expect(Object.values(color)).toEqual([undefined, undefined, undefined, undefined]);
      expect(ratioColor(1.2)).toBeUndefined();
    } finally {
      delete process.env.NO_COLOR;
      vi.resetModules();
    }
  });
});