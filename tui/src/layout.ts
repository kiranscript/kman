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
 * `want` is how many lines each panel could use (a number is the focused panel's): a
 * panel gets no more than that, plus its border, so a short list does not leave blank
 * rows and the rest goes to the panels that can use it, as in lazygit, where every
 * panel keeps a share of the column.
 */
export function layout(columns: number, rows: number, focus: Tab, want: number | Partial<Record<Tab, number>> = Infinity): Layout {
  const wide = columns >= WIDE;
  // About two fifths of the screen, so names and targets keep room beside the detail.
  const leftW = wide ? Math.min(56, Math.max(42, Math.floor(columns * 0.4))) : columns;
  const strips = !wide && rows < STRIP_ROWS;
  const small = strips ? 1 : COLLAPSED;
  const heights: Record<Tab, number> = { status: small, keys: small, upstreams: small, audit: small };
  const wants: Partial<Record<Tab, number>> = typeof want === 'number' ? { [focus]: want } : want;
  const need = (t: Tab) => Math.max(COLLAPSED, (wants[t] ?? Infinity) + 2);

  if (wide) {
    // Extra rows go to the focused panel first, then Keys, then the rest, each up to what it can use.
    let spare = rows - COLLAPSED * PANELS.length;
    const order = [focus, ...(['keys', 'status', 'upstreams', 'audit'] as Tab[]).filter((t) => t !== focus)];
    for (const t of order) {
      const give = Math.max(0, Math.min(spare, need(t) - COLLAPSED));
      heights[t] += give;
      spare -= give;
    }
    // Whatever nothing wants goes to the focused panel, so the column is always full.
    heights[focus] += spare;
    return { wide, strips, leftW, mainW: columns - leftW, mainH: rows, heights };
  }

  const rest = rows - small * 3;
  const w = wants[focus] ?? Infinity;
  const own = Math.max(COLLAPSED, Math.min(Number.isFinite(w) ? w + 2 : rest, Math.max(5, Math.floor(rest / 2)), rest - MAIN_MIN));
  heights[focus] = own;
  return { wide, strips, leftW, mainW: columns, mainH: rest - own, heights };
}
