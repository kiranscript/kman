import { describe, expect, it } from 'vitest';
import { ApiError } from '../src/api.js';
import { App } from '../src/App.js';
import { fakeClient, tick, until, upstream } from './fakes.js';
import { attentionRows } from '../src/views/OverviewView.js';
import { render } from './render.js';

const SIZE = { columns: 100, rows: 34 };
const ENTER = '\r';
const ESC = '\u001b';
const CTRL_S = '\u0013';

function mount(client = fakeClient(), size = SIZE, copy?: (text: string) => void, noticeMs?: number, secretGuardMs = 0) {
  const element = (sz: typeof SIZE) => (
    <App client={client} url="http://127.0.0.1:8081" size={sz} intervalMs={60_000} copy={copy} noticeMs={noticeMs} secretGuardMs={secretGuardMs} />
  );
  const app = render(element(size), size.columns);
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
    expect(m.screen()).toContain('Disable checkout (k_checkout)?');
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
      expect(m.screen()).toContain('Disable checkout (k_checkout)?');
      m.resize({ columns: 60, rows: 12 });
      await until(() => m.screen().includes('Terminal too small'), 'the size gate');
      await m.press('y'); // would confirm the disable if it were let through
      await m.press('n');
      await m.press('x');
      expect(m.client.calls).toEqual([]);
      m.resize(SIZE);
      await until(() => m.screen().includes('Disable checkout (k_checkout)?'), 'the prompt to come back');
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
    const m = mount(client, SIZE, undefined, 600);
    await loaded(m);
    await m.press('2');
    await m.press('d');
    await m.press('y');
    await until(() => m.screen().includes('Disabled checkout.'), 'the success notice');
    await until(() => !m.screen().includes('Disabled checkout.'), 'the success notice to fade', 4000);

    client.disableKey = async () => {
      throw new ApiError('unreachable', 'cannot reach http://127.0.0.1:8081: connection refused');
    };
    await m.press('G'); // not the disabled one: pick an active key
    await m.press('g');
    await m.press('d');
    await m.press('y');
    await until(() => m.screen().includes('Could not disable'), 'the failure notice');
    await tick(900); // well past the 600 ms a success would last
    expect(m.screen()).toContain('Could not disable');
    await m.press('j');
    await until(() => !m.screen().includes('Could not disable'), 'the failure to clear on a key press');
    m.unmount();
  });

  describe('second review fixes', () => {
    it('copes with an upstream that has no credential, in every view', async () => {
      const client = fakeClient();
      client.upstreams = [{ ...upstream, credentials: [] }];
      const m = mount(client);
      await loaded(m);
      for (const tab of ['1', '2', '3', '4']) {
        await m.press(tab);
        expect(m.screen()).not.toContain('Something went wrong');
      }
      await m.press('n');
      expect(m.screen()).toContain('has no credential yet');
      m.unmount();
    });

    it('asks which credential a key uses when the upstream has several, and sends it', async () => {
      const client = fakeClient();
      client.upstreams = [
        {
          ...upstream,
          credentials: [
            { id: 'cr_1', name: 'prod', created_at: '2026-11-01T00:00:00Z' },
            { id: 'cr_2', name: 'staging', created_at: '2026-11-01T00:00:00Z' },
          ],
        },
      ];
      const m = mount(client);
      await loaded(m);
      await m.press('n');
      expect(m.screen()).toContain('Credential');
      await m.press('\t'); // name -> upstream
      await m.press('\t'); // upstream -> credential
      await m.press('\u001b[C'); // right: staging
      expect(m.screen()).toContain('staging');
      await m.press(CTRL_S);
      await until(() => m.client.calls.length > 0, 'the create call');
      expect(JSON.parse(m.client.calls[0]!.replace(/^create /, ''))).toMatchObject({ upstream: 'openai', credential: 'staging' });
      m.unmount();
    });

    it('does not send a change twice when the confirm key is pressed twice', async () => {
      const client = fakeClient();
      const disable = client.disableKey;
      client.disableKey = async (id) => {
        await tick(150);
        return disable(id);
      };
      const m = mount(client);
      await loaded(m);
      await m.press('2');
      await m.press('d');
      m.stdin.write('y');
      await tick(20);
      m.stdin.write('y');
      await until(() => m.client.calls.length > 0, 'the disable call');
      await tick(300);
      expect(m.client.calls).toEqual(['disable k_checkout']);
      m.unmount();
    });

    it('shows the result of a change even when a poll was in flight when it was made', async () => {
      const client = fakeClient();
      const list = client.listKeys;
      client.listKeys = async () => {
        const snapshot = await list(); // what the server says now ...
        await tick(400); // ... delivered late
        return snapshot;
      };
      const m = mount(client);
      await loaded(m);
      await m.press('2');
      await m.press('r'); // a slow fetch starts, with checkout still active
      await m.press('d');
      await m.press('y');
      await until(
        () => m.screen().split('\n').some((l) => l.includes('checkout') && l.includes('disabled')),
        'the list to show the change',
        4000,
      );
      m.unmount();
    });
  });

  describe('ui review fixes', () => {
    const lines = (m: ReturnType<typeof mount>) => m.screen().split('\n');

    it('ignores Enter for a moment on the secret screen, so a stray Enter cannot discard it', async () => {
      const m = mount(fakeClient(), SIZE, undefined, undefined, 500);
      await loaded(m);
      await m.press('n');
      await m.press(CTRL_S);
      await until(() => m.screen().includes('kmn_secretsecretsecret'), 'the secret');
      await m.press(ENTER);
      expect(m.screen()).toContain('kmn_secretsecretsecret');
      await tick(600);
      await m.press(ENTER);
      await until(() => !m.screen().includes('kmn_secretsecretsecret'), 'the secret to be cleared');
      m.unmount();
    });

    it('keeps the status and the address apart when the connection is lost at 80 columns', async () => {
      const client = fakeClient();
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      client.fail = new ApiError('unreachable', 'cannot reach http://127.0.0.1:8081: connection refused');
      await m.press('r');
      await until(() => m.screen().includes('connection lost'), 'the lost-connection flag');
      const head = lines(m).find((l) => l.includes('connection lost'))!;
      expect(head).toMatch(/(just now|ago) {2,}\S/);
      m.unmount();
    });

    it('does not wrap a Keys row at 106 columns', async () => {
      const m = mount(fakeClient(), { columns: 106, rows: 30 });
      await loaded(m);
      await m.press('2');
      const row = lines(m).find((l) => l.includes('checkout'))!;
      expect(row).toContain('in 60d');
      const old = lines(m).find((l) => l.includes('old-proto'))!;
      expect(old).toContain('◐ disabled');
      expect(old).toContain('in 60d');
      m.unmount();
    });

    it('offers only quit on a refused token', async () => {
      const client = fakeClient();
      client.fail = new ApiError('unauthorized', 'the admin token is unknown or revoked', 401);
      const m = mount(client);
      await until(() => m.screen().includes('refused this token'), 'the refusal');
      const last = lines(m).at(-1)!;
      expect(last).toContain('q quit');
      expect(last).not.toContain('retry');
      m.unmount();
    });

    it('says why nothing can be changed while kman is not answering', async () => {
      const client = fakeClient();
      const m = mount(client);
      await loaded(m);
      client.fail = new ApiError('unreachable', 'cannot reach http://127.0.0.1:8081: connection refused');
      await m.press('r');
      await until(() => m.screen().includes('connection lost'), 'the lost-connection flag');
      await m.press('2');
      expect(lines(m).at(-1)).not.toContain('new key');
      await m.press('n');
      expect(m.screen()).not.toContain('New virtual key');
      expect(m.screen()).toContain('not answering');
      await m.press('d');
      expect(m.screen()).not.toContain('Disable checkout (k_checkout)?');
      m.unmount();
    });

    it('shows that a change is under way', async () => {
      const client = fakeClient();
      const disable = client.disableKey;
      client.disableKey = async (id) => {
        await tick(400);
        return disable(id);
      };
      const m = mount(client);
      await loaded(m);
      await m.press('2');
      await m.press('d');
      m.stdin.write('y');
      await until(() => m.screen().includes('Disabling checkout…'), 'the working notice');
      await until(() => m.screen().includes('Disabled checkout.'), 'the result');
      m.unmount();
    });

    it('scrolls Upstreams with a hint, and keeps the panel title and the update mark at 80 columns', async () => {
      const client = fakeClient();
      client.upstreams = Array.from({ length: 14 }, (_, i) => ({
        ...upstream,
        id: `up_${i}`,
        name: i === 0 ? 'a-very-long-upstream-name-indeed-yes' : `up-${i}`,
        update_available: i === 0,
        path_allowlist: Array.from({ length: 12 }, (_, r) => `POST /v1/route/number/${r}`),
      }));
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('3');
      const s = m.screen();
      expect(s).toContain('↓');
      expect(lines(m).find((l) => l.includes('a-very') && l.includes('…'))).toContain('▲');
      expect(s).toMatch(/\+\d+ more/);
      expect(lines(m).filter((l) => l.includes('a-very-long-upstream-name-indeed-yes'))).toHaveLength(1);
      expect(lines(m).every((l) => [...l].length <= 80)).toBe(true);
      m.unmount();
    });

    it('keeps audit columns apart even for the longest action names', async () => {
      const client = fakeClient();
      client.latestAudit = async () => [
        {
          id: 1,
          time: '2026-11-17T12:00:00Z',
          actor: 'ci-bot-with-long-name',
          action: 'price_override.delete',
          target_kind: 'upstream',
          target_id: 'up_x',
        },
      ];
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('4');
      const row = lines(m).find((l) => l.includes('price_override.delete'))!;
      expect(row).toMatch(/price_override\.delete +upstream/);
      expect(row).toMatch(/ci-bot-with-long… +price/);
      m.unmount();
    });

    it('points an empty install at n', async () => {
      const client = fakeClient([]);
      const m = mount(client);
      await loaded(m);
      expect(m.screen()).toContain('Press n to create one');
      m.unmount();
    });

    it('keeps the disable prompt and the r refresh hint within 80 columns', async () => {
      const m = mount(fakeClient(), { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('2');
      expect(lines(m).at(-1)).toContain('r refresh');
      await m.press('d');
      const at = lines(m).findIndex((l) => l.includes('Disable checkout (k_checkout)?'));
      expect(lines(m)[at + 1]).toContain('e re-enables');
      expect(lines(m).every((l) => [...l].length <= 80)).toBe(true);
      m.unmount();
    });

    it('closes help with Esc', async () => {
      const m = mount();
      await loaded(m);
      await m.press('?');
      expect(m.screen()).toContain('Keyboard');
      await m.press(ESC);
      expect(m.screen()).not.toContain('Keyboard');
      m.unmount();
    });
  });

  describe('second ui review fixes', () => {
    const lines = (m: ReturnType<typeof mount>) => m.screen().split('\n');

    it('keeps the time and the action apart in Recent activity for an older entry', async () => {
      const client = fakeClient();
      client.latestAudit = async () => [
        { id: 1, time: '2026-11-15T01:30:00Z', actor: 'owner', action: 'key.enable', target_kind: 'key', target_id: 'k_web' },
      ];
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      expect(lines(m).find((l) => l.includes('key.enable'))).toMatch(/\d\d-\d\d \d\d:\d\d +key\.enable/);
      m.unmount();
    });

    it('shows the audit entry\'s second line and says only the latest entries are loaded', async () => {
      const client = fakeClient();
      client.latestAudit = async () =>
        Array.from({ length: 100 }, (_, i) => ({
          id: i + 1,
          time: '2026-11-17T12:00:00Z',
          actor: 'owner',
          action: 'key.create',
          target_kind: 'key',
          target_id: 'k_web',
          client_ip: '10.0.0.7',
        }));
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('4');
      expect(m.screen()).toContain('key id k_web · from 10.0.0.7');
      expect(m.screen()).toContain('the latest 100 entries');
      m.unmount();
    });

    it('does not let one change open another dialog that claims its work', async () => {
      const client = fakeClient();
      const disable = client.disableKey;
      client.disableKey = async (id) => {
        await tick(700);
        return disable(id);
      };
      const m = mount(client);
      await loaded(m);
      await m.press('2');
      await m.press('d');
      await m.press('y');
      await m.press('x');
      expect(m.screen()).not.toContain('Revoke checkout?');
      expect(m.screen()).toContain('Wait: still disabling checkout');
      await m.press('n');
      expect(m.screen()).not.toContain('New virtual key');
      await until(() => m.screen().includes('Disabled checkout.'), 'the result');
      m.unmount();
    });

    it('fits a three-digit request count on a card, and shortens a bigger one', async () => {
      const client = fakeClient([
        {
          ...fakeClient().keys[0]!,
          spend: {
            today: { period: 'd', cost_micro: 1, requests: 424 },
            month: { period: 'm', cost_micro: 1, requests: 12_345 },
          },
        },
      ]);
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      expect(m.screen()).toContain('424 requests');
      expect(m.screen()).toContain('12k reqs');
      m.unmount();
    });

    it('does not send an empty install to n, which has nowhere to put a key', async () => {
      const client = fakeClient([]);
      client.upstreams = [];
      const m = mount(client);
      await loaded(m);
      expect(m.screen()).toContain('kman upstream add');
      expect(m.screen()).not.toContain('Press n to create');
      await m.press('3');
      expect(m.screen()).toContain('No upstreams yet');
      expect(lines(m).at(-1)).not.toContain('select');
      expect(m.screen()).not.toContain('No upstream selected');
      m.unmount();
    });

    it('shows the whole key detail under the list when there is room, and none when there is no key', async () => {
      const m = mount(fakeClient(), { columns: 100, rows: 34 });
      await loaded(m);
      await m.press('2');
      expect(m.screen()).toContain('POLICY');
      expect(m.screen()).toContain('created');
      await m.press('/');
      await m.press('zzz');
      expect(m.screen()).not.toContain('No key selected');
      expect(m.screen()).toContain('No key matches "zzz"');
      m.unmount();
    });

    it('keeps the expiry visible for a long key name at 80 columns', async () => {
      const client = fakeClient();
      client.keys[0]!.name = 'a-really-quite-long-key-name-that-goes-past-the-column-width-for-sure-yes';
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('2');
      expect(m.screen()).toMatch(/expires \d{4}-\d\d-\d\d \(in/);
      m.unmount();
    });

    it('asks for the id, not the name, when revoking an unnamed key', async () => {
      const client = fakeClient();
      client.keys[1]!.name = undefined; // k_ci, which sorts second
      const m = mount(client);
      await loaded(m);
      await m.press('2');
      await m.press('j');
      await m.press('x');
      expect(m.screen()).toContain('Revoke k_ci?');
      expect(m.screen()).toContain("Type the key's id exactly");
      m.unmount();
    });

    it('puts the login command on one line of the refused-token panel', async () => {
      const client = fakeClient();
      client.fail = new ApiError('unauthorized', 'the admin token is unknown or revoked', 401);
      const m = mount(client);
      await until(() => m.screen().includes('refused this token'), 'the refusal');
      expect(lines(m).some((l) => l.includes('kman login, or set KMAN_ADMIN_TOKEN'))).toBe(true);
      m.unmount();
    });
  });

  describe('third ui review fixes', () => {
    const lines = (m: ReturnType<typeof mount>) => m.screen().split('\n');

    it('tells apart two keys whose names start alike in the disable prompt', async () => {
      const client = fakeClient();
      client.keys[0]!.name = 'billing-reconciliation-production-eu';
      client.keys[1]!.name = 'billing-reconciliation-production-us';
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('2');
      await m.press('d');
      const prompt = lines(m).find((l) => l.startsWith(' Disable '))!;
      expect(prompt).toMatch(/\(k_ci\)\?|\(k_checkout\)\?/);
      expect([...prompt].length).toBeLessThanOrEqual(80);
      m.unmount();
    });

    it('keeps the plain-http mark and shows the full address for a long host', async () => {
      const client = fakeClient();
      client.upstreams = [{ ...upstream, host: 'ollama.internal.example.corp', port: 11434, tls: false }];
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('3');
      expect(lines(m).find((l) => l.includes('ollama.internal'))).toContain('▲http');
      expect(m.screen()).toContain('ollama.internal.example.corp:11434');
      expect(m.screen()).toContain('plain http');
      m.unmount();
    });

    it('lets the Upstreams detail use the room the list leaves, so every route can be read', async () => {
      const client = fakeClient();
      client.upstreams = [{ ...upstream, path_allowlist: Array.from({ length: 16 }, (_, i) => `POST /v1/route/${i}`) }];
      const m = mount(client, { columns: 120, rows: 34 });
      await loaded(m);
      await m.press('3');
      expect(m.screen()).toContain('POST /v1/route/15');
      expect(m.screen()).not.toMatch(/\+\d+ more/);
      m.unmount();
    });

    it('fits a stale price card at 80 columns', async () => {
      const client = fakeClient();
      client.priceStatus = async () => ({ age_days: 175, generated_at: '2026-05-01T00:00:00Z', stale: true });
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      expect(m.screen()).toContain('▲ 175d old');
      expect(m.screen()).toContain('upgrade kman');
      m.unmount();
    });

    it('widens the Audit actor column at 100 columns', async () => {
      const client = fakeClient();
      client.latestAudit = async () => [
        { id: 1, time: '2026-11-17T12:00:00Z', actor: 'ci-deploy-bot-production', action: 'key.create', target_kind: 'key', target_id: 'k_web' },
      ];
      const m = mount(client);
      await loaded(m);
      await m.press('4');
      expect(m.screen()).toContain('ci-deploy-bot-production');
      m.unmount();
    });

    it('offers only what can run: nothing to change while one is running, no new key without an upstream, no filter without keys', async () => {
      const client = fakeClient();
      const disable = client.disableKey;
      client.disableKey = async (id) => {
        await tick(700);
        return disable(id);
      };
      const m = mount(client);
      await loaded(m);
      await m.press('2');
      await m.press('d');
      await m.press('y');
      const footer = lines(m).at(-1)!;
      expect(footer).not.toContain('disable');
      expect(footer).not.toContain('revoke');
      expect(footer).not.toContain('new key');
      await until(() => m.screen().includes('Disabled checkout.'), 'the result');
      m.unmount();

      const empty = fakeClient([]);
      empty.upstreams = [];
      const m2 = mount(empty);
      await loaded(m2);
      await m2.press('2');
      expect(lines(m2).at(-1)).not.toContain('new key');
      expect(lines(m2).at(-1)).not.toContain('filter');
      m2.unmount();
    });

    it('sorts numbers in key names naturally', async () => {
      const client = fakeClient();
      const base = client.keys[0]!;
      client.keys.length = 0;
      for (const n of ['uncapped-10', 'uncapped-2', 'uncapped-1']) client.keys.push({ ...base, id: `k_${n}`, name: n });
      const m = mount(client);
      await loaded(m);
      await m.press('2');
      const order = lines(m).filter((l) => /uncapped-\d+/.test(l)).map((l) => l.match(/uncapped-\d+/)![0]);
      expect(order.slice(0, 3)).toEqual(['uncapped-1', 'uncapped-2', 'uncapped-10']);
      m.unmount();
    });

    it('shows a revoked key\'s spend the same way in the list and the detail', async () => {
      const client = fakeClient();
      client.keys[2]!.status = 'revoked';
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('2');
      await m.press('G');
      const detail = lines(m).filter((l) => l.startsWith(' │ today'));
      expect(detail[0]).toContain('·');
      expect(detail[0]).not.toContain('█');
      expect(detail[0]).not.toContain('░');
      m.unmount();
    });
  });

  describe('fourth ui review fixes', () => {
    const lines = (m: ReturnType<typeof mount>) => m.screen().split('\n');
    const twins = (client: ReturnType<typeof fakeClient>) => {
      client.keys[0]!.name = 'customer-support-summariser-production-eu-west-a';
      client.keys[1]!.name = 'customer-support-summariser-production-eu-west-b';
    };

    it('keeps both ends of a long key name, so keys sharing a prefix can be told apart, in the list and Needs attention', async () => {
      for (const size of [{ columns: 80, rows: 24 }, { columns: 120, rows: 34 }]) {
        const client = fakeClient();
        twins(client);
        const m = mount(client, size);
        await loaded(m);
        await m.press('2');
        const rows = lines(m).filter((l) => /^ [▌ ].? ?customer/.test(l) || /customer.*…/.test(l) || l.includes('customer-support'));
        expect(rows.length).toBeGreaterThanOrEqual(2);
        expect(rows.some((l) => /west-a/.test(l))).toBe(true);
        expect(rows.some((l) => /west-b/.test(l))).toBe(true);
        m.unmount();
      }
    });

    it('shows the whole name in the wide detail', async () => {
      const client = fakeClient();
      twins(client);
      const m = mount(client, { columns: 120, rows: 34 });
      await loaded(m);
      await m.press('2');
      const detail = lines(m).map((l) => l.slice(l.indexOf('│', 60) + 1)).join('');
      expect(detail.replace(/[\s│]/g, '')).toContain('customer-support-summariser-production-eu-west-');
      m.unmount();
    });

    it('keeps the reason of a failure visible, on a second row, until a key press', async () => {
      const client = fakeClient();
      twins(client);
      client.disableKey = async () => {
        throw new ApiError('conflict', 'the key was changed by someone else a moment ago; refresh and try again');
      };
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('2');
      await m.press('d');
      await m.press('y');
      await until(() => m.screen().includes('Could not disable'), 'the failure');
      expect(m.screen()).toContain('refresh and try again');
      expect(lines(m).length).toBeLessThanOrEqual(24);
      m.unmount();
    });

    it('wraps a server error in the create form instead of cutting it', async () => {
      const client = fakeClient();
      client.createKey = async () => {
        throw new ApiError('invalid', 'upstream openai has no credential named prod, and this message is long enough to need two lines in the form');
      };
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('n');
      await m.press(CTRL_S);
      await until(() => m.screen().includes('✖'), 'the error');
      expect(m.screen()).toContain('two lines in the form');
      m.unmount();
    });

    it('shows the full detail under the list at 100x34 even with many keys', async () => {
      const client = fakeClient();
      const base = client.keys[0]!;
      for (let i = 0; i < 8; i++) client.keys.push({ ...base, id: `k_more${i}`, name: `more-${i}` });
      const m = mount(client);
      await loaded(m);
      await m.press('2');
      expect(m.screen()).toContain('POLICY');
      expect(m.screen()).toMatch(/↓ \d+ more|more-7/);
      m.unmount();
    });

    it('does not clip a two-line note in the full detail', async () => {
      const client = fakeClient();
      client.keys[0]!.note = 'x'.repeat(130) + ' END';
      const m = mount(client, { columns: 100, rows: 34 });
      await loaded(m);
      await m.press('2');
      const noteRows = lines(m).filter((l) => l.includes('xxxxxxxx'));
      expect(noteRows.length).toBe(2);
      expect(m.screen()).toContain('END');
      m.unmount();
    });
  });

  describe('fifth ui review fixes', () => {
    const lines = (m: ReturnType<typeof mount>) => m.screen().split('\n');
    const note =
      'owned by the support platform team; rotate quarterly and ask in the platform channel before changing the policy or the caps, and tell finance when the monthly cap moves';
    const rich = (client: ReturnType<typeof fakeClient>) => {
      const k = client.keys[0]!;
      k.name = 'customer-support-summariser-production-eu-west-a';
      k.tags = ['support', 'prod'];
      k.credential = 'prod-primary';
      k.policy = { allowed_paths: ['POST /v1/chat/completions', 'POST /v1/embeddings'], allowed_models: ['gpt-4o', 'gpt-4o-mini'] };
      k.note = note;
    };
    const many = (client: ReturnType<typeof fakeClient>) => {
      rich(client);
      for (let i = 0; i < 12; i++) client.keys.push({ ...client.keys[2]!, id: `k_m${i}`, name: `more-${i}`, status: 'active', spend: undefined });
    };

    it('does not resize the list as the selection moves between keys of different heights', async () => {
      const client = fakeClient();
      many(client);
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('2');
      const top = () => lines(m).findIndex((l) => l.startsWith(' ╭'));
      const first = top();
      expect(first).toBeGreaterThan(0);
      for (const key of ['j', 'j', 'j', 'j', 'G', 'g']) {
        await m.press(key);
        expect(top()).toBe(first);
      }
      m.unmount();
    });

    it('shows every fact of a key at 80x24, with the note on as many rows as it needs', async () => {
      const client = fakeClient();
      rich(client);
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('2');
      await m.press('j');
      const s = m.screen();
      expect(s).toContain('tags support prod');
      expect(s).toContain('credential prod-primary');
      expect(s).toContain('paths POST /v1/chat/completions, POST /v1/embeddings');
      expect(s).toContain('created 20');
      expect(s).toContain('when the monthly cap moves');
      m.unmount();
    });

    it('ends a note that is too long with an ellipsis on its last visible line', async () => {
      const client = fakeClient();
      client.keys[0]!.note = Array.from({ length: 60 }, (_, i) => `word${i}`).join(' ');
      const m = mount(client, { columns: 100, rows: 34 });
      await loaded(m);
      await m.press('2');
      const rows = lines(m).filter((l) => /word\d+/.test(l));
      expect(rows.length).toBe(3);
      expect(rows.at(-1)).toMatch(/…\s*│$/);
      expect(rows.every((l) => !/│ {2}\S/.test(l))).toBe(true);
      m.unmount();
    });

    it('gives the wide detail\'s note all the rows the panel has left', async () => {
      const client = fakeClient();
      rich(client);
      const m = mount(client, { columns: 120, rows: 34 });
      await loaded(m);
      await m.press('2');
      await m.press('j');
      expect(m.screen()).toContain('moves');
      expect(m.screen()).toContain('POST /v1/embeddings');
      m.unmount();
    });

    it('keeps the labels in Needs attention at 120 columns, cutting names in the middle instead', async () => {
      const client = fakeClient();
      for (let i = 0; i < 3; i++) {
        client.keys.push({
          ...client.keys[0]!,
          id: `k_u${i}`,
          name: `uncapped-with-a-really-long-name-${i}`,
          policy: {},
          spend: {
            today: { period: 'd', cost_micro: 1, requests: 4, unmetered_requests: 4 },
            month: { period: 'm', cost_micro: 1, requests: 4, unmetered_requests: 4 },
          },
        });
      }
      const m = mount(client, { columns: 120, rows: 34 });
      await loaded(m);
      expect(lines(m).filter((l) => l.includes('no spend cap · 4 unmetered today')).length).toBe(3);
      m.unmount();
    });

    it('puts the Upstreams scroll hint on the list\'s title row', async () => {
      const client = fakeClient();
      client.upstreams = Array.from({ length: 14 }, (_, i) => ({ ...upstream, id: `u${i}`, name: `up-${i}` }));
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('3');
      const title = lines(m).find((l) => l.includes('14 upstreams'))!;
      expect(title).toMatch(/↓ \d+ more/);
      expect(lines(m).filter((l) => l.startsWith(' │ up-0')).join('')).not.toContain('more');
      m.unmount();
    });

    it('wraps a server error in the create form without a leading space on the next line', async () => {
      const client = fakeClient();
      client.createKey = async () => {
        throw new ApiError('invalid', 'upstream openai has no credential named prod, and this message is long enough to need two lines in the form with kman config');
      };
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('n');
      await m.press(CTRL_S);
      await until(() => m.screen().includes('✖'), 'the error');
      const row = lines(m).find((l) => l.includes('is long enough'))!;
      expect(row).toMatch(/│ {3}is long enough/);
      m.unmount();
    });

    it('does not offer a filter row when there are no keys', async () => {
      const m = mount(fakeClient([]));
      await loaded(m);
      await m.press('2');
      expect(m.screen()).not.toContain('to filter');
      m.unmount();
    });
  });

  describe('sixth ui review fixes', () => {
    const lines = (m: ReturnType<typeof mount>) => m.screen().split('\n');
    const LEFT = '\u001b[D';
    const bulk = (client: ReturnType<typeof fakeClient>, n: number) => {
      for (let i = 0; i < n; i++) client.keys.push({ ...client.keys[2]!, id: `k_m${i}`, name: `more-${i}`, status: 'active', spend: undefined });
    };

    it('keeps a useful list when one key has a large detail, at 80x24 and 100x34', async () => {
      for (const [size, rows] of [
        [{ columns: 80, rows: 24 }, 5],
        [{ columns: 100, rows: 34 }, 6],
      ] as const) {
        const client = fakeClient();
        const k = client.keys[0]!;
        k.note = Array.from({ length: 60 }, (_, i) => `word${i}`).join(' ');
        k.policy = { allowed_paths: Array.from({ length: 8 }, (_, i) => `POST /v1/route/${i}`) };
        bulk(client, 40);
        const m = mount(client, size);
        await loaded(m);
        await m.press('2');
        const listed = lines(m).filter((l) => /^ [▌ ][▲○✖ ]? ?\S.*(active|disabled|revoked)/.test(l) && !l.includes('│'));
        expect(listed.length).toBeGreaterThanOrEqual(rows);
        m.unmount();
      }
    });

    it('says how many rows the detail left out when it is short, instead of cutting silently', async () => {
      const client = fakeClient();
      const k = client.keys[0]!;
      k.note = Array.from({ length: 80 }, (_, i) => `word${i}`).join(' ');
      k.policy = { allowed_paths: Array.from({ length: 12 }, (_, i) => `POST /v1/route/${i}`) };
      bulk(client, 40);
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('2');
      // The big key sorts first, so it is the selected one.
      expect(m.screen()).toMatch(/… \d+ more rows/);
      m.unmount();
    });

    it('lists every path of a key in the compact detail, wrapped, not cut', async () => {
      const client = fakeClient();
      client.keys[0]!.policy = { allowed_paths: ['POST /v1/chat/completions', 'POST /v1/embeddings', 'POST /v1/responses', 'GET /v1/models'] };
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('2');
      expect(m.screen()).toContain('GET /v1/models');
      m.unmount();
    });

    it('uses the compact detail in the side panel when the terminal is too short for the full one', async () => {
      const client = fakeClient();
      const k = client.keys[0]!;
      k.credential = 'prod-primary';
      k.tags = ['support'];
      k.policy = { allowed_paths: Array.from({ length: 10 }, (_, i) => `POST /v1/route/${i}`) };
      const m = mount(client, { columns: 120, rows: 24 });
      await loaded(m);
      await m.press('2');
      expect(m.screen()).toContain('credential prod-primary');
      expect(m.screen()).toContain('created 20');
      expect(lines(m).filter((l) => /\b(today|month)\b.*requests/.test(l)).length).toBe(2);
      m.unmount();
    });

    it('does not shift the Needs attention columns as the list scrolls', async () => {
      const client = fakeClient();
      client.keys.length = 0;
      for (let i = 0; i < 12; i++) {
        client.keys.push({
          ...fakeClient().keys[0]!,
          id: `k_n${i}`,
          name: i < 4 ? `n${i}` : `uncapped-with-a-much-longer-name-${i}`,
          policy: {},
          spend: undefined,
        });
      }
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      const col = () => lines(m).map((l) => l.indexOf('no spend cap')).find((i) => i > 0);
      const before = col();
      await m.press('G');
      expect(col()).toBe(before);
      m.unmount();
    });

    it('does not shift the Audit columns as the list scrolls', async () => {
      const client = fakeClient();
      client.latestAudit = async () =>
        Array.from({ length: 100 }, (_, i) => ({
          id: i + 1,
          time: '2026-11-17T12:00:00Z',
          actor: i > 50 ? 'ci-deploy-bot-production' : 'owner',
          action: 'key.create',
          target_kind: 'key',
          target_id: 'k_web',
        }));
      const m = mount(client);
      await loaded(m);
      await m.press('4');
      const col = () => lines(m).map((l) => l.indexOf('key.create')).find((i) => i > 0);
      const before = col();
      await m.press('G');
      expect(col()).toBe(before);
      m.unmount();
    });

    it('shows the audit action in full on the first line of the entry', async () => {
      const client = fakeClient();
      client.latestAudit = async () => [
        { id: 1, time: '2026-11-17T12:00:00Z', actor: 'owner', action: 'price_override.delete', target_kind: 'upstream', target_id: 'up_x' },
      ];
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('4');
      expect(lines(m).some((l) => l.startsWith(' │ price_override.delete · upstream'))).toBe(true);
      m.unmount();
    });

    it('shows a long failure reason in full, hanging under the mark', async () => {
      const client = fakeClient();
      client.disableKey = async () => {
        throw new ApiError(
          'conflict',
          'the key was changed by someone else a moment ago; refresh and try again, and if it keeps happening ask the owner of the install to look at the audit log for what changed it',
        );
      };
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('2');
      await m.press('d');
      await m.press('y');
      await until(() => m.screen().includes('Could not disable'), 'the failure');
      expect(m.screen()).toContain('for what changed it');
      expect(lines(m).some((l) => /^ {3}\S/.test(l) && l.includes('ago;'))).toBe(true);
      expect(lines(m).length).toBeLessThanOrEqual(24);
      m.unmount();
    });

    it('says "expired" once in a key\'s detail', async () => {
      const client = fakeClient();
      client.keys[0]!.expired = true;
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('2');
      const detail = lines(m).filter((l) => l.startsWith(' │') && l.includes('expired'));
      expect(detail).toHaveLength(1);
      m.unmount();
    });

    it('lets the cursor move in a form field, and edits at it', async () => {
      const m = mount();
      await loaded(m);
      await m.press('n');
      await m.press('abcdef');
      await m.press(LEFT);
      await m.press(LEFT);
      await m.press(LEFT);
      await m.press('X');
      expect(m.screen()).toContain('abcxdef');
      await m.press('\u007f'); // backspace removes the x, left of the cursor
      expect(m.screen()).toContain('abcdef');
      expect(m.screen()).not.toContain('abcxdef');
      m.unmount();
    });
  });

  describe('seventh ui review fixes', () => {
    const lines = (m: ReturnType<typeof mount>) => m.screen().split('\n');
    const downs = (n: number) => Array.from({ length: n }, () => 'j');

    it('keeps the Audit header on one row when scrolled, with long actors', async () => {
      const client = fakeClient();
      client.latestAudit = async () =>
        Array.from({ length: 100 }, (_, i) => ({
          id: i + 1,
          time: '2026-11-17T12:00:00Z',
          actor: 'ci-deploy-bot-production-eu',
          action: 'credential.compromise',
          target_kind: 'credential',
          target_id: `cr_abcdefghijklmnop${i}`,
          client_ip: '2001:db8:85a3::8a2e:370:7334',
        }));
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('4');
      for (const key of downs(30)) await m.press(key);
      const head = lines(m).find((l) => l.includes('TARGET'))!;
      expect(head).toMatch(/TARGET\s+↑\d+ · ↓\d+/);
      expect(lines(m).filter((l) => l.includes('credential.compromise')).length).toBeGreaterThanOrEqual(12);
      // The entry's id and address are in the panel in full, wrapped.
      expect(m.screen().replace(/[│\s]+/g, ' ')).toContain('cr_abcdefghijklmnop69 · from 2001:db8:85a3::8a2e:370:7334');
      m.unmount();
    });

    it('keeps a long address on one row of the Upstreams detail, with the routes below it', async () => {
      const client = fakeClient();
      client.upstreams = [
        {
          ...upstream,
          host: 'a-very-long-hostname.internal.example-corporation.example.com.au',
          tls: false,
          path_allowlist: Array.from({ length: 8 }, (_, i) => `POST /v1/route/${i}`),
        },
      ];
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('3');
      const addr = lines(m).filter((l) => l.includes('address'));
      expect(addr).toHaveLength(1);
      expect(addr[0]).toMatch(/address {5}▲ plain http a-very-long/);
      expect(m.screen()).toContain('ROUTES');
      expect(m.screen()).toContain('POST /v1/route/7');
      m.unmount();
    });

    it('puts the id and upstream on their own row of a compact title when they do not fit', async () => {
      const client = fakeClient();
      const k = client.keys[0]!;
      k.id = 'aB3dE5fG';
      k.name = 'customer-support-summariser-production-eu-west-a';
      k.spend = {
        today: { period: 'd', cost_micro: 1, requests: 3, unmetered_requests: 4 },
        month: { period: 'm', cost_micro: 1, requests: 3 },
      };
      k.policy = { allowed_paths: Array.from({ length: 10 }, (_, i) => `POST /v1/route/${i}`) };
      const m = mount(client, { columns: 120, rows: 24 });
      await loaded(m);
      await m.press('2');
      await m.press('j');
      expect(m.screen()).toContain('id aB3dE5fG · openai');
      const spend = lines(m).filter((l) => /\b(today|month)\b.*3 requests/.test(l));
      expect(spend).toHaveLength(2);
      expect(spend.every((l) => !l.includes('…'))).toBe(true);
      m.unmount();
    });

    it('lists every path in the full detail, with no cap', async () => {
      const client = fakeClient();
      client.keys[0]!.policy = { allowed_paths: ['POST /v1/chat/completions', 'POST /v1/embeddings', 'POST /v1/responses', 'GET /v1/models'] };
      const m = mount(client, { columns: 120, rows: 34 });
      await loaded(m);
      await m.press('2');
      expect(m.screen()).toContain('GET /v1/models');
      expect(m.screen()).not.toMatch(/\+\d+ more/);
      m.unmount();
    });

    it('cuts upstream names in the middle in the create form, so similar ones can be told apart', async () => {
      const client = fakeClient();
      client.upstreams = [{ ...upstream, name: 'internal-llm-gateway-staging-17' }, { ...upstream, id: 'u2', name: 'internal-llm-gateway-staging-18' }];
      const m = mount(client);
      await loaded(m);
      await m.press('n');
      expect(m.screen()).toMatch(/internal.*….*ing-17/);
      m.unmount();
    });

    it('names the target alone in Recent activity when the action already says its kind', async () => {
      const m = mount();
      await loaded(m);
      const row = lines(m).find((l) => l.includes('key.create'))!;
      expect(row).toMatch(/key\.create +k_web/);
      m.unmount();
    });

    it('keeps the Keys filter and its scroll hint on one row', async () => {
      const client = fakeClient();
      const base = client.keys[0]!;
      client.keys.length = 0;
      for (let i = 0; i < 40; i++) client.keys.push({ ...base, id: `k_${i}`, name: `long-prefix-name-aaaaaaaaaaaa-${i}` });
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('2');
      await m.press('/');
      await m.press('long-prefix-name-aaaaaaaaaaaa-');
      const row = lines(m).find((l) => l.startsWith(' / long-prefix'))!;
      expect(row).toContain('40 of 40 keys');
      expect(row).toMatch(/↓/);
      expect(lines(m).filter((l) => l.includes('NAME')).length).toBe(1);
      m.unmount();
    });
  });

  describe('eighth ui review fixes', () => {
    const lines = (m: ReturnType<typeof mount>) => m.screen().split('\n');

    it('lists every risk in the side panel, wrapping them rather than cutting one off', async () => {
      const client = fakeClient();
      const k = client.keys[0]!;
      k.expires_at = new Date(Date.now() + 3 * 86_400_000).toISOString();
      k.spend = {
        today: { period: 'd', cost_micro: 4_600_000, requests: 1234, unmetered_requests: 17, cap_micro: 5_000_000 },
        month: { period: 'm', cost_micro: 31_250_000, requests: 9000 },
      };
      const m = mount(client, { columns: 120, rows: 24 });
      await loaded(m);
      await m.press('2');
      expect(m.screen()).toContain('expires in 3 days');
      expect(m.screen()).toContain('92% of its daily cap');
      expect(m.screen()).toContain('17 unmetered today');
      // The two spend bars are the same length.
      const bars = lines(m)
        .map((l) => l.match(/\b(today|month) ([█░·]+) /))
        .filter((x): x is RegExpMatchArray => x !== null && x.index !== undefined && x.index > 70)
        .map((x) => x[2]!.length);
      expect(bars).toHaveLength(2);
      expect(bars[0]).toBe(bars[1]);
      m.unmount();
    });

    it('shows the full detail when only a one-line note is needed beside the facts, not just when three note lines fit', async () => {
      const client = fakeClient();
      const k = client.keys[0]!;
      k.name = 'customer-support-summariser';
      k.tags = ['support'];
      k.credential = 'prod-primary';
      k.policy = { allowed_models: ['gpt-4o', 'gpt-4o-mini'], allowed_paths: ['POST /v1/chat/completions'] };
      k.note = 'owned by the support platform team; rotate quarterly and ask in the platform channel before changing the policy or the caps and again and again';
      for (let i = 0; i < 3; i++) client.keys.push({ ...client.keys[2]!, id: `k_m${i}`, name: `more-${i}`, status: 'active' });
      const m = mount(client);
      await loaded(m);
      await m.press('2');
      expect(m.screen()).toContain('POLICY');
      m.unmount();
    });

    it('shows the end of a long server error in the create form, where the screen has room', async () => {
      const client = fakeClient();
      client.createKey = async () => {
        throw new ApiError('invalid', `upstream openai has no credential named prod. ${'It is a long explanation that goes on. '.repeat(5)}Run kman upstream show openai to list what exists.`);
      };
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('n');
      await m.press(CTRL_S);
      await until(() => m.screen().includes('✖'), 'the error');
      expect(m.screen()).toContain('to list what exists.');
      expect(lines(m).length).toBeLessThanOrEqual(24);
      m.unmount();
    });

    it('names a target kind the same way in the Audit list and its detail', async () => {
      const client = fakeClient();
      client.latestAudit = async () => [
        { id: 1, time: '2026-11-17T12:00:00Z', actor: 'owner', action: 'price_override.delete', target_kind: 'price_override', target_id: 'po_100' },
      ];
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('4');
      expect(m.screen()).toContain('price override po_100');
      expect(m.screen()).toContain('price override id po_100');
      expect(m.screen()).not.toContain('price_override id');
      m.unmount();
    });
  });

  describe('ninth ui review fixes', () => {
    const lines = (m: ReturnType<typeof mount>) => m.screen().split('\n');
    const rich = (client: ReturnType<typeof fakeClient>) => {
      const k = { ...client.keys[0]! };
      k.id = 'k_rich';
      k.name = 'customer-support-summariser-production-eu-west-a';
      k.expires_at = new Date(Date.now() + 3 * 86_400_000).toISOString();
      k.tags = ['support', 'prod'];
      k.credential = 'prod-primary';
      k.spend = {
        today: { period: 'd', cost_micro: 4_600_000, requests: 12, unmetered_requests: 5, cap_micro: 5_000_000 },
        month: { period: 'm', cost_micro: 31_250_000, requests: 90 },
      };
      k.policy = {
        allowed_models: ['gpt-4o', 'gpt-4o-mini', 'o3-mini', 'o4'],
        allowed_paths: ['POST /v1/chat/completions', 'POST /v1/embeddings', 'POST /v1/responses'],
      };
      k.note = 'owned by the support platform team; rotate quarterly and ask in the platform channel before changing the policy or the caps';
      return k;
    };

    it('shows a lone rich key in full, with its whole note, and never clips it in the body', async () => {
      const client = fakeClient();
      client.keys = [rich(client)];
      const m = mount(client);
      await loaded(m);
      await m.press('2');
      expect(m.screen()).toContain('changing the policy or the caps');
      expect(lines(m).length).toBeLessThanOrEqual(34);
      m.unmount();
    });

    it('uses the tight full detail, not the compact one, when it is only a few rows short', async () => {
      const client = fakeClient();
      client.keys.push(rich(client));
      const m = mount(client);
      await loaded(m);
      await m.press('2');
      await m.press('j');
      await m.press('j');
      expect(m.screen()).toContain('POLICY');
      expect(m.screen()).toContain('credential  prod-primary');
      m.unmount();
    });

    it('gives the note of a tight wide panel every row it has left', async () => {
      const client = fakeClient();
      client.keys.push(rich(client));
      const m = mount(client, { columns: 120, rows: 32 });
      await loaded(m);
      await m.press('2');
      await m.press('j');
      await m.press('j');
      // The tight panel has rows left for the note, so all of it shows (it ends in "the caps").
      expect(m.screen().replace(/[│\s]+/g, ' ')).toContain('policy or the caps');
      m.unmount();
    });

    it('keeps whole labels in Needs attention and says how many it left out', async () => {
      const client = fakeClient();
      const k = client.keys[0]!;
      k.expires_at = new Date(Date.now() + 20 * 3_600_000).toISOString();
      k.spend = {
        today: { period: 'd', cost_micro: 4_750_000, requests: 12, unmetered_requests: 10, cap_micro: 5_000_000 },
        month: { period: 'm', cost_micro: 1, requests: 1 },
      };
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      const row = lines(m).find((l) => l.includes('expires within a day'))!;
      expect(row).toMatch(/\+1 more/);
      expect(row).not.toMatch(/dail…/);
      m.unmount();
    });

    it('wraps the no-credential warning under its mark and keeps the form within the body', async () => {
      const client = fakeClient();
      client.upstreams = [{ ...upstream, credentials: [] }];
      client.createKey = async () => {
        throw new ApiError('invalid', `upstream openai has no credential. ${'Explanation that goes on and on. '.repeat(6)}THE END`);
      };
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('n');
      await m.press(CTRL_S);
      await until(() => m.screen().includes('✖'), 'the error');
      expect(lines(m).some((l) => /│ {3}key on it\./.test(l))).toBe(true);
      expect(m.screen()).toContain('THE END');
      expect(lines(m).length).toBeLessThanOrEqual(24);
      m.unmount();
    });

    it('marks text cut off at either end of a scrolled form field', async () => {
      const m = mount();
      await loaded(m);
      await m.press('n');
      await m.press('a'.repeat(40));
      expect(m.screen()).toMatch(/\[…a+ ?\]/);
      m.unmount();
    });
  });

  describe('tenth ui review fixes', () => {
    const lines = (m: ReturnType<typeof mount>) => m.screen().split('\n');

    it('keeps two whole labels in Needs attention at 120 columns even when a key has a long name', async () => {
      const client = fakeClient();
      const base = client.keys[0]!;
      client.keys.length = 0;
      client.keys.push(
        { ...base, id: 'k_long', name: 'a-key-with-a-very-long-name-that-goes-past-every-column-here-yes', policy: {} },
      );
      for (let i = 1; i <= 2; i++) {
        client.keys.push({
          ...base,
          id: `k_a${i}`,
          name: `attn-${i}`,
          expires_at: new Date(Date.now() + 2.5 * 86_400_000).toISOString(),
          spend: {
            today: { period: 'd', cost_micro: 950_000, requests: 3, cap_micro: 1_000_000 },
            month: { period: 'm', cost_micro: 950_000, requests: 3, cap_micro: 50_000_000 },
          },
        });
      }
      const m = mount(client, { columns: 120, rows: 34 });
      await loaded(m);
      const row = lines(m).find((l) => l.includes('attn-1'))!;
      expect(row).toContain('95% of its daily cap');
      expect(row).not.toContain('+1 more');
      m.unmount();
    });

    it('fits the compact detail\'s "more rows" marker in the side panel, on one row', async () => {
      const client = fakeClient();
      const k = client.keys[0]!;
      k.tags = ['support'];
      k.credential = 'prod-primary';
      k.policy = { allowed_paths: Array.from({ length: 24 }, (_, i) => `POST /v1/route/${i}`) };
      k.note = Array.from({ length: 80 }, (_, i) => `word${i}`).join(' ');
      const m = mount(client, { columns: 120, rows: 24 });
      await loaded(m);
      await m.press('2');
      const marker = lines(m).find((l) => l.includes('more rows'))!;
      expect(marker).toContain('make the terminal taller');
      m.unmount();
    });

    it('marks the cut start of a long value in an unfocused field', async () => {
      const m = mount();
      await loaded(m);
      await m.press('n');
      await m.press('customer-support-summariser-production');
      await m.press('\t');
      expect(m.screen()).toMatch(/\[….{17}\]/);
      m.unmount();
    });

    it('keeps the revoke hints under a server error, and says Enter retries', async () => {
      const client = fakeClient();
      client.revokeKey = async () => {
        throw new ApiError('conflict', 'something is in the way');
      };
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('2');
      await m.press('x');
      await m.press('checkout');
      await m.press(ENTER);
      await until(() => m.screen().includes('something is in the way'), 'the error');
      expect(m.screen()).toContain('Enter to try again · Esc to cancel');
      expect(lines(m).length).toBeLessThanOrEqual(24);
      m.unmount();
    });
  });

  describe('eleventh ui review fixes', () => {
    const lines = (m: ReturnType<typeof mount>) => m.screen().split('\n');

    it('does not tell someone to retry a revoke whose name no longer matches', async () => {
      const client = fakeClient();
      client.revokeKey = async () => {
        throw new ApiError('conflict', 'something is in the way');
      };
      const m = mount(client, { columns: 100, rows: 34 });
      await loaded(m);
      await m.press('2');
      await m.press('x');
      await m.press('checkout');
      await m.press(ENTER);
      await until(() => m.screen().includes('Enter to try again'), 'the retry hint');
      await m.press('\u007f'); // backspace: the name no longer matches
      expect(m.screen()).toContain('Type the name exactly');
      expect(m.screen()).not.toContain('Enter to try again');
      await m.press(ENTER);
      expect(m.screen()).toContain('That is not the name');
      m.unmount();
    });

    it('shows all of a long revoke error where the screen has room', async () => {
      const client = fakeClient();
      client.revokeKey = async () => {
        throw new ApiError('conflict', `${'The server explained at length why it could not do this. '.repeat(4)}THE END`);
      };
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('2');
      await m.press('x');
      await m.press('checkout');
      await m.press(ENTER);
      await until(() => m.screen().includes('✖'), 'the error');
      expect(m.screen()).toContain('THE END');
      expect(lines(m).length).toBeLessThanOrEqual(24);
      m.unmount();
    });

    it('shortens a six-digit request count rather than cutting it in the side panel', async () => {
      const client = fakeClient();
      client.keys[0]!.spend = {
        today: { period: 'd', cost_micro: 108_000, requests: 3, cap_micro: 5_000_000 },
        month: { period: 'm', cost_micro: 108_000, requests: 123_456 },
      };
      client.keys[0]!.policy = { allowed_paths: Array.from({ length: 10 }, (_, i) => `POST /v1/route/${i}`) };
      const m = mount(client, { columns: 120, rows: 24 });
      await loaded(m);
      await m.press('2');
      expect(lines(m).some((l) => l.includes('month') && l.includes('123k reqs'))).toBe(true);
      expect(m.screen()).not.toMatch(/reques…/);
      m.unmount();
    });

    it('gives a compact detail\'s note the spare rows, not just three lines', async () => {
      const client = fakeClient();
      const k = client.keys[0]!;
      k.tags = ['support'];
      k.credential = 'prod-primary';
      k.policy = { allowed_paths: Array.from({ length: 5 }, (_, i) => `POST /v1/route/${i}`) };
      k.note = Array.from({ length: 24 }, (_, i) => `word${i}`).join(' ');
      const m = mount(client, { columns: 120, rows: 24 });
      await loaded(m);
      await m.press('2');
      const noteRows = lines(m).filter((l) => /word\d+/.test(l));
      expect(noteRows.length).toBeGreaterThanOrEqual(4);
      m.unmount();
    });

    it('marks an upstream with no credential in the list', async () => {
      const client = fakeClient();
      client.upstreams = [
        { ...upstream, id: 'a', name: 'healthy' },
        { ...upstream, id: 'b', name: 'nocred', credentials: [] },
      ];
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('3');
      const row = lines(m).find((l) => l.includes('nocred') && l.includes('api.openai.com'))!;
      expect(row).toMatch(/^ [▌ ]▲ nocred/);
      expect(lines(m).find((l) => l.includes('healthy') && l.includes('api.openai.com'))).not.toContain('▲');
      m.unmount();
    });

    it('does not repeat an unnamed key\'s id in its detail title', async () => {
      const client = fakeClient();
      client.keys[0]!.name = undefined;
      client.keys[0]!.id = 'k_zzz';
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('2');
      await m.press('G');
      await m.press('g');
      expect(m.screen()).not.toContain('id k_zzz');
      m.unmount();
    });
  });

  describe('twelfth ui review fixes', () => {
    const lines = (m: ReturnType<typeof mount>) => m.screen().split('\n');
    const pad = (n: number) => String(n).padStart(2, '0');

    it('pages the Keys list by the rows it shows, so no row is skipped', async () => {
      const client = fakeClient();
      const base = client.keys[0]!;
      client.keys.length = 0;
      for (let i = 0; i < 30; i++) client.keys.push({ ...base, id: `k_${pad(i)}`, name: `nocap-${pad(i)}`, policy: {} });
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('2');
      const shown = lines(m).filter((l) => /nocap-\d\d/.test(l) && /(active|disabled)/.test(l) && !l.includes('│'));
      await m.press('\u001b[6~'); // PgDn
      const selected = lines(m).find((l) => l.includes('▌') && /nocap-\d\d/.test(l))!;
      // One page down lands on the last row that was on screen.
      expect(selected).toContain(`nocap-${pad(shown.length - 1)}`);
      m.unmount();
    });

    it('pages the Audit list by the rows it shows', async () => {
      const client = fakeClient();
      client.latestAudit = async () =>
        Array.from({ length: 60 }, (_, i) => ({ id: i + 1, time: '2026-11-17T12:00:00Z', actor: 'owner', action: 'key.create', target_kind: 'key', target_id: `k_${i}` }));
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('4');
      const shown = lines(m).filter((l) => l.includes('key.create') && !l.includes('│')).length;
      await m.press('\u001b[6~');
      // The newest entry is first (k_59); a page down lands on the last row that was on screen.
      const selected = lines(m).find((l) => l.includes('▌') && l.includes('key.create'))!;
      expect(selected).toContain(`key k_${59 - (shown - 1)}`);
      m.unmount();
    });

    it('knows how many attention rows the Overview shows, for paging', () => {
      expect(attentionRows(30, 78, 19)).toBe(5);
      expect(attentionRows(2, 78, 19)).toBe(2);
      expect(attentionRows(30, 118, 29)).toBe(21);
    });

    it('puts a space after the no-credential mark in the Upstreams list', async () => {
      const client = fakeClient();
      client.upstreams = [{ ...upstream, id: 'b', name: 'nocred', credentials: [] }];
      const m = mount(client);
      await loaded(m);
      await m.press('3');
      expect(lines(m).some((l) => /▲ nocred/.test(l))).toBe(true);
      m.unmount();
    });

    it('says an empty audit log is empty, with no entry panel to select from', async () => {
      const client = fakeClient();
      client.latestAudit = async () => [];
      const m = mount(client);
      await loaded(m);
      await m.press('4');
      expect(m.screen()).toContain('The audit log is empty');
      expect(m.screen()).not.toContain('No entry selected');
      m.unmount();
    });
  });

  describe('client addresses', () => {
    const lines = (m: ReturnType<typeof mount>) => m.screen().split('\n');
    const seen = (minutesAgo: number) => new Date(Date.now() - minutesAgo * 60_000).toISOString();
    const withAddresses = (client: ReturnType<typeof fakeClient>) => {
      client.clients['k_checkout'] = {
        key_id: 'k_checkout',
        total: 3,
        clients: [
          { address: '198.51.100.23', requests: 8800, blocked: 0, first_seen: seen(9000), last_seen: seen(1), country: 'US', asn: 16509, cost_micro: 41_300_000 },
          { address: '2001:db8:85a3::8a2e:370:7334', requests: 510, blocked: 2, first_seen: seen(9000), last_seen: seen(300), country: 'NL', asn: 1136, cost_micro: 1_820_000, unmetered_requests: 3 },
          { address: '10.0.4.17', requests: 15, blocked: 15, first_seen: seen(2000), last_seen: seen(1560), cost_micro: 0 },
        ],
      };
    };

    it('lists the addresses a key was used from, newest first, on c, and closes on Esc', async () => {
      const client = fakeClient();
      withAddresses(client);
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('2');
      await m.press('c');
      await until(() => m.screen().includes('198.51.100.23'), 'the addresses');
      expect(m.client.calls).toContain('clients k_checkout');
      const s = m.screen();
      for (const want of ['Addresses that used checkout', 'ADDRESS', 'REQUESTS', 'BLOCKED', 'LAST SEEN', 'US AS16509', '$41.30', '$1.82+', '10.0.4.17', '1d ago']) {
        expect(s).toContain(want);
      }
      expect(s.indexOf('198.51.100.23')).toBeLessThan(s.indexOf('10.0.4.17'));
      expect(s).toContain('+ some costs unknown');
      expect(lines(m).length).toBeLessThanOrEqual(24);
      expect(lines(m).every((l) => [...l].length <= 80)).toBe(true);
      await m.press(ESC);
      expect(m.screen()).not.toContain('Addresses that used');
      m.unmount();
    });

    it('says how to turn addresses on when the install does not give them out', async () => {
      const client = fakeClient();
      client.exposeClients = false;
      const m = mount(client);
      await loaded(m);
      await m.press('2');
      await m.press('c');
      await until(() => m.screen().includes('Not shown'), 'the refusal');
      expect(m.screen().replace(/[│\s]+/g, ' ')).toContain('kman settings set expose_client_addresses true');
      m.unmount();
    });

    it('says so when a key has no addresses in the events kept', async () => {
      const m = mount();
      await loaded(m);
      await m.press('2');
      await m.press('c');
      await until(() => m.screen().includes('None in the events kman keeps'), 'the empty list');
      m.unmount();
    });

    it('says how many addresses are not listed, and scrolls a long list', async () => {
      const client = fakeClient();
      client.clients['k_checkout'] = {
        key_id: 'k_checkout',
        total: 80,
        clients: Array.from({ length: 30 }, (_, i) => ({
          address: `192.0.2.${i + 1}`,
          requests: i + 1,
          blocked: 0,
          first_seen: seen(5000),
          last_seen: seen(10 + i),
          cost_micro: 1000,
        })),
      };
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('2');
      await m.press('c');
      await until(() => m.screen().includes('192.0.2.1 '), 'the list');
      expect(m.screen()).toContain('50 more not listed');
      expect(m.screen()).not.toContain('192.0.2.30');
      await m.press('G');
      expect(m.screen()).toContain('192.0.2.30');
      expect(m.screen()).not.toContain('192.0.2.1 ');
      expect(lines(m).length).toBeLessThanOrEqual(24);
      m.unmount();
    });

    it('offers the addresses on the Keys footer, not while offline, and lists the key in help', async () => {
      const client = fakeClient();
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('2');
      expect(lines(m).at(-1)).toContain('c IPs');
      await m.press('?');
      expect(m.screen()).toContain('the addresses a key was used from');
      await m.press(ESC);
      client.fail = new ApiError('unreachable', 'cannot reach http://127.0.0.1:8081: connection refused');
      await m.press('r');
      await until(() => m.screen().includes('connection lost'), 'the lost-connection flag');
      expect(lines(m).at(-1)).not.toContain('c IPs');
      await m.press('c');
      expect(m.screen()).not.toContain('Addresses that used');
      m.unmount();
    });

    it('keeps a "Disabling…" notice on screen for as long as the request runs', async () => {
      const client = fakeClient();
      client.disableKey = () => new Promise(() => {});
      const m = mount(client, SIZE, undefined, 300);
      await loaded(m);
      await m.press('2');
      await m.press('d');
      m.stdin.write('y');
      await until(() => m.screen().includes('Disabling checkout…'), 'the working notice');
      await tick(800);
      expect(m.screen()).toContain('Disabling checkout…');
      m.unmount();
    });
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