import { describe, expect, it } from 'vitest';
import { COLLAPSED, layout, listArea } from '../src/layout.js';
import type { Tab } from '../src/ui/chrome.js';

const TABS: Tab[] = ['status', 'keys', 'upstreams', 'audit', 'requests'];

describe('layout', () => {
  it.each([
    [80, 22],
    [100, 22],
    [100, 32],
    [140, 40],
    [200, 60],
  ])('fills the screen exactly at %ix%i, whichever panel is focused', (columns, rows) => {
    for (const focus of TABS) {
      const l = layout(columns, rows, focus);
      const stack = Object.values(l.heights).reduce((a, b) => a + b, 0);
      // On a short screen the last two strips are one row.
      expect(l.wide ? stack : stack + l.mainH - (l.shared ? 1 : 0)).toBe(rows);
      for (const h of Object.values(l.heights)) expect(h).toBeGreaterThanOrEqual(l.strips ? 1 : COLLAPSED);
      expect(l.leftW + (l.wide ? l.mainW : 0)).toBe(l.wide ? columns : columns);
    }
  });

  it('expands the focused list and collapses the others', () => {
    const l = layout(120, 32, 'keys');
    expect(l.heights.keys).toBeGreaterThan(10);
    expect(l.heights.upstreams).toBe(COLLAPSED);
    expect(l.heights.audit).toBe(COLLAPSED);
  });

  it('turns the inactive panels into one-line strips when stacked and short, giving the rows to the detail', () => {
    const short = layout(80, 22, 'keys');
    expect(short.strips).toBe(true);
    expect(short.heights.status).toBe(1);
    expect(short.heights.audit).toBe(1);
    expect(short.mainH).toBeGreaterThanOrEqual(9);
    expect(layout(80, 40, 'keys').strips).toBe(false);
    expect(layout(120, 22, 'keys').strips).toBe(false);
  });

  it('costs a short screen no row for the Requests strip until it or Audit is focused', () => {
    const without = layout(80, 22, 'keys', 3, { panels: ['status', 'keys', 'upstreams', 'audit'] });
    const withIt = layout(80, 22, 'keys', 3);
    expect(withIt.shared).toBe(true);
    expect(withIt.mainH).toBe(without.mainH);
    for (const focus of ['audit', 'requests'] as Tab[]) expect(layout(80, 22, focus, 3).shared).toBe(false);
    expect(layout(80, 40, 'keys', 3).shared).toBe(false);
    expect(layout(140, 40, 'keys', 3).shared).toBe(false);
  });

  it('leaves out a panel a server cannot fill, and stacks a wide screen when asked', () => {
    const four = layout(140, 40, 'keys', 3, { panels: ['status', 'keys', 'upstreams', 'audit'] });
    expect(four.heights.requests).toBe(0);
    const stacked = layout(140, 40, 'requests', 2, { stacked: true });
    expect(stacked).toMatchObject({ wide: false, strips: true, mainW: 140 });
    expect(stacked.mainH).toBeGreaterThanOrEqual(30);
  });

  it('keeps the panels narrow on a wide screen, so the main panel gets the width', () => {
    expect([100, 140, 150, 160, 200, 300].map((c) => layout(c, 40, 'keys').leftW)).toEqual([44, 44, 45, 48, 48, 48]);
    expect(layout(200, 40, 'keys').mainW).toBe(152);
  });

  it('gives each wide panel what it can use, two lines at least, and the rest to the log so both columns end together', () => {
    const l = layout(140, 40, 'upstreams', { status: 5, keys: 3, upstreams: 1, audit: 1 });
    expect([l.heights.status, l.heights.keys, l.heights.upstreams, l.heights.requests, l.heights.audit]).toEqual([7, 5, 3, 3, 3 + (40 - 7 - 5 - 3 - 3 - 3)]);
    // A focused list with more than the screen holds keeps the rest; the others keep their first rows.
    const busy = layout(140, 40, 'keys', { status: 5, keys: 100, upstreams: 1, audit: 100 });
    expect(busy.heights.status).toBe(5);
    expect(busy.heights.upstreams).toBe(3);
    expect(busy.heights.keys + busy.heights.audit + busy.heights.status + busy.heights.upstreams + busy.heights.requests).toBe(40);
  });

  it('never hides the rows of a problem: every wide panel keeps its first two', () => {
    const l = layout(100, 30, 'status', { status: 12, keys: 20, upstreams: 2, audit: 50 });
    expect(l.heights.upstreams).toBe(4);
    expect(l.heights.audit).toBeGreaterThanOrEqual(5);
    expect(l.heights.keys).toBeGreaterThanOrEqual(5);
  });

  it('puts the main panel beside the panels when wide and under them when narrow', () => {
    expect(layout(120, 32, 'keys').mainH).toBe(32);
    const narrow = layout(80, 22, 'keys');
    expect(narrow.wide).toBe(false);
    expect(narrow.mainW).toBe(80);
    expect(narrow.mainH).toBeGreaterThanOrEqual(7);
  });
});

describe('listArea', () => {
  it('gives a list the rows inside its border, less the filter row under Keys', () => {
    expect(listArea(10, { tab: 'upstreams' }).rows).toBe(8);
    expect(listArea(10, { tab: 'audit' }).rows).toBe(8);
    expect(listArea(10, { tab: 'keys', filterRow: false }).rows).toBe(8);
    expect(listArea(10, { tab: 'keys', filterRow: true }).rows).toBe(7);
    // A strip has no border.
    expect(listArea(1, { tab: 'keys', filterRow: false }).rows).toBe(1);
  });

  it('shares the Status panel between its summary, the list header and the list', () => {
    // 12 rows inside: three for the summary, one for the header, the rest for the list.
    expect(listArea(14, { tab: 'status', items: 20, noKeys: false })).toEqual({ header: 1, rows: 8 });
    // A short list takes only what it holds, and an empty one a single line.
    expect(listArea(14, { tab: 'status', items: 2, noKeys: false })).toEqual({ header: 1, rows: 2 });
    expect(listArea(14, { tab: 'status', items: 0, noKeys: false })).toEqual({ header: 0, rows: 1 });
    // Too short for the header, the list still keeps a line.
    expect(listArea(6, { tab: 'status', items: 20, noKeys: false })).toEqual({ header: 0, rows: 2 });
  });
});
