// Named colours, so a view says what a colour means, not which one it is. They
// are the 16 ANSI colours, which take the operator's own terminal theme. With
// NO_COLOR set (https://no-color.org) every colour here is undefined, but bold, dim
// and inverse stay, so the active panel and the selected line are still visible.
// Nothing relies on colour alone: a status always has a glyph and a word.
// A mark is one column in every terminal, so none is a character that can be drawn
// as an emoji (U+2716 and U+2714 are, over the next cell, in Windows Terminal).
import type { Severity } from '../risk.js';
import type { KeyStatus } from '../api.js';

export const noColor = Boolean(process.env.NO_COLOR);
const c = <T extends string>(name: T): T | undefined => (noColor ? undefined : name);

export const color = {
  accent: c('cyan'),
  ok: c('green'),
  warn: c('yellow'),
  danger: c('red'),
  /** The key in a keybinding hint. */
  key: c('blueBright'),
  /** The background of the selected line: the only background the screen has. */
  selected: c('blue'),
};

export const severityColor: Record<Severity, string | undefined> = {
  danger: color.danger,
  warn: color.warn,
  info: undefined,
};

export const severityGlyph: Record<Severity, string> = {
  danger: '!',
  warn: '▲',
  info: '·',
};

/** All is well: said of a change that worked, and of there being nothing to look at. */
export const okGlyph = '✓';

export const statusStyle: Record<KeyStatus, { glyph: string; color: string | undefined; dim: boolean }> = {
  active: { glyph: '●', color: color.ok, dim: false },
  disabled: { glyph: '○', color: color.warn, dim: false },
  revoked: { glyph: '×', color: undefined, dim: true },
};

/** The colour of a spend bar by how full it is. */
export function ratioColor(ratio: number): string | undefined {
  if (ratio >= 1) return color.danger;
  if (ratio >= 0.8) return color.warn;
  return color.accent;
}
