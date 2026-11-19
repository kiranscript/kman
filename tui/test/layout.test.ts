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
      for (const h of Object.values(l.heights)) expect(h).toBeGreaterThanOrEqual(COLLAPSED);
      expect(l.leftW + (l.wide ? l.mainW : 0)).toBe(l.wide ? columns : columns);
    }
  });

  it('expands the focused list and collapses the others', () => {
    const l = layout(120, 32, 'keys');
    expect(l.heights.keys).toBeGreaterThan(10);
    expect(l.heights.upstreams).toBe(COLLAPSED);
    expect(l.heights.audit).toBe(COLLAPSED);
  });

  it('puts the main panel beside the panels when wide and under them when narrow', () => {
    expect(layout(120, 32, 'keys').mainH).toBe(32);
    const narrow = layout(80, 22, 'keys');
    expect(narrow.wide).toBe(false);
    expect(narrow.mainW).toBe(80);
    expect(narrow.mainH).toBeGreaterThanOrEqual(7);
  });
});
