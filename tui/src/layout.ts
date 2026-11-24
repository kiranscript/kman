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

/** Under this many rows, stacked panels other than the focused one shrink to one bare line each. */
export const STRIP_ROWS = 28;

export interface Layout {
  wide: boolean;
  /** Inactive panels are one borderless line each, to leave the rows to the lists and the detail. */
  strips: boolean;
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
  // About two fifths of the screen, so names and targets keep room beside the detail.
  const leftW = wide ? Math.min(56, Math.max(42, Math.floor(columns * 0.4))) : columns;
  const strips = !wide && rows < STRIP_ROWS;
  const small = strips ? 1 : COLLAPSED;
  const heights: Record<Tab, number> = { status: small, keys: small, upstreams: small, audit: small };

  if (wide) {
    const others = PANELS.reduce((n, t) => n + (t === focus ? 0 : heights[t]), 0);
    heights[focus] = rows - others;
    return { wide, strips, leftW, mainW: columns - leftW, mainH: rows, heights };
  }

  const rest = rows - small * 3;
  const own = Math.max(COLLAPSED, Math.min(Number.isFinite(want) ? want + 2 : rest, Math.max(5, Math.floor(rest / 2)), rest - MAIN_MIN));
  heights[focus] = own;
  return { wide, strips, leftW, mainW: columns, mainH: rest - own, heights };
}
