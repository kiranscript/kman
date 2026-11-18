// Where each panel goes, as plain numbers, so it can be tested without drawing.
//
// Wide (100 columns or more): a column of panels on the left, about a third of the
// screen, and the main panel on the right for the full height. Narrow: the same
// panels stacked across the full width with the main panel under the focused one.
// In both, the focused panel is expanded and the others shrink to one line, which is
// how lazygit keeps five lists on one screen.
import type { Tab } from './ui/chrome.js';

export const WIDE = 100;
/** A panel's border plus one line: the least that shows where its selection is. */
export const COLLAPSED = 3;
/** The least the main panel needs under the focused panel: a compact key detail. */
export const MAIN_MIN = 7;

export interface Layout {
  wide: boolean;
  leftW: number;
  mainW: number;
  mainH: number;
  heights: Record<Tab, number>;
}

const PANELS: Tab[] = ['status', 'keys', 'upstreams', 'audit'];

/**
 * `want` is how many lines the focused panel could use: when it has few, the rest
 * goes to the main panel instead of being left empty.
 */
export function layout(columns: number, rows: number, focus: Tab, want = Infinity): Layout {
  const wide = columns >= WIDE;
  const leftW = wide ? Math.min(52, Math.max(38, Math.floor(columns * 0.36))) : columns;
  const heights: Record<Tab, number> = { status: COLLAPSED, keys: COLLAPSED, upstreams: COLLAPSED, audit: COLLAPSED };

  if (wide) {
    const others = PANELS.reduce((n, t) => n + (t === focus ? 0 : heights[t]), 0);
    heights[focus] = rows - others;
    return { wide, leftW, mainW: columns - leftW, mainH: rows, heights };
  }

  const rest = rows - COLLAPSED * 3;
  const own = Math.max(COLLAPSED, Math.min(Number.isFinite(want) ? want + 2 : rest, Math.max(5, Math.floor(rest / 2)), rest - MAIN_MIN));
  heights[focus] = own;
  return { wide, leftW, mainW: columns, mainH: rest - own, heights };
}
