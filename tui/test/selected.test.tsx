import { afterAll, afterEach, describe, expect, it, vi } from 'vitest';
import { key } from './fakes.js';
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

describe('the Keys panel', () => {
  it('dims active keys, and not disabled or revoked ones', async () => {
    vi.resetModules();
    const { KeysPanel } = await import('../src/views/panels.js');
    const keys = [key({ id: 'a', name: 'working' }), key({ id: 'b', name: 'paused', status: 'disabled' }), key({ id: 'c', name: 'ended', status: 'revoked' })];
    const app = render(
      <KeysPanel width={60} height={10} focused={false} keys={keys} all={keys} total={3} filter="" filtering={false} hasUpstreams now={0} />,
      60,
    );
    // What is between the name and the panel's right border: the mark and the word.
    const status = (name: string) =>
      (app.lastFrame() ?? '').split('\n').find((l) => l.includes(name))!.split(name)[1]!.replace(/(\u001b\[[0-9;]*m)*│.*$/, '');
    expect(status('working')).toContain(DIM);
    expect(status('paused')).not.toContain(DIM);
    expect(status('ended')).not.toContain(DIM);
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
