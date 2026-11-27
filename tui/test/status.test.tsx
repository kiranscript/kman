// What Status must keep on screen at every size it can be given: the position, which install
// this is and who is signed in, and how many incidents are open.
import { describe, expect, it } from 'vitest';
import type { Key } from '../src/api.js';
import { attention, unknowns } from '../src/risk.js';
import { attentionItems, StatusPanel } from '../src/views/panels.js';
import { incident, key, NOW } from './fakes.js';
import { render } from './render.js';

const LOCAL = 'http://127.0.0.1:8081';
const REMOTE = 'https://kman.prod.internal:8443';
const LONG = 'https://kman-admin.production.eu-west.example.com:8443';
const ACTORS = ['owner', 'github-actions-deploy-production'];

interface Case {
  width: number;
  height: number;
  focused: boolean;
  url: string;
  actor: string;
  incidents: number;
  keys: number;
  link?: 'down';
}

function draw(c: Case) {
  const keys: Key[] = Array.from({ length: c.keys }, (_, i) => key({ id: `k_${i}`, name: `key-${i}`, spend: undefined }));
  const watch = { incidents: Array.from({ length: c.incidents }, (_, i) => incident({ id: i + 1, key_id: `k_${i}`, key_name: `key-${i}` })), findings: [] };
  const items = attentionItems(attention(keys, NOW, watch), [], 1, [], unknowns(watch));
  const link = c.link ? ({ state: 'down', error: 'refused', lastOk: Date.now() } as const) : ({ state: 'ok', at: Date.now() } as const);
  const app = render(
    <StatusPanel width={c.width} height={c.height} focused={c.focused} link={link} url={c.url} actor={c.actor} items={items} keys={keys} upstreamCount={1} watch={watch} selected={0} price={{ age_days: 45, stale: false } as never} />,
    c.width,
  );
  const lines = (app.lastFrame() ?? '').split('\n');
  app.unmount();
  return { lines, screen: lines.join('\n'), bottom: lines[lines.length - 1]!, items: items.length };
}

describe('Status at every size', () => {
  const cases: Case[] = [];
  // The narrowest column, one an address widens and the widest; Status short, middling and
  // tall.
  for (const width of [44, 49, 56])
    for (const height of [4, 5, 8])
      for (const focused of [true, false])
        for (const url of [LOCAL, REMOTE, LONG])
          for (const actor of ACTORS)
            for (const incidents of [0, 2, 10]) cases.push({ width, height, focused, url, actor, incidents, keys: 13 });

  it('keeps the position whole, the install, the owner and the incidents', () => {
    const wrong: string[] = [];
    for (const c of cases) {
      const { lines, screen, bottom, items } = draw(c);
      const say = (what: string) => wrong.push(`${what}: ${JSON.stringify(c)}\n${screen}`);
      if (lines.some((l) => [...l].length > c.width)) say('too wide');
      if (c.focused && items > 1 && !bottom.includes(`1 of ${items} `)) say('position');
      if (!c.focused && items > 0 && !bottom.includes(`${items} to review`)) say('to review');
      // The start of the host and its port, and the start of the owner's name.
      const host = c.url.replace(/^https?:\/\//, '');
      if (!screen.includes(host.slice(0, 4)) || !screen.includes(host.slice(-4))) say('install');
      if (!screen.includes(c.actor.slice(0, 3))) say('owner');
      if (c.incidents && !new RegExp(`${c.incidents} (open )?incidents`).test(screen)) say('incidents');
    }
    expect(wrong.slice(0, 5).join('\n\n'), `${wrong.length} of ${cases.length}`).toBe('');
  }, 30_000);

  it('cuts no line mid-word when offline, and fills none to the border', () => {
    for (const width of [44, 48]) {
      const { screen } = draw({ width, height: 8, focused: false, url: LONG, actor: ACTORS[1]!, incidents: 2, keys: 3, link: 'down' });
      expect(screen).toContain('offline');
      expect(screen).not.toMatch(/prices[^│\n]*…/);
      for (const l of screen.split('\n').slice(1, -1)) expect(l, l).toMatch(/ [│┃]$/);
    }
  });
});
