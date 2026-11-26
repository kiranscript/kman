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
/** The rows a wide panel is sure of beside its border and one line: two lines of its list. */
const FLOOR = COLLAPSED + 2;
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

/** The lines Status's summary is sure of above its list: one when there are no keys to count. */
export const summaryRows = (noKeys: boolean) => (noKeys ? 1 : 3);

/** Whether Keys, when focused, gives its last row to the filter. */
export const hasFilterRow = (filter: string, filtering: boolean) => filtering || filter !== '';

/** What of a panel decides how its rows are shared. */
export type ListPanel =
  | { tab: 'status'; items: number; noKeys: boolean }
  | { tab: 'keys'; filterRow: boolean }
  | { tab: 'upstreams' | 'audit' };

/**
 * What a panel `height` rows high gives its list, when it is the focused one: the
 * `rows` of it on screen at once, and for Status the `header` line above them. The
 * panels draw by this and PgUp/PgDn move by it, so a page is what is shown (#117).
 */
export function listArea(height: number, panel: ListPanel): { header: number; rows: number } {
  const inside = height - 2; // less the border
  // A strip has no border: its one line is the list's.
  if (panel.tab === 'keys') return { header: 0, rows: height <= 1 ? 1 : Math.max(0, inside - (panel.filterRow ? 1 : 0)) };
  if (panel.tab !== 'status') return { header: 0, rows: Math.max(1, inside) };
  const rows = Math.max(1, inside);
  const reserve = summaryRows(panel.noKeys);
  // The label sits on the list only when there is room; otherwise the border says it
  // (`1 of 7 need attention`), and at least one selectable line is always kept.
  const roomy = rows >= reserve + 2;
  const header = panel.items > 0 && roomy ? 1 : 0;
  // The list is sized to what it holds, an empty one being a single line, and the
  // summary gets everything else, down to a few lines.
  const needed = header + Math.max(1, panel.items);
  const area = Math.min(needed, Math.max(header + 1, roomy ? rows - reserve : rows - 2));
  return { header, rows: area - header };
}

/**
 * `want` is how many lines each panel could use (a number is the focused panel's): a
 * panel gets no more than that, plus its border, so a short list does not leave blank
 * rows and the rest goes to the panels that can use it, as in lazygit, where every
 * panel keeps a share of the column.
 */
export function layout(columns: number, rows: number, focus: Tab, want: number | Partial<Record<Tab, number>> = Infinity): Layout {
  const wide = columns >= WIDE;
  // About two fifths of the screen (44 cells at 100 columns), so names and targets keep room beside the detail.
  const leftW = wide ? Math.min(56, Math.max(44, Math.floor(columns * 0.42))) : columns;
  const strips = !wide && rows < STRIP_ROWS;
  const small = strips ? 1 : COLLAPSED;
  const heights: Record<Tab, number> = { status: small, keys: small, upstreams: small, audit: small };
  const wants: Partial<Record<Tab, number>> = typeof want === 'number' ? { [focus]: want } : want;
  // A panel nobody said anything about stays one line, unless it is the focused one.
  const need = (t: Tab) => (wants[t] === undefined ? (t === focus ? Infinity : COLLAPSED) : Math.max(COLLAPSED, wants[t]! + 2));

  if (wide) {
    // First every panel gets what shows its first rows (up to FLOOR), so a problem is never
    // hidden; then the focused one gets what it can use, then Keys and the rest.
    let spare = rows - COLLAPSED * PANELS.length;
    const give = (t: Tab, upTo: number) => {
      const n = Math.max(0, Math.min(spare, upTo - heights[t]));
      heights[t] += n;
      spare -= n;
    };
    const order = [focus, ...(['keys', 'status', 'upstreams', 'audit'] as Tab[]).filter((t) => t !== focus)];
    for (const t of order) give(t, Math.min(FLOOR, need(t)));
    for (const t of order) give(t, need(t));
    // What nothing wants goes to the last panel (the log), so both columns end on the same row;
    // a focused panel with no limit takes it instead.
    heights[Number.isFinite(need(focus)) ? 'audit' : focus] += spare;
    return { wide, strips, leftW, mainW: columns - leftW, mainH: rows, heights };
  }

  const rest = rows - small * 3;
  const w = wants[focus] ?? Infinity;
  const own = Math.max(COLLAPSED, Math.min(Number.isFinite(w) ? w + 2 : rest, Math.max(5, Math.floor(rest / 2)), rest - MAIN_MIN));
  heights[focus] = own;
  return { wide, strips, leftW, mainW: columns, mainH: rest - own, heights };
}
