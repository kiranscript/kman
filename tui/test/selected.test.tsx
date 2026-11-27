import { afterAll, afterEach, describe, expect, it, vi } from 'vitest';
import { fakeClient, incident, key, tick, until } from './fakes.js';
import { render } from './render.js';

// The colours the selected line is drawn with. Ink only writes them when told the output
// takes colour, which has to be said before Ink is loaded.
async function draw(env: { NO_COLOR?: string }, focused: boolean): Promise<string> {
  vi.resetModules();
  vi.stubEnv('NO_COLOR', env.NO_COLOR ?? '');
  const { Row } = await import('../src/ui/row.js');
  const { color } = await import('../src/ui/theme.js');
  const app = render(
    <Row
      width={30}
      selected
      focused={focused}
      left={[{ t: '! ', color: color.danger }, { t: 'prod-api' }]}
      right={[{ t: 'incident', color: color.danger }, { t: ' old', dim: true }]}
    />,
  );
  const frame = app.lastFrame() ?? '';
  app.unmount();
  return frame;
}

const forced = vi.hoisted(() => {
  const was = process.env.FORCE_COLOR;
  process.env.FORCE_COLOR = '1';
  return { was };
});

afterEach(() => vi.unstubAllEnvs());
afterAll(() => {
  if (forced.was === undefined) delete process.env.FORCE_COLOR;
  else process.env.FORCE_COLOR = forced.was;
});

const esc = '\u001b[';
const RED = `${esc}31m`;
/** Any foreground colour but the default. */
const ANY_FOREGROUND = /\u001b\[(?:3[0-7]|9[0-7]|38;[0-9;]+)m/;
const BLUE_BG = `${esc}44m`;
const DIM = `${esc}2m`;
const INVERSE = `${esc}7m`;
const plain = (s: string) => s.replace(/\u001b\[[0-9;]*m/g, '');

describe.each([{ NO_COLOR: '' }, { NO_COLOR: '1' }])('what is dim, with NO_COLOR=$NO_COLOR', ({ NO_COLOR }) => {
  it('in Keys is the active ones, mark and word, and nothing of the others', async () => {
    vi.resetModules();
    vi.stubEnv('NO_COLOR', NO_COLOR);
    const { KeysPanel } = await import('../src/views/panels.js');
    const keys = [
      key({ id: 'a', name: 'working' }),
      key({ id: 'b', name: 'paused', status: 'disabled' }),
      key({ id: 'c', name: 'ended', status: 'revoked' }),
      key({ id: 'd', name: 'lapsed', expired: true }),
    ];
    const app = render(
      <KeysPanel width={60} height={10} focused={false} keys={keys} all={keys} total={4} filter="" filtering={false} hasUpstreams now={0} />,
      60,
    );
    // The line between the panel's borders, which are dim themselves when it is not focused.
    const inside = (name: string) =>
      (app.lastFrame() ?? '').split('\n').find((l) => l.includes(name))!.replace(/^[^│]*│(\u001b\[[0-9;]*m)*/, '').replace(/(\u001b\[[0-9;]*m)*│[^│]*$/, '');
    // The mark itself is dim, not only the word after it.
    expect(inside('working')).toMatch(/\u001b\[2m(\u001b\[[0-9;]*m)*●/);
    // And the word: no end of dim between where it starts and the word.
    expect(inside('working')).toMatch(/\u001b\[2m(?:(?!\u001b\[22m).)*active/);
    for (const name of ['paused', 'ended', 'lapsed']) expect(inside(name), name).not.toContain(DIM);
    app.unmount();
  });

  it('in a key’s detail is the command under its incident', async () => {
    vi.resetModules();
    vi.stubEnv('NO_COLOR', NO_COLOR);
    const { App } = await import('../src/App.js');
    const client = fakeClient();
    client.incidents = [incident({ id: 12, key_id: 'k_checkout', key_name: 'checkout' })];
    const app = render(<App client={client} url="http://127.0.0.1:8081" size={{ columns: 140, rows: 40 }} intervalMs={60_000} />, 140);
    await until(() => plain(app.lastFrame() ?? '').includes('live'), 'the first load');
    app.stdin.write('2');
    await tick(150);
    const line = (text: string) => (app.lastFrame() ?? '').split('\n').find((l) => plain(l).includes(text))!;
    expect(line('kman incident show 12')).toMatch(/\u001b\[2m(\u001b\[[0-9;]*m)*kman incident show 12/);
    expect(line('Enter to answer')).not.toMatch(/\u001b\[2m(\u001b\[[0-9;]*m)*[!▲] incident/);
    app.unmount();
  });
});

describe('the selected line', () => {
  it('is drawn in the default foreground on the bar of the focused panel', async () => {
    const frame = await draw({}, true);
    expect(frame).toContain(BLUE_BG);
    expect(frame).not.toMatch(ANY_FOREGROUND);
    expect(frame).not.toContain(DIM);
    // The mark and the word still say how severe it is.
    expect(plain(frame)).toMatch(/^! prod-api +incident old$/);
  });

  it('keeps its colours in a panel that is not focused', async () => {
    const frame = await draw({}, false);
    expect(frame).not.toContain(BLUE_BG);
    expect(frame).toContain(RED);
  });

  it('is inverse, with no colour and nothing dim, under NO_COLOR', async () => {
    const frame = await draw({ NO_COLOR: '1' }, true);
    expect(frame).toContain(INVERSE);
    expect(frame).not.toContain(BLUE_BG);
    expect(frame).not.toContain(RED);
    expect(frame).not.toContain(DIM);
  });
});
