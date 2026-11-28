import { describe, expect, it } from 'vitest';
import { ApiError } from '../src/api.js';
import { App } from '../src/App.js';
import { fakeClient, incident, key, place, tick, until, upstream } from './fakes.js';
import { render } from './render.js';

const SIZE = { columns: 100, rows: 34 };
const ENTER = '\r';
const ESC = '\u001b';
const CTRL_S = '\u0013';

/** An upstream whose detail is taller than the detail panel at 80x24. */
function longUpstream(client: ReturnType<typeof fakeClient>) {
  client.upstreams = [{ ...upstream, path_allowlist: Array.from({ length: 40 }, (_, i) => `POST /v1/route/${i}`) }];
}

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
    expect(s).toContain('127.0.0.1:8081');
    expect(s).toContain('owner');
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
    expect(s).toContain('○ disabled');
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

  it('applies every list movement when terminal input arrives in a burst', async () => {
    const m = mount(fakeClient(['a', 'b', 'c', 'd'].map((id) => key({ id }))));
    await loaded(m);
    await m.press('2');
    await m.press('jjj');
    expect(m.screen()).toContain('Key: d');
    await m.press('\x1b[A'.repeat(3));
    expect(m.screen()).toContain('Key: a');
    await m.press('/');
    await m.press('jjj');
    expect(m.screen()).toContain('No key matches "jjj"');
    m.unmount();
  });

  it('clears a failed refresh when the next automatic poll succeeds', async () => {
    const client = fakeClient();
    const app = render(<App client={client} url="unix:/test" size={SIZE} intervalMs={80} />);
    try {
      await until(() => !!app.lastFrame()?.includes('● live'), 'first poll');
      client.fail = new ApiError('unreachable', 'connection refused');
      app.stdin.write('r');
      await until(() => !!app.lastFrame()?.includes('Could not refresh'), 'failed refresh');
      client.fail = undefined;
      await until(() => !!app.lastFrame()?.includes('● live') && !app.lastFrame()?.includes('Could not refresh'), 'automatic recovery');
    } finally {
      app.unmount();
    }
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
      expect(s).toContain('NEEDS ATTENTION');
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

    it('says where the selection is in a long list: n of m on the border', async () => {
      const client = fakeClient();
      for (let i = 0; i < 14; i++) client.keys.push({ ...client.keys[0]!, id: `k_${i}`, name: `extra-${String(i).padStart(2, '0')}` });
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('2');
      expect(m.screen()).toContain('1 of 17');
      await m.press('G');
      expect(m.screen()).toContain('17 of 17');
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
    m.unmount();
  });

  it('keeps a failure on screen until the next key press, while a success fades', async () => {
    // Long enough that press() finishes before a success fades, and the test can
    // still see it: press() waits for the frame to settle, and at 60 ms a success
    // could come and go between its samples.
    const noticeMs = 600;
    const client = fakeClient();
    const m = mount(client, SIZE, undefined, noticeMs);
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
    await tick(noticeMs + 200); // well past the time a success would last
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
      await until(() => m.screen().includes('offline'), 'the lost-connection flag');
      await m.press('2');
      expect(lines(m).at(-1)).not.toContain('new key');
      await m.press('n');
      expect(m.screen()).not.toContain('New virtual key');
      expect(m.screen()).toContain('not answering');
      await m.press('d');
      expect(m.screen()).not.toContain('Disable this key?');
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

    it('marks an upstream with a newer preset and a plain-http one in the list, and says where the selection is', async () => {
      const client = fakeClient();
      client.upstreams = Array.from({ length: 14 }, (_, i) => ({
        ...upstream,
        id: `up_${i}`,
        name: i === 0 ? 'a-very-long-upstream-name-indeed-yes' : `up-${i}`,
        update_available: i === 0,
        tls: i === 1 ? false : true,
        path_allowlist: Array.from({ length: 12 }, (_, r) => `POST /v1/route/number/${r}`),
      }));
      const m = mount(client, { columns: 100, rows: 34 });
      await loaded(m);
      await m.press('3');
      const s = m.screen();
      expect(s).toContain('Upstreams (14)');
      expect(s).toContain('1 of 14');
      expect(lines(m).find((l) => l.includes('a-very') && l.includes('…'))).toContain('· update');
      expect(lines(m).find((l) => /^[│┃] up-1 /.test(l))).toContain('▲ http');
      expect(lines(m).every((l) => [...l].length <= 100)).toBe(true);
      m.unmount();
    });

    it('shows the longest action names in full in the entry', async () => {
      const client = fakeClient();
      client.latestAudit = async () => [
        { id: 1, time: '2026-11-17T12:00:00Z', actor: 'ci-bot-with-long-name', action: 'price_override.delete', target_kind: 'upstream', target_id: 'up_x' },
      ];
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('4');
      expect(lines(m).find((l) => l.includes('action'))).toContain('price_override.delete');
      expect(lines(m).find((l) => l.includes('actor'))).toContain('ci-bot-with-long-name');
      m.unmount();
    });

    it('points an empty install at n', async () => {
      const client = fakeClient([]);
      const m = mount(client);
      await loaded(m);
      expect(m.screen()).toContain('n creates one');
      m.unmount();
    });

    it('keeps the disable prompt within 80 columns, and the key actions in the options bar', async () => {
      const m = mount(fakeClient(), { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('2');
      // What can be done to the selected key is dropped last; scrolling and selecting go first.
      for (const hint of ['↑↓ select', 'd disable', 'x revoke', '1-4 panels', '/ filter', '? help', 'q quit']) expect(lines(m).at(-1)).toContain(hint);
      await m.press('d');
      expect(m.screen()).toContain('Disable this key?');
      expect(m.screen()).toContain('you can enable it again');
      expect(lines(m).every((l) => [...l].length <= 80)).toBe(true);
      m.unmount();
    });

    it('closes help with Esc', async () => {
      const m = mount();
      await loaded(m);
      await m.press('?');
      expect(m.screen()).toContain('Keybindings');
      await m.press(ESC);
      expect(m.screen()).not.toContain('Keybindings');
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
      const m = mount(client, SIZE);
      await loaded(m);
      await m.press('4');
      const main = m.screen().split('\n').map((l) => l.slice(44)).join(' ').replace(/[│┃\s]+/g, ' ');
      expect(main).toContain('from 10.0.0.7');
      expect(main).toContain('The latest 100 entries');
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
      // The spend is the last part of the detail: scroll down to it.
      for (let i = 0; i < 10 && !m.screen().includes('12k reqs'); i++) await m.press('J');
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

    it('keeps the expiry reachable for a long key name at 80 columns', async () => {
      const client = fakeClient();
      client.keys[0]!.name = 'a-really-quite-long-key-name-that-goes-past-the-column-width-for-sure-yes';
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('2');
      expect(m.screen().replace(/[│┃\s]+/g, ' ')).toMatch(/expires \d{4}-\d\d-\d\d/);
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
      expect(lines(m).some((l) => l.includes('kman login --admin-url <address>'))).toBe(true);
      expect(m.screen()).toContain('Admin address: http://127.0.0.1:8081');
      expect(m.screen()).not.toContain('KMAN_ADMIN_TOKEN supplied');
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
      // The whole name and the id, never a title cut in the middle of what tells them apart.
      expect(m.screen()).toContain('billing-reconciliation-production-eu');
      expect(m.screen()).toContain('id k_checkout');
      expect(lines(m).every((l) => [...l].length <= 80)).toBe(true);
      m.unmount();
    });

    it('keeps the plain-http mark in the list and shows the full address for a long host', async () => {
      const client = fakeClient();
      client.upstreams = [{ ...upstream, host: 'ollama.internal.example.corp', port: 11434, tls: false }];
      const m = mount(client, { columns: 100, rows: 34 });
      await loaded(m);
      await m.press('3');
      expect(lines(m).find((l) => /^[│┃] openai/.test(l))).toContain('▲ http');
      expect(m.screen().split('\n').map((l) => l.slice(44)).join(' ').replace(/[│┃\s]+/g, ' ')).toContain('plain http ollama.internal.example.corp:11434');
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

    it('says a stale price table is stale, at 80 columns', async () => {
      const client = fakeClient();
      client.priceStatus = async () => ({ age_days: 175, generated_at: '2026-05-01T00:00:00Z', stale: true });
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      expect(m.screen()).toContain('▲ price stale');
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
      const order = lines(m).filter((l) => /^[│┃] uncapped-\d+/.test(l)).map((l) => l.match(/uncapped-\d+/)![0]);
      expect(order.slice(0, 3)).toEqual(['uncapped-1', 'uncapped-2', 'uncapped-10']);
      m.unmount();
    });

    it('shows a revoked key\'s spend as dots, not a meter, in the detail', async () => {
      const client = fakeClient();
      client.keys[2]!.status = 'revoked';
      const m = mount(client, { columns: 100, rows: 34 });
      await loaded(m);
      await m.press('2');
      await m.press('G');
      const detail = lines(m).filter((l) => /\btoday\b.*[█░·]/.test(l));
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
      await until(() => /[│┃] ! \S/.test(m.screen()), 'the error');
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

    it('says where the selection is in a long Upstreams list', async () => {
      const client = fakeClient();
      client.upstreams = Array.from({ length: 14 }, (_, i) => ({ ...upstream, id: `u${i}`, name: `up-${i}` }));
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('3');
      expect(m.screen()).toContain('Upstreams (14)');
      expect(m.screen()).toContain('1 of 14');
      await m.press('G');
      expect(m.screen()).toContain('14 of 14');
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
      await until(() => /[│┃] ! \S/.test(m.screen()), 'the error');
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

    it('shows the audit action in full in the entry', async () => {
      const client = fakeClient();
      client.latestAudit = async () => [
        { id: 1, time: '2026-11-17T12:00:00Z', actor: 'owner', action: 'price_override.delete', target_kind: 'upstream', target_id: 'up_x' },
      ];
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('4');
      expect(lines(m).some((l) => /^[│┃] action +price_override\.delete/.test(l))).toBe(true);
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
      expect(lines(m).some((l) => /^ {3}\S/.test(l) && l.includes('refresh and try again'))).toBe(true);
      expect(lines(m).length).toBeLessThanOrEqual(24);
      m.unmount();
    });

    it('says "expired" once in a key\'s detail', async () => {
      const client = fakeClient();
      client.keys[0]!.expired = true;
      const m = mount(client, { columns: 100, rows: 34 });
      await loaded(m);
      await m.press('2');
      const detail = lines(m).filter((l) => l.includes('Key: ') === false && l.includes('expired') && !/^[│┃] checkout/.test(l));
      expect(detail.map((l) => l.slice(44)).filter((l) => l.includes('expired'))).toHaveLength(1);
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

    it('keeps a long address on one row of the Upstreams detail, with the routes reachable', async () => {
      const client = fakeClient();
      client.upstreams = [
        {
          ...upstream,
          host: 'a-very-long-hostname.internal.example-corporation.example.com.au',
          tls: false,
          path_allowlist: Array.from({ length: 8 }, (_, i) => `POST /v1/route/${i}`),
        },
      ];
      const m = mount(client, { columns: 100, rows: 34 });
      await loaded(m);
      await m.press('3');
      const main = m.screen().split('\n').map((l) => l.slice(44)).join(' ').replace(/[│┃\s]+/g, ' ');
      expect(main).toContain('▲ plain http a-very-long-hostname');
      expect(main).toContain('routes');
      expect(main).toContain('POST /v1/route/7');
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
      // Nothing needs attention, so Status shows what happened.
      const m = mount(fakeClient([key({ id: 'k_ok', name: 'ok' })]));
      await loaded(m);
      const row = lines(m).find((l) => l.includes('Recent activity') === false && l.includes('key.create'))!;
      expect(row).toMatch(/key\.create +k_web/);
      m.unmount();
    });

  });

  describe('eighth ui review fixes', () => {
    const lines = (m: ReturnType<typeof mount>) => m.screen().split('\n');

    it('lists every risk of a key in its detail', async () => {
      const client = fakeClient();
      const k = client.keys[0]!;
      k.expires_at = new Date(Date.now() + 3 * 86_400_000).toISOString();
      k.spend = {
        today: { period: 'd', cost_micro: 4_600_000, requests: 1234, unmetered_requests: 17, cap_micro: 5_000_000 },
        month: { period: 'm', cost_micro: 31_250_000, requests: 9000 },
      };
      const m = mount(client, { columns: 120, rows: 40 });
      await loaded(m);
      await m.press('2');
      expect(m.screen()).toContain('expires in 3 days');
      expect(m.screen()).toContain('92% of its daily cap');
      expect(m.screen()).toContain('17 unmetered today');
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
      await until(() => /[│┃] ! \S/.test(m.screen()), 'the error');
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
      expect(m.screen()).not.toContain('price_override ');
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
      // The note wraps wherever the detail is wide enough to break it: read the detail as one run.
      const detail = lines(m).map((l) => l.slice(44)).join(' ').replace(/[│┃\s]+/g, ' ');
      expect(detail).toContain('changing the policy or the caps');
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
      await until(() => /[│┃] ! \S/.test(m.screen()), 'the error');
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
      expect(m.screen()).toContain('Enter tries again.');
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
      await until(() => m.screen().includes('Enter tries again'), 'the retry hint');
      await m.press('\u007f'); // backspace: the name no longer matches
      expect(m.screen()).toContain('Type the name exactly');
      expect(m.screen()).not.toContain('Enter tries again');
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
      await until(() => /[│┃] ! \S/.test(m.screen()), 'the error');
      expect(m.screen()).toContain('THE END');
      expect(lines(m).length).toBeLessThanOrEqual(24);
      m.unmount();
    });

    it('shortens a six-digit request count rather than cutting it', async () => {
      const client = fakeClient();
      client.keys[0]!.spend = {
        today: { period: 'd', cost_micro: 108_000, requests: 3, cap_micro: 5_000_000 },
        month: { period: 'm', cost_micro: 108_000, requests: 123_456 },
      };
      const m = mount(client, { columns: 120, rows: 40 });
      await loaded(m);
      await m.press('2');
      expect(m.screen()).toContain('123k reqs');
      m.unmount();
    });

    it('marks an upstream with no credential in the list', async () => {
      const client = fakeClient();
      client.upstreams = [
        { ...upstream, id: 'a', name: 'healthy' },
        { ...upstream, id: 'b', name: 'nocred', credentials: [] },
      ];
      const m = mount(client, { columns: 100, rows: 34 });
      await loaded(m);
      await m.press('3');
      expect(lines(m).find((l) => /^[│┃] nocred/.test(l))).toContain('! no credential');
      expect(lines(m).find((l) => /^[│┃] healthy/.test(l))).not.toContain('▲');
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
      const shown = lines(m).filter((l) => /^[│┃] nocap-\d\d/.test(l)).length;
      await m.press('\u001b[6~'); // PgDn
      // One page down lands on the last row that was on screen.
      expect(m.screen()).toContain(`${shown} of 30`);
      m.unmount();
    });

    // The wide layout gives the other panels more than a collapsed line, so the
    // page has to come from the layout that is drawn (#117).
    it('pages the Keys list by the rows it shows in the wide layout too', async () => {
      const client = fakeClient();
      const base = client.keys[0]!;
      client.keys.length = 0;
      for (let i = 0; i < 60; i++) client.keys.push({ ...base, id: `k_${pad(i)}`, name: `nocap-${pad(i)}`, policy: {} });
      const m = mount(client, { columns: 120, rows: 40 });
      await loaded(m);
      await m.press('2');
      const shown = lines(m).filter((l) => /^[│┃] nocap-\d\d/.test(l)).length;
      await m.press('\u001b[6~'); // PgDn
      expect(m.screen()).toContain(`${shown} of 60`);
      await m.press('\u001b[6~');
      expect(m.screen()).toContain(`${2 * shown - 1} of 60`);
      await m.press('\u001b[5~'); // PgUp, by the same page
      expect(m.screen()).toContain(`${shown} of 60`);
      m.unmount();
    });

    it('pages the Audit list by the rows it shows in the wide layout too', async () => {
      const client = fakeClient();
      client.latestAudit = async () =>
        Array.from({ length: 60 }, (_, i) => ({ id: i + 1, time: '2026-11-17T12:00:00Z', actor: 'owner', action: 'key.create', target_kind: 'key', target_id: `k_${i}` }));
      const m = mount(client, { columns: 140, rows: 45 });
      await loaded(m);
      await m.press('4');
      const shown = lines(m).filter((l) => /key\.create +k_\d+/.test(l)).length;
      await m.press('\u001b[6~');
      expect(m.screen()).toContain(`${shown} of 60`);
      m.unmount();
    });

    it('pages the Audit list by the rows it shows', async () => {
      const client = fakeClient();
      client.latestAudit = async () =>
        Array.from({ length: 60 }, (_, i) => ({ id: i + 1, time: '2026-11-17T12:00:00Z', actor: 'owner', action: 'key.create', target_kind: 'key', target_id: `k_${i}` }));
      const m = mount(client, { columns: 80, rows: 24 });
      await loaded(m);
      await m.press('4');
      const shown = lines(m).filter((l) => /key\.create +k_\d+/.test(l)).length;
      await m.press('\u001b[6~');
      expect(m.screen()).toContain(`${shown} of 60`);
      m.unmount();
    });

    it('says an empty audit log is empty, with no entry to select', async () => {
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
      const m = mount(client, SIZE);
      await loaded(m);
      await m.press('2');
      expect(lines(m).at(-1)).toContain('c IPs');
      await m.press('?');
      expect(m.screen()).toContain('the IP addresses a key was used from');
      await m.press(ESC);
      client.fail = new ApiError('unreachable', 'cannot reach http://127.0.0.1:8081: connection refused');
      await m.press('r');
      await until(() => m.screen().includes('offline'), 'the lost-connection flag');
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

  it('displays the server-provided proxy address with the SDK base path', async () => {
    const client = fakeClient();
    const create = client.createKey;
    client.createKey = async (input) => ({ ...(await create(input)), proxy_url: 'https://proxy.test:8443' });
    const m = mount(client, { columns: 80, rows: 24 });
    await loaded(m);
    await m.press('n');
    await m.press(CTRL_S);
    expect(m.screen()).toContain('Base URL on the kman host: https://proxy.test:8443/v1');
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

  it('shows every field of an upstream at 80x24, the other panels being single lines', async () => {
    const m = mount(fakeClient(), SMALL);
    await loaded(m);
    await m.press('3');
    for (const text of ['address', 'routes', 'POST /v1/chat/completions', '3 (2 active)']) expect(m.screen()).toContain(text);
    expect(m.screen()).not.toContain('J/K scroll');
    m.unmount();
  });

  it('keeps every field of a long upstream reachable at 80x24 by scrolling', async () => {
    const client = fakeClient();
    longUpstream(client);
    const m = mount(client, SMALL);
    await loaded(m);
    await m.press('3');
    expect(m.screen()).toContain('↓ J more');
    expect(m.screen()).toContain('J/K scroll');
    expect(m.screen()).not.toContain('/v1/route/39');
    for (let i = 0; i < 14; i++) await m.press('J');
    expect(m.screen()).toContain('/v1/route/39');
    for (let i = 0; i < 14; i++) await m.press('K');
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
      // One line is all the strip has: the price goes before the address, and shows when there is room.
      [SMALL, /owner @ 127\.0\.0\.1:8081/],
      [{ columns: 100, rows: 24 }, /price (3d|data 3 days old)/],
      // A fresh price joins the first line, in its short form.
      [SIZE, /updated .* · price 3d/],
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
    // The title says 0 of 3 and the detail says why: the list does not say it a third time.
    expect(rows().filter((l) => l.includes('No key matches')).length).toBe(1);
    expect(rows().find((l) => l.includes('/ zzz'))).not.toContain('No key matches');
    expect(rows().find((l) => l.includes('Keys (0/3)'))).toBeDefined();
    await m.press(ENTER);
    expect(rows().filter((l) => l.includes('No key matches')).length).toBe(1);
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
    expect(row).toContain('! at cap');
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
    expect(row).toContain('! no credential');
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

  it('keeps a key warning on its row beside a long name, and no spend', async () => {
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
    const row = m.screen().split('\n').find((l) => /^[│┃] checkou/.test(l)) ?? '';
    expect(row).toContain('● active');
    // Long name, narrow column: the warning keeps its mark. The spend is in the detail, not the row.
    expect(row).toContain('▲');
    expect(row).not.toContain('$');
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
    const flat = () => m.screen().split('\n').map((l) => l.slice(44)).join('').replace(/[\s│┃]/g, '');
    expect(flat()).toContain(`${base}-a`);
    await m.press('j');
    expect(flat()).toContain(`${base}-b`);
    m.unmount();
  });

  it('stops offering to scroll when a filter leaves no key to show', async () => {
    const lines = (m: ReturnType<typeof mount>) => m.screen().split('\n');
    const client = fakeClient();
    for (const k of client.keys) k.policy = { allowed_models: Array.from({ length: 40 }, (_, i) => `model-number-${i}`) };
    const m = mount(client, SMALL);
    await loaded(m);
    await m.press('2');
    // The detail has more than it shows: the bar offers going into it, and the border says how to scroll.
    expect(lines(m).at(-1)).toMatch(/Enter details|J\/K scroll/);
    expect(m.screen()).toContain('↓ J more');
    await m.press('/');
    await m.press('zzz');
    await m.press(ENTER);
    expect(m.screen()).toContain('No key matches "zzz".');
    expect(lines(m).at(-1)).not.toMatch(/Enter details|J\/K scroll/);
    expect(m.screen()).not.toContain('↓ J more');
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
          expect(s, label).toContain('no ');
          expect(s, label).toMatch(/✓ no (open )?incidents/);
          expect(s, label).toContain('1 disabled');
          expect(s, label).toContain('1 revoked');
          expect(s, label).toContain(stale ? '▲ price' : 'price');
          expect(s.match(/▲ price/g) ?? [], label).toHaveLength(stale ? 1 : 0);
          if (extra > 0) expect(s, label).toMatch(/(to review|\d of \d+)/);
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
      const flat = () => m.screen().split('\n').map((l) => l.slice(44)).join('').replace(/[\s│┃]/g, '');
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
    longUpstream(client);
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
    expect(rows.find((l) => /^[│┃] old-proto/.test(l))).toContain('○ disabled');
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
    // Enter is offered once it would revoke: with the name typed.
    expect(m.screen().split('\n').at(-1)).not.toContain('Enter revoke');
    await m.press('checkout');
    expect(m.screen().split('\n').at(-1)).toContain('Enter revoke');
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
    const client = fakeClient();
    longUpstream(client);
    const m = mount(client, SMALL);
    await loaded(m);
    await m.press('3');
    for (let i = 0; i < 14; i++) await m.press('J');
    expect(m.screen()).toContain('↑ K back');
    await m.press('4');
    expect(m.screen()).not.toContain('↑ K back');
    m.unmount();
  });
});

describe('review round 2', () => {
  const lines = (m: ReturnType<typeof mount>) => m.screen().split('\n');
  const ESCAPE = '\u001b';
  const withBadUpstream = (client: ReturnType<typeof fakeClient>) =>
    client.upstreams.push({ ...upstream, id: 'up_2', name: 'ollama', host: 'ollama.internal', port: 11434, tls: false, credentials: [] });

  it('counts an upstream with no credential in Status, lists it, and opens it on Enter', async () => {
    const client = fakeClient();
    withBadUpstream(client);
    const m = mount(client, { columns: 100, rows: 30 });
    await loaded(m);
    expect(m.screen()).not.toContain('every key is capped and current');
    expect(m.screen()).toMatch(/Status !1 ▲1/);
    const row = lines(m).find((l) => l.includes('! ollama'))!;
    expect(row).toContain('no credential');
    // The worst comes first, so the upstream with no credential is the selected row.
    await m.press(ENTER);
    expect(lines(m).find((l) => l.includes('Upstreams ('))).toContain('Upstreams (2)');
    expect(m.screen()).toContain('Upstream: ollama');
    m.unmount();
  });

  it('says there is nothing to look at only when keys and upstreams are all fine', async () => {
    const client = fakeClient();
    client.keys = client.keys.map((k) => ({ ...k, status: 'disabled' as const }));
    const m = mount(client, { columns: 100, rows: 30 });
    await loaded(m);
    expect(m.screen()).toContain('No active keys');
    m.unmount();
  });

  it('puts the install address in the top border of Status, and in the line when stacked short', async () => {
    const wide = mount(fakeClient(), { columns: 100, rows: 30 });
    await loaded(wide);
    // At 100 columns the border has room for the address, and the summary has the rest.
    expect(lines(wide)[0]).toContain('127.0.0.1:8081');
    expect(lines(wide)[0]).toContain('owner @ 127.0.0.1:8081');
    wide.unmount();
    const small = mount(fakeClient(), { columns: 80, rows: 24 });
    await loaded(small);
    expect(lines(small)[0]).toContain('owner @ 127.0.0.1:8081');
    small.unmount();
  });

  it('shrinks the inactive panels to one line each at 80x24, so the detail has room', async () => {
    const m = mount(fakeClient(), { columns: 80, rows: 24 });
    await loaded(m);
    await m.press('2');
    for (const title of ['[1] Status', '[3] Upstreams', '[4] Audit']) {
      const row = lines(m).find((l) => l.includes(title))!;
      expect(row).not.toMatch(/[╭│╰]/);
    }
    expect(lines(m).find((l) => l.includes('[3] Upstreams'))).toContain('openai');
    expect(lines(m).find((l) => l.includes('[4] Audit'))).toContain('key.create');
    expect(lines(m).length).toBeLessThanOrEqual(24);
    m.unmount();
  });

  it('keeps the borders of the inactive panels when there are rows for them', async () => {
    const m = mount(fakeClient(), { columns: 80, rows: 40 });
    await loaded(m);
    await m.press('2');
    expect(lines(m).find((l) => l.includes('[4] Audit'))).toMatch(/^╭/);
    m.unmount();
  });

  it('names an audit row by its target, not by its kind twice', async () => {
    const m = mount(fakeClient(), { columns: 100, rows: 30 });
    await loaded(m);
    await m.press('4');
    const row = lines(m).find((l) => l.includes('11-17') && l.includes('key.create'))!;
    expect(row).toContain('k_web');
    expect(row).not.toMatch(/key\.create +key /);
    m.unmount();
  });

  it('lets Enter hand the keyboard to the detail panel, scroll it, and Esc take it back', async () => {
    const client = fakeClient();
    longUpstream(client);
    const m = mount(client, { columns: 80, rows: 24 });
    await loaded(m);
    await m.press('3');
    expect(lines(m).at(-1)).toContain('Enter details');
    await m.press(ENTER);
    expect(lines(m).at(-1)).toContain('Esc back to list');
    expect(m.screen()).toMatch(/1-\d+ of \d+/);
    await m.press('G');
    expect(m.screen()).toContain('/v1/route/39');
    await m.press(ESCAPE);
    expect(lines(m).at(-1)).not.toContain('Esc back to list');
    expect(lines(m).at(-1)).toContain('Enter details');
    m.unmount();
  });

  it('puts a gap between every key and its meaning in help, and fits 80x24', async () => {
    const m = mount(fakeClient(), { columns: 80, rows: 24 });
    await loaded(m);
    for (const panel of ['1', '2', '3', '4']) {
      await m.press(panel);
      await m.press('?');
      expect(m.screen()).toMatch(/⇧Tab +next panel/);
      expect(lines(m).find((l) => l.includes('Keybindings'))).toMatch(/╭─ Keybindings/);
      expect(lines(m).length).toBeLessThanOrEqual(24);
      await m.press(ESCAPE);
    }
    m.unmount();
  });

  it('does not offer selecting when a filter matches nothing', async () => {
    const m = mount(fakeClient(), { columns: 100, rows: 30 });
    await loaded(m);
    await m.press('2');
    await m.press('/');
    await m.press('zzz');
    expect(lines(m).at(-1)).not.toContain('select');
    expect(lines(m).at(-1)).toContain('Esc clear');
    m.unmount();
  });

  it('shows the dialogs with their title in the border, as the panels have it', async () => {
    const m = mount(fakeClient(), { columns: 100, rows: 30 });
    await loaded(m);
    await m.press('2');
    await m.press('n');
    expect(lines(m).find((l) => l.includes('New virtual key'))).toMatch(/^\s*╭─ New virtual key/);
    m.unmount();
  });
});

describe('review round 3', () => {
  const lines = (m: ReturnType<typeof mount>) => m.screen().split('\n');
  const ESCAPE = '\u001b';

  it('keeps the title and corners of the detail panel intact when its content overflows at 80x24', async () => {
    const client = fakeClient();
    longUpstream(client);
    const m = mount(client, { columns: 80, rows: 24 });
    await loaded(m);
    await m.press('3');
    const at = lines(m).findIndex((l) => l.includes('Upstream: openai'));
    expect(lines(m)[at]).toMatch(/^╭─ Upstream: openai ─+╮$/);
    expect(lines(m).find((l) => l.includes('↓ J more'))).toMatch(/^╰─+ ↓ J more ─╯$/);
    expect(lines(m)[at + 1]).toMatch(/^│ address/);
    m.unmount();
  });

  it('shares the wide column between the panels, each up to what it holds, with Keys first', async () => {
    const m = mount(fakeClient(), { columns: 140, rows: 40 });
    await loaded(m);
    await m.press('3');
    // Upstreams is focused and has one row, so Keys keeps all three of its rows.
    for (const key of ['checkout', 'ci-evals', 'old-proto']) expect(m.screen()).toContain(key);
    m.unmount();
  });

  it('counts what needs a look by how much, and the border says the same number', async () => {
    const client = fakeClient();
    client.upstreams.push({ ...upstream, id: 'up_2', name: 'ollama', host: 'ollama.internal', port: 11434, tls: false, credentials: [] });
    const m = mount(client, { columns: 140, rows: 40 });
    await loaded(m);
    expect(lines(m)[0]).toMatch(/Status !1 ▲1/);
    expect(m.screen()).toContain('1 of 2');
    expect(m.screen()).not.toContain('need attention');
    m.unmount();
  });

  it('puts no upstream in Status, and opens Upstreams on Enter', async () => {
    const client = fakeClient();
    client.upstreams = [];
    client.keys = [];
    const m = mount(client, { columns: 100, rows: 30 });
    await loaded(m);
    expect(lines(m).find((l) => l.includes('no upstream'))).toContain('kman upstream add');
    await m.press(ENTER);
    expect(m.screen()).toContain('No upstreams yet');
    m.unmount();
  });

  it('lines up the columns of the Keys list when they fit', async () => {
    const m = mount(fakeClient(), { columns: 140, rows: 40 });
    await loaded(m);
    await m.press('2');
    const rows = lines(m).filter((l) => /^[│┃] (checkout|ci-evals) /.test(l));
    expect(rows).toHaveLength(2);
    const at = (l: string) => l.indexOf('● active');
    expect(at(rows[0]!)).toBe(at(rows[1]!));
    expect(rows.join('\n')).not.toContain('$');
    m.unmount();
  });

  it('lists the routes of an upstream one to a line', async () => {
    const m = mount(fakeClient(), { columns: 100, rows: 30 });
    await loaded(m);
    await m.press('3');
    expect(lines(m).some((l) => /routes +POST \/v1\/chat\/completions/.test(l))).toBe(true);
    m.unmount();
  });

  it('only hands over the keyboard when the detail has more than it shows', async () => {
    const m = mount(fakeClient(), { columns: 100, rows: 30 });
    await loaded(m);
    await m.press('2');
    expect(lines(m).at(-1)).not.toContain('Enter details');
    await m.press(ENTER);
    expect(lines(m).at(-1)).not.toContain('Esc back to list');
    m.unmount();
  });

  it('ignores the list actions while the detail has the keyboard, and shows the position in its border', async () => {
    const client = fakeClient();
    longUpstream(client);
    const m = mount(client, { columns: 80, rows: 24 });
    await loaded(m);
    await m.press('3');
    await m.press(ENTER);
    await m.press('n');
    expect(m.screen()).not.toContain('New virtual key');
    expect(m.screen()).toMatch(/[╰┗][─━]+ 1-\d+ of \d+ [─━][╯┛]/);
    await m.press(ESCAPE);
    await m.press('n');
    expect(m.screen()).toContain('New virtual key');
    m.unmount();
  });
});

describe('review round 4', () => {
  const lines = (m: ReturnType<typeof mount>) => m.screen().split('\n');
  const ESCAPE = '\u001b';
  const cjk = '東京の請求チーム'.repeat(4);

  it('measures wide characters in cells, so a CJK key name cannot break the frame at 80x24', async () => {
    const client = fakeClient();
    client.keys[0]!.name = `${cjk}-east`;
    for (const size of [{ columns: 80, rows: 24 }, { columns: 100, rows: 30 }]) {
      const m = mount(client, size);
      await loaded(m);
      await m.press('2');
      expect(lines(m).length).toBeLessThanOrEqual(size.rows);
      const top = lines(m).find((l) => l.includes('Key: '))!;
      expect(top).toMatch(/╭─ Key: .*─+╮$/);
      expect(lines(m).find((l) => l.includes('● active'))).toBeDefined();
      m.unmount();
    }
  });

  it('shows the keys of the detail panel in help while it has the keyboard', async () => {
    const client = fakeClient();
    longUpstream(client);
    const m = mount(client, { columns: 80, rows: 24 });
    await loaded(m);
    await m.press('3');
    await m.press(ENTER);
    await m.press('?');
    expect(m.screen()).toContain('DETAIL (focused)');
    expect(m.screen()).not.toContain('KEYS (this panel)');
    expect(m.screen()).not.toMatch(/^ *returns/m);
    m.unmount();
  });

  it('offers only Esc in the new-key footer when there is no upstream to make a key on', async () => {
    const client = fakeClient();
    client.upstreams = [];
    const m = mount(client, { columns: 80, rows: 24 });
    await loaded(m);
    await m.press('n');
    expect(m.screen()).not.toContain('Ctrl+S');
    expect(lines(m).at(-1)).toContain('Esc close');
    expect(lines(m).at(-1)).not.toContain('next field');
    m.unmount();
  });

  it('keeps typed confirmation and its target across layout changes', async () => {
    const m = mount(fakeClient(), { columns: 140, rows: 40 });
    await loaded(m);
    await m.press('2');
    await m.press('x');
    await m.press('check');
    m.resize({ columns: 80, rows: 24 });
    await m.press('out');
    await until(() => m.screen().includes('Name matches.'), 'the preserved field');
    m.resize({ columns: 140, rows: 40 });
    await until(() => m.screen().includes('[2] Keys'), 'the wide popup');
    expect(m.screen()).toContain('Name matches.');
    expect(m.client.calls).toEqual([]);
    await m.press(ENTER);
    expect(m.client.calls).toEqual(['revoke k_checkout']);
    m.unmount();
  });

  it('keeps the panels in view beside a popup when the main panel can hold it', async () => {
    const m = mount(fakeClient(), { columns: 140, rows: 40 });
    await loaded(m);
    await m.press('2');
    await m.press('d');
    expect(m.screen()).toContain('Disable this key?');
    expect(m.screen()).toContain('[2] Keys');
    expect(m.screen()).toContain('[4] Audit');
    expect(lines(m).every((l) => [...l].length <= 140)).toBe(true);
    await m.press('n');
    m.unmount();
    // Where the main panel is too narrow for it the popup takes the screen, as before.
    const small = mount(fakeClient(), { columns: 100, rows: 30 });
    await loaded(small);
    await small.press('2');
    await small.press('d');
    expect(small.screen()).not.toContain('[4] Audit');
    small.unmount();
  });

  it('marks the same problem the same way in Status, the Upstreams list and its detail', async () => {
    const client = fakeClient();
    client.upstreams.push({ ...upstream, id: 'up_2', name: 'ollama', host: 'ollama.internal', port: 11434, tls: false, credentials: [] });
    const m = mount(client, { columns: 140, rows: 40 });
    await loaded(m);
    expect(lines(m).find((l) => l.includes('ollama'))).toContain('!');
    await m.press('3');
    await m.press('j');
    expect(lines(m).find((l) => /^[│┃] ollama/.test(l))).toContain('! no credential');
    expect(m.screen()).toContain('! none: add one');
    m.unmount();
  });

  it('puts no blank row above the first line of the confirmation', async () => {
    const m = mount(fakeClient(), { columns: 100, rows: 34 });
    await loaded(m);
    await m.press('2');
    await m.press('d');
    const at = lines(m).findIndex((l) => l.includes('Disable this key?'));
    expect(lines(m)[at + 1]).toMatch(/checkout/);
    await m.press(ESCAPE);
    m.unmount();
  });
});

describe('review round 5', () => {
  const lines = (m: ReturnType<typeof mount>) => m.screen().split('\n');
  const ESCAPE = '\u001b';

  it('keeps the revoke field on one row for a long wide-character name', async () => {
    const client = fakeClient();
    const name = '東京'.repeat(18);
    client.keys[0]!.name = name;
    const m = mount(client, { columns: 80, rows: 24 });
    await loaded(m);
    await m.press('2');
    await m.press('x');
    await m.press(name);
    const field = lines(m).filter((l) => l.includes('['));
    expect(field.length).toBeGreaterThan(0);
    for (const l of field) expect(l).toMatch(/\[.*\]/);
    expect(lines(m).length).toBeLessThanOrEqual(24);
    m.unmount();
  });

  it('lists only what works while the detail has the keyboard, in help', async () => {
    const client = fakeClient();
    longUpstream(client);
    const m = mount(client, { columns: 80, rows: 24 });
    await loaded(m);
    await m.press('3');
    await m.press(ENTER);
    await m.press('?');
    expect(m.screen()).not.toMatch(/\bn {2}r\b/);
    expect(m.screen()).not.toContain('new key');
    expect(m.screen()).toContain('give the keyboard back');
    await m.press(ESCAPE);
    m.unmount();
  });

  it('says only once, in the footer, that y confirms and n cancels', async () => {
    const m = mount(fakeClient(), { columns: 100, rows: 34 });
    await loaded(m);
    await m.press('2');
    await m.press('d');
    const hints = lines(m).filter((l) => /\by yes\b/.test(l));
    expect(hints).toHaveLength(1);
    expect(hints[0]).toBe(lines(m).at(-1));
    m.unmount();
  });

  it('ends the panels and the detail on the same row at 100x30 and 140x40', async () => {
    for (const size of [{ columns: 100, rows: 30 }, { columns: 140, rows: 40 }]) {
      const m = mount(fakeClient(), size);
      await loaded(m);
      const bottoms = lines(m).filter((l) => /[╯┛]/.test(l));
      // The last panel and the detail close on the last row of the body.
      const lastBody = lines(m).findLastIndex((l) => /[╯┛]/.test(l));
      expect(lines(m)[lastBody]).toMatch(/[╯┛].*[╯┛]/);
      expect(bottoms.length).toBeGreaterThan(0);
      m.unmount();
    }
  });
});

describe('review round 6', () => {
  const lines = (m: ReturnType<typeof mount>) => m.screen().split('\n');
  const ESCAPE = '\u001b';

  it('says what a key is where an empty install has nothing to show', async () => {
    const client = fakeClient();
    client.keys = [];
    const m = mount(client, { columns: 100, rows: 30 });
    await loaded(m);
    expect(m.screen()).toContain('Getting started');
    expect(m.screen()).toContain('Press n to create the first one.');
    expect(m.screen()).not.toContain('Recent activity');
    m.unmount();
  });

  it('keeps the filter prompt on the last row of the panel, with or without matches', async () => {
    const m = mount(fakeClient(), { columns: 100, rows: 30 });
    await loaded(m);
    await m.press('2');
    await m.press('/');
    await m.press('ci-');
    const withMatch = lines(m).findIndex((l) => l.includes('/ ci-'));
    await m.press('zzz');
    const none = lines(m).findIndex((l) => l.includes('/ ci-zzz'));
    expect(none).toBe(withMatch);
    m.unmount();
  });

  it('says the first thing to look at in a tall Status that is not focused', async () => {
    const m = mount(fakeClient(), { columns: 140, rows: 40 });
    await loaded(m);
    await m.press('2');
    expect(m.screen()).toMatch(/▲ ci-evals · near cap/);
    m.unmount();
  });

  it('separates an upstream\'s warnings from its count', async () => {
    const client = fakeClient();
    client.upstreams.push({ ...upstream, id: 'up_2', name: 'ollama', host: 'ollama.internal', port: 11434, tls: false, credentials: [] });
    const m = mount(client, { columns: 140, rows: 40 });
    await loaded(m);
    await m.press('3');
    expect(lines(m).find((l) => /^[│┃] ollama/.test(l))).toMatch(/http {2,}0 active/);
    m.unmount();
  });

  it('wraps a help line under its meaning, not under the key', async () => {
    const m = mount(fakeClient(), { columns: 80, rows: 24 });
    await loaded(m);
    await m.press('4');
    await m.press('?');
    const at = lines(m).findIndex((l) => l.includes('focus the detail'));
    expect(at).toBeGreaterThan(0);
    expect(lines(m)[at]).toMatch(/Enter +focus the detail/);
    await m.press(ESCAPE);
    m.unmount();
  });
});

describe('review round 7', () => {
  const lines = (m: ReturnType<typeof mount>) => m.screen().split('\n');

  it('keeps the Keys panel the same height while a filter narrows the list', async () => {
    const m = mount(fakeClient(), { columns: 100, rows: 30 });
    await loaded(m);
    await m.press('2');
    await m.press('/');
    const prompt = () => lines(m).findIndex((l) => /^[│┃] \/( |$)/.test(l));
    const first = prompt();
    await m.press('ci');
    expect(prompt()).toBe(first);
    await m.press('zz');
    expect(prompt()).toBe(first);
    m.unmount();
  });

  it('offers up and down only where there is more than one row to move over', async () => {
    const m = mount(fakeClient(), { columns: 140, rows: 40 });
    await loaded(m);
    await m.press('4');
    expect(lines(m).at(-1)).not.toContain('select');
    await m.press('2');
    expect(lines(m).at(-1)).toContain('select');
    m.unmount();
  });

  it('shows Getting started with Keys focused on an empty install too', async () => {
    const client = fakeClient();
    client.keys = [];
    const m = mount(client, { columns: 80, rows: 24 });
    await loaded(m);
    await m.press('2');
    expect(m.screen()).toContain('Getting started');
    expect(m.screen()).not.toMatch(/^ .*applies its limits/m);
    m.unmount();
  });

  it('says no position for a list of one, and no match in an empty filtered list', async () => {
    const m = mount(fakeClient(), { columns: 100, rows: 30 });
    await loaded(m);
    expect(m.screen()).not.toContain('1 of 1');
    await m.press('2');
    await m.press('/');
    await m.press('zzz');
    expect(m.screen()).toContain('no match');
    m.unmount();
  });

  it('puts an incident on its key row, and no spend, at 100x30 with a long-named, mixed set', async () => {
    const client = fakeClient();
    client.keys.push(
      key({ id: 'k_prod', name: 'prod-api', spend: { today: { period: 'd', cost_micro: 4_300_000, requests: 812, cap_micro: 5_000_000 }, month: { period: 'm', cost_micro: 61_000_000, requests: 9100, cap_micro: 200_000_000 } } }),
      key({ id: 'k_rev', name: 'leaked-demo', status: 'revoked' }),
    );
    client.incidents = [incident({ id: 3, key_id: 'k_prod', key_name: 'prod-api' })];
    const m = mount(client, { columns: 100, rows: 34 });
    await loaded(m);
    await m.press('2');
    expect(lines(m).find((l) => /^[│┃] prod-api/.test(l))).toContain('! incident');
    expect(lines(m).find((l) => /^[│┃] checkout/.test(l))).not.toContain('$');
    m.unmount();
  });
});

describe('review round 8', () => {
  const lines = (m: ReturnType<typeof mount>) => m.screen().split('\n');

  it('keeps the setup command whole on its own line in the empty install', async () => {
    const client = fakeClient();
    client.upstreams = [];
    client.keys = [];
    for (const columns of [100, 120]) {
      const m = mount(client, { columns, rows: 30 });
      await loaded(m);
      expect(lines(m).some((l) => l.includes('kman upstream add openai --preset openai'))).toBe(true);
      await m.press('3');
      expect(lines(m).some((l) => l.includes('kman upstream add openai --preset openai'))).toBe(true);
      expect(m.screen()).not.toMatch(/^[│┃] +\S* ?--preset/m);
      m.unmount();
    }
  });

  it('says a server error is not offline', async () => {
    const client = fakeClient();
    const m = mount(client, { columns: 100, rows: 30 });
    await loaded(m);
    client.fail = new ApiError('internal', 'SQLITE_BUSY', 500);
    await m.press('r');
    await until(() => m.screen().includes('server error'), 'the server error flag');
    expect(m.screen()).not.toContain('offline');
    m.unmount();
  });

  it('ends an unfocused Status on the first thing to look at, not on the price', async () => {
    const client = fakeClient();
    client.upstreams.push({ ...upstream, id: 'up_2', name: 'ollama', host: 'ollama.internal', port: 11434, tls: false, credentials: [] });
    const m = mount(client, { columns: 100, rows: 30 });
    await loaded(m);
    await m.press('2');
    expect(lines(m).find((l) => /! ollama · no credential/.test(l))).toBeDefined();
    m.unmount();
  });

  it('says the connection once, in the footer, when it is live', async () => {
    const m = mount(fakeClient(), { columns: 100, rows: 30 });
    await loaded(m);
    expect(m.screen().match(/● live/g)).toHaveLength(1);
    expect(lines(m).at(-1)).toContain('● live');
    m.unmount();
  });

  it('keeps up and down and the panel keys in the footer at 80x24, ahead of c IPs', async () => {
    const client = fakeClient();
    client.keys.push(key({ id: 'k_x', name: 'x' }), key({ id: 'k_y', name: 'y' }));
    const m = mount(client, { columns: 80, rows: 24 });
    await loaded(m);
    await m.press('2');
    expect(lines(m).at(-1)).toContain('select');
    m.unmount();
  });
});

describe('review round 9', () => {
  const lines = (m: ReturnType<typeof mount>) => m.screen().split('\n');

  it('puts the first thing to look at on the one-line Status at 80x24', async () => {
    const client = fakeClient();
    client.upstreams.push({ ...upstream, id: 'up_2', name: 'ollama', host: 'ollama.internal', port: 11434, tls: false, credentials: [] });
    const m = mount(client, { columns: 80, rows: 24 });
    await loaded(m);
    await m.press('2');
    expect(lines(m).find((l) => l.includes('[1] Status'))).toContain('! ollama · no credential');
    m.unmount();
  });

  it('keeps the panel keys in the footer ahead of revoke at 80x24, and a gap before the connection', async () => {
    const m = mount(fakeClient(), { columns: 80, rows: 24 });
    await loaded(m);
    await m.press('2');
    expect(lines(m).at(-1)).toContain('1-4 panels');
    expect(lines(m).at(-1)).toMatch(/q quit {2,}● live/);
    m.unmount();
  });

  it('names a refused answer by its kind, not as a server error', async () => {
    const client = fakeClient();
    const m = mount(client, { columns: 100, rows: 30 });
    await loaded(m);
    client.fail = new ApiError('conflict', 'busy', 409);
    await m.press('r');
    await until(() => m.screen().includes('refused'), 'the refused flag');
    expect(m.screen()).not.toContain('server error');
    client.fail = new ApiError('internal', 'boom', 500);
    await m.press('r');
    await until(() => m.screen().includes('server error'), 'the server error flag');
    m.unmount();
  });

  it('keeps updated first and the same row order in a focused and an unfocused Status', async () => {
    const client = fakeClient();
    client.keys.push(key({ id: 'k_n', name: 'n', spend: undefined }), key({ id: 'k_m', name: 'm', spend: undefined }));
    const m = mount(client, { columns: 80, rows: 24 });
    await loaded(m);
    expect(lines(m)[1]).toContain('updated');
    m.unmount();
  });

  it('says so when Enter has nothing more to show', async () => {
    const m = mount(fakeClient(), { columns: 100, rows: 34 });
    await loaded(m);
    await m.press('2');
    await m.press(ENTER);
    expect(m.screen()).toContain('The whole detail is already showing.');
    m.unmount();
  });

  it('gives the relative time of an expiring key once', async () => {
    const client = fakeClient();
    client.keys[0]!.expires_at = new Date(Date.now() + 3 * 86_400_000).toISOString();
    const m = mount(client, { columns: 100, rows: 34 });
    await loaded(m);
    await m.press('2');
    expect(m.screen()).not.toMatch(/\(in [23]d\)/);
    expect(m.screen()).toMatch(/expires in \d days/);
    m.unmount();
  });
});

describe('review round 10', () => {
  const lines = (m: ReturnType<typeof mount>) => m.screen().split('\n');

  it('says an unreadable successful reply is an invalid reply', async () => {
    const client = fakeClient();
    const m = mount(client, { columns: 100, rows: 30 });
    await loaded(m);
    client.fail = new ApiError('internal', 'the admin API sent a reply that is not JSON', 200);
    await m.press('r');
    await until(() => m.screen().includes('invalid reply'), 'the invalid reply flag');
    m.unmount();
  });

  it('keeps the panel keys ahead of J/K in the footer when the detail scrolls at 80x24', async () => {
    const client = fakeClient();
    longUpstream(client);
    const m = mount(client, { columns: 80, rows: 24 });
    await loaded(m);
    await m.press('3');
    expect(lines(m).at(-1)).toContain('1-4 panels');
    m.unmount();
  });

  it('collapses the zeros of an empty install to one line', async () => {
    const client = fakeClient();
    client.keys = [];
    const m = mount(client, { columns: 80, rows: 24 });
    await loaded(m);
    expect(m.screen()).toContain('No keys yet');
    expect(m.screen()).not.toContain('0 disabled');
    expect(m.screen()).not.toContain('$0 today');
    m.unmount();
  });

  it('puts the price on the first line of Status at 80x24 when it is fresh', async () => {
    const m = mount(fakeClient(), { columns: 80, rows: 24 });
    await loaded(m);
    expect(lines(m).find((l) => l.includes('updated'))).toMatch(/price 3d/);
    m.unmount();
  });

  it('says what a second problem on a row is, and what the more in the Status line are', async () => {
    const client = fakeClient();
    client.upstreams.push({ ...upstream, id: 'up_2', name: 'ollama', host: 'ollama.internal', port: 11434, tls: false, credentials: [] });
    const m = mount(client, { columns: 100, rows: 30 });
    await loaded(m);
    expect(lines(m).find((l) => l.includes('! ollama'))).toContain('+1 issue');
    await m.press('2');
    expect(m.screen()).toMatch(/· \+1 more/);
    m.unmount();
  });
});

describe('review round 12', () => {
  const lines = (m: ReturnType<typeof mount>) => m.screen().split('\n');

  it('does not tell anyone to start kman when a first load is answered with an error', async () => {
    const client = fakeClient();
    client.fail = new ApiError('internal', 'SQLITE_BUSY', 500);
    const m = mount(client, { columns: 100, rows: 30 });
    await until(() => m.screen().includes('server error'), 'the server error flag');
    expect(m.screen()).toContain('kman answered with an error');
    expect(m.screen()).not.toContain('kman serve');
    expect(m.screen()).not.toContain('Cannot reach kman');
    m.unmount();
  });

  it('keeps the incidents ahead of the owner line when the title carries two badges', async () => {
    const client = fakeClient();
    client.upstreams.push({ ...upstream, id: 'up_2', name: 'ollama', host: 'ollama.internal', port: 11434, tls: false, credentials: [] });
    const m = mount(client, { columns: 100, rows: 30 });
    await loaded(m);
    expect(m.screen()).toContain('✓ no open incidents');
    m.unmount();
  });

  it('sizes a focused Status on an empty install to its one line', async () => {
    const client = fakeClient();
    client.keys = [];
    const m = mount(client, { columns: 100, rows: 30 });
    await loaded(m);
    const at = lines(m).findIndex((l) => l.includes('Nothing to review.'));
    expect(at).toBeGreaterThan(0);
    expect(lines(m)[at + 1]).toMatch(/^╰|^┗/);
    m.unmount();
  });
});

describe('review round 13', () => {
  const lines = (m: ReturnType<typeof mount>) => m.screen().split('\n');

  it('keeps updated on the first line of a focused Status on an empty install', async () => {
    const client = fakeClient();
    client.keys = [];
    const m = mount(client, { columns: 100, rows: 30 });
    await loaded(m);
    expect(lines(m)[1]).toContain('updated');
    m.unmount();
  });

  it('shows the heading and the row, and no blank row, in Status with no keys and no upstream', async () => {
    const client = fakeClient();
    client.keys = [];
    client.upstreams = [];
    const m = mount(client, { columns: 100, rows: 30 });
    await loaded(m);
    const at = lines(m).findIndex((l) => l.includes('! no upstream'));
    expect(at).toBeGreaterThan(0);
    expect(lines(m)[at + 1]).toMatch(/^╰|^┗/);
    m.unmount();
  });

  it('keeps the end of a long key name beside its reason on the Status line', async () => {
    const client = fakeClient();
    client.keys[1]!.name = 'customer-support-summariser-production-eu-west-b';
    const m = mount(client, { columns: 120, rows: 30 });
    await loaded(m);
    await m.press('2');
    expect(lines(m).find((l) => l.includes('near cap') && l.includes('…'))).toMatch(/west-b · near cap/);
    m.unmount();
  });

  it('says who is signed in once: on the top border, or on the bottom one when the top has no room', async () => {
    const m = mount(fakeClient(), { columns: 100, rows: 30 });
    await loaded(m);
    await m.press('2');
    expect(m.screen().match(/owner/g)).toHaveLength(1);
    expect(m.screen()).toContain('1 to review');
    m.unmount();
  });

  it('closes the notice when help opens', async () => {
    const m = mount(fakeClient(), { columns: 100, rows: 34 });
    await loaded(m);
    await m.press('2');
    await m.press(ENTER);
    expect(m.screen()).toContain('already showing');
    await m.press('?');
    expect(m.screen()).not.toContain('already showing');
    m.unmount();
  });

  it('leaves no blank rows under the revoke input before anything is typed', async () => {
    const m = mount(fakeClient(), { columns: 100, rows: 34 });
    await loaded(m);
    await m.press('2');
    await m.press('x');
    const at = lines(m).findIndex((l) => /\[ .* \]|\[.{10,}\]/.test(l) && l.includes('['));
    expect(at).toBeGreaterThan(0);
    expect(lines(m)[at + 1]).toMatch(/^\s*[╰┗]/);
    m.unmount();
  });
});

describe('a note mark', () => {
  // The mark for a note is the dot that also parts a summary line: it must not be dropped
  // as a separator that opens a line, nor doubled after one.
  const onlyANote = () => {
    const client = fakeClient();
    client.keys = [key({ id: 'k_a', name: 'solo', spend: undefined })];
    return client;
  };

  it('stays in front of the first thing to look at under an unfocused Status', async () => {
    const m = mount(onlyANote(), { columns: 100, rows: 34 });
    await loaded(m);
    await m.press('2');
    expect(m.screen()).toMatch(/[│┃] · solo · no cap/);
    m.unmount();
  });

  it('is not doubled after a separator in the one-line Status', async () => {
    const client = onlyANote();
    // A stale price comes first on that line, so something is there to part the note from.
    client.priceStatus = async () => ({ age_days: 99, generated_at: '2026-08-10T00:00:00Z', stale: true });
    const m = mount(client, { columns: 80, rows: 24 });
    await loaded(m);
    await m.press('2');
    const strip = m.screen().split('\n').find((l) => l.includes('[1] Status'))!;
    expect(strip).toContain('▲ price stale · solo · no cap');
    expect(strip).not.toContain('· · ');
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
describe('who and where come first', () => {
  const lines = (m: ReturnType<typeof mount>) => m.screen().split('\n');

  it('leads Status with an open incident, ahead of a key near its cap', async () => {
    const client = fakeClient();
    client.incidents = [incident({ id: 7, key_id: 'k_checkout', key_name: 'checkout' })];
    const m = mount(client);
    await loaded(m);
    expect(m.screen()).toContain('! 1 open incident');
    // The money is in the key detail, not in Status.
    expect(lines(m).map((l) => l.slice(0, 44)).join('\n')).not.toContain('$');
    const first = lines(m).findIndex((l) => l.includes('NEEDS ATTENTION'));
    expect(lines(m)[first + 1]).toMatch(/! checkout +new country \+1/);
    // Its detail names the incident and the command that shows it.
    expect(m.screen()).toContain('incident #7 (high): new country, new ASN');
    expect(m.screen()).toContain('kman incident show 7');
    m.unmount();
  });

  it('shows where a key was used from, refused requests marked, before its policy and spend', async () => {
    const client = fakeClient();
    client.places.k_checkout = {
      key_id: 'k_checkout',
      places: [
        place({ kind: 'country', value: 'DE', requests: 0, refused: 57 }),
        place({ kind: 'asn', value: '60729', label: 'Example Exit', requests: 0, refused: 57 }),
        place({ kind: 'client', value: 'curl/8', requests: 0, refused: 57 }),
        place({ kind: 'country', value: 'US', requests: 420 }),
      ],
      networks_withheld: true,
    };
    const m = mount(client, { columns: 140, rows: 40 });
    await loaded(m);
    await m.press('2');
    await until(() => m.screen().includes('57 refused'), 'the places');
    const s = m.screen();
    expect(s).toMatch(/country +DE .*57 refused/);
    expect(s).toMatch(/ +US .*420 fwd/);
    expect(s).toContain('AS60729 Example Exit');
    expect(s).toMatch(/client +curl\/8/);
    expect(s).toContain('kman settings set expose_client_addresses on');
    expect(s.indexOf('WHERE IT IS USED')).toBeLessThan(s.indexOf('POLICY'));
    expect(s.indexOf('POLICY')).toBeLessThan(s.indexOf('SPEND'));
    m.unmount();
  });

  it('ranks a key open to every address above one near its cap, with the fix', async () => {
    const client = fakeClient();
    client.findings = [
      { key_id: 'k_checkout', name: 'checkout', rule: 'open_to_every_address', message: 'open', fix: `kman key update checkout --policy '{"allowed_cidrs":["<network/bits>"]}'` },
    ];
    const m = mount(client);
    await loaded(m);
    const first = lines(m).findIndex((l) => l.includes('NEEDS ATTENTION'));
    expect(lines(m)[first + 1]).toMatch(/▲ checkout +open to all/);
    expect(lines(m)[first + 2]).toContain('ci-evals');
    expect(m.screen()).toContain('▲ 1 exposed');
    expect(m.screen()).toContain('kman key update checkout --policy');
    m.unmount();
  });

  it('lists a canary incident, which has no key row, and says how to answer it', async () => {
    const client = fakeClient();
    client.incidents = [incident({ id: 9, key_id: 'k_canary', key_name: 'wiki-canary', severity: 'critical', rules: ['canary'] })];
    const m = mount(client);
    await loaded(m);
    const first = lines(m).findIndex((l) => l.includes('NEEDS ATTENTION'));
    expect(lines(m)[first + 1]).toMatch(/! wiki-canary +canary used/);
    expect(m.screen()).toContain('Incident #9');
    // By id: a revoked canary may have passed its name on.
    expect(m.screen()).toContain('kman canary show k_canary');
    expect(m.screen()).toContain('kman canary reset k_canary');
    // There is no key to open: Enter opens the incident itself.
    expect(m.screen()).toContain('Enter incident');
    await m.press(ENTER);
    expect(m.screen()).toContain('Incident #9 · wiki-canary');
    expect(m.screen()).toContain('z reset canary');
    m.unmount();
  });

  it('says nothing about incidents to an older kman that does not list them', async () => {
    const client = fakeClient();
    client.openIncidents = async () => {
      throw new ApiError('not_found', 'no route', 404);
    };
    const m = mount(client);
    await loaded(m);
    expect(m.screen()).not.toContain('incident');
    m.unmount();
  });

  /** A request that failed, as against a route that is not there. */
  const broken = async (): Promise<never> => {
    throw new ApiError('internal', 'database is locked', 500);
  };

  it('keeps the last incidents, marked old, when they stop loading', async () => {
    const client = fakeClient();
    client.incidents = [incident({ id: 7, key_id: 'k_checkout', key_name: 'checkout' })];
    const m = mount(client, { columns: 120, rows: 34 });
    await loaded(m);
    client.openIncidents = broken;
    await m.press('r');
    await until(() => m.screen().includes('(old)'), 'the old mark');
    const s = m.screen();
    expect(s).toContain('! 1 open incident (old)');
    expect(s).toMatch(/! checkout +new country \+1/);
    expect(s).toMatch(/▲ incidents +not updated/);
    expect(s).toContain('incidents did not load: database is locked');
    m.unmount();
  });

  it('never says there is nothing to review while the incidents are unknown', async () => {
    for (const size of [SIZE, { columns: 80, rows: 24 }, { columns: 140, rows: 40 }]) {
      const client = fakeClient([key({ id: 'k_quiet', name: 'quiet' })]);
      client.openIncidents = broken;
      const m = mount(client, size);
      await loaded(m);
      const s = m.screen();
      expect(s).toContain('incidents unknown');
      expect(s).not.toContain('nothing to review');
      expect(s).not.toMatch(/no (open )?incidents/);
      m.unmount();
    }
  });

  it('says so in a key\'s detail and in Status when exposure does not load', async () => {
    const client = fakeClient([key({ id: 'k_quiet', name: 'quiet' })]);
    client.exposure = broken;
    const m = mount(client, { columns: 140, rows: 40 });
    await loaded(m);
    expect(m.screen()).toContain('exposure unknown');
    expect(m.screen()).not.toContain('nothing to review');
    await m.press('2');
    expect(m.screen()).toContain('exposure unknown: database is locked');
    m.unmount();
  });

  it('ranks a medium or acknowledged incident above a key at its cap, and names it on the row', async () => {
    const capped = key({ id: 'k_capped', name: 'capped' });
    capped.spend!.today.cost_micro = capped.spend!.today.cap_micro!;
    const leaked = key({ id: 'k_leaked', name: 'leaked' });
    leaked.spend!.today.cost_micro = leaked.spend!.today.cap_micro!;
    const old = key({ id: 'k_old', name: 'old', expired: true });
    const client = fakeClient([capped, old, leaked]);
    client.incidents = [incident({ id: 3, key_id: 'k_leaked', severity: 'medium', rules: ['new_asn'] })];
    const m = mount(client, { columns: 120, rows: 34 });
    await loaded(m);
    const first = lines(m).findIndex((l) => l.includes('NEEDS ATTENTION'));
    expect(lines(m)[first + 1]).toMatch(/▲ leaked +new ASN \+1 issue/);
    expect(m.screen()).toContain('▲ 1 open incident');
    await m.press('2');
    expect(m.screen()).toMatch(/leaked +● active +▲ incident/);
    m.unmount();
  });

  it('leaves out where a key was used on a kman without that route', async () => {
    const client = fakeClient();
    client.keyPlaces = async () => {
      throw new ApiError('not_found', 'no such admin API route', 404);
    };
    const m = mount(client, { columns: 140, rows: 40 });
    await loaded(m);
    await m.press('2');
    expect(m.screen()).toContain('POLICY');
    expect(m.screen()).not.toContain('WHERE IT IS USED');
    expect(m.screen()).not.toContain('Could not load');
    m.unmount();
  });

  it('marks places that stop loading, and keeps what it had', async () => {
    const client = fakeClient();
    client.places.k_checkout = { key_id: 'k_checkout', places: [place({ kind: 'country', value: 'DE' })] };
    const m = mount(client, { columns: 140, rows: 40 });
    await loaded(m);
    await m.press('2');
    await until(() => /country +DE/.test(m.screen()), 'the places');
    client.keyPlaces = broken;
    await m.press('r');
    await until(() => m.screen().includes('not updated: database is locked'), 'the mark');
    expect(m.screen()).toMatch(/country +DE/);
    m.unmount();
  });

  it('does not call a key unused when all it has to show is hidden networks', async () => {
    const client = fakeClient();
    client.places.k_checkout = { key_id: 'k_checkout', places: [], networks_withheld: true };
    const m = mount(client, { columns: 140, rows: 40 });
    await loaded(m);
    await m.press('2');
    await until(() => m.screen().includes('networks hidden'), 'the hint');
    expect(m.screen()).not.toContain('Not used yet');
    m.unmount();
  });

  it('names a revoked key by its id in the command for the rest of its places', async () => {
    const client = fakeClient([key({ id: 'k_gone', name: 'leaked', status: 'revoked' })]);
    client.places.k_gone = { key_id: 'k_gone', places: ['DE', 'US', 'NL', 'FR'].map((value) => place({ kind: 'country', value })) };
    const m = mount(client, { columns: 140, rows: 40 });
    await loaded(m);
    await m.press('2');
    await until(() => m.screen().includes('+1 more'), 'the places');
    expect(m.screen()).toContain('+1 more: kman key places k_gone');
    m.unmount();
  });

  it('keeps each count whole and a network\'s width, however narrow the detail', async () => {
    const client = fakeClient();
    client.places.k_checkout = {
      key_id: 'k_checkout',
      places: [
        place({ kind: 'asn', value: '60729', label: 'An Organisation With A Very Long Name', requests: 12_500, refused: 57 }),
        place({ kind: 'network', value: '2001:db8:1234:5678::/64', requests: 12_500, refused: 57 }),
      ],
    };
    for (const columns of [80, 100, 120, 140]) {
      const m = mount(client, { columns, rows: 40 });
      await loaded(m);
      await m.press('2');
      await until(() => m.screen().includes('/64'), `the places at ${columns}`);
      expect(m.screen().match(/13k fwd · 57 refused/g)).toHaveLength(2);
      expect(m.screen()).toContain('2001:db8:1234:5678::/64');
      m.unmount();
    }
  });

  it('shows a fix command whole, however long', async () => {
    const fix = `kman key update checkout --policy '{"allowed_cidrs":["<network/bits>"],"daily_cap_micro":5000000}'`;
    const client = fakeClient();
    client.findings = [{ key_id: 'k_checkout', rule: 'open_to_every_address', message: '', fix }];
    for (const columns of [100, 120]) {
      const m = mount(client, { columns, rows: 40 });
      await loaded(m);
      // The detail's lines, joined: where the command wrapped does not matter.
      const x = lines(m)[0]!.indexOf('╭─ Key');
      const detail = lines(m).map((l) => l.slice(x + 1).replace(/│\s*$/, '').trim()).join('');
      expect(detail.replace(/\s/g, '')).toContain(fix.replace(/\s/g, ''));
      m.unmount();
    }
  });

  it('says a key with no expiry never expires, not that it is about to', async () => {
    const client = fakeClient([key({ id: 'k_forever', name: 'forever', expires_at: '0001-01-01T00:00:00Z' })]);
    client.findings = [{ key_id: 'k_forever', rule: 'never_expires', message: '', fix: 'kman key update forever --ttl 90d' }];
    const m = mount(client, { columns: 140, rows: 40 });
    await loaded(m);
    expect(m.screen()).toContain('never expires');
    expect(m.screen()).not.toContain('expires within a day');
    expect(m.screen()).not.toMatch(/expires \d/);
    m.unmount();
  });
});
