import { describe, expect, it } from 'vitest';
import { COLLAPSED, layout } from '../src/layout.js';
import type { Tab } from '../src/ui/chrome.js';

const TABS: Tab[] = ['status', 'keys', 'upstreams', 'audit'];

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
      expect(l.wide ? stack : stack + l.mainH).toBe(rows);
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

  it('gives the panels about two fifths of a wide screen', () => {
    expect(layout(100, 30, 'keys').leftW).toBe(42);
    expect(layout(140, 40, 'keys').leftW).toBe(56);
    expect(layout(200, 40, 'keys').leftW).toBe(56);
  });

  it('gives each wide panel what it can use, two lines at least, and the rest to the log so both columns end together', () => {
    const l = layout(140, 40, 'upstreams', { status: 5, keys: 3, upstreams: 1, audit: 1 });
    expect([l.heights.status, l.heights.keys, l.heights.upstreams, l.heights.audit]).toEqual([7, 5, 3, 3 + (40 - 7 - 5 - 3 - 3)]);
    // A focused list with more than the screen holds keeps the rest; the others keep their first rows.
    const busy = layout(140, 40, 'keys', { status: 5, keys: 100, upstreams: 1, audit: 100 });
    expect(busy.heights.status).toBe(5);
    expect(busy.heights.upstreams).toBe(3);
    expect(busy.heights.keys + busy.heights.audit + busy.heights.status + busy.heights.upstreams).toBe(40);
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
