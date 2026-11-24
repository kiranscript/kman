// Pure formatting: money, time, text widths and bars. No Ink, so it is cheap to
// test and every view formats the same way.

const MICRO = 1_000_000;

/** Microdollars as dollars: $0.036, $5.00, $1,250. */
export function money(micro: number): string {
  const dollars = micro / MICRO;
  if (micro === 0) return '$0';
  // Under a tenth of a cent there is nothing to show but that it is not nothing.
  if (dollars < 0.001) return '<$0.001';
  if (dollars < 1) return `$${dollars.toFixed(3)}`;
  if (dollars < 1000) return `$${dollars.toFixed(2)}`;
  return `$${Math.round(dollars).toLocaleString('en-US')}`;
}

/** A cap in dollars, one way everywhere: $5 when whole, else at least two decimals: $0.50, $0.36, $1.50, $0.125. */
export function capMoney(micro: number): string {
  const dollars = micro / MICRO;
  if (Number.isInteger(dollars)) return `$${dollars.toLocaleString('en-US')}`;
  return `$${dollars.toFixed(3).replace(/0$/, '')}`;
}

/** Dollars typed by an operator ("5", "0.25") as microdollars, or undefined if not a positive amount. */
export function parseDollars(text: string): number | undefined {
  const t = text.trim().replace(/^\$/, '');
  if (!/^(\d+(\.\d{1,6})?|\.\d{1,6})$/.test(t)) return undefined;
  const micro = Math.round(Number(t) * MICRO);
  return micro > 0 ? micro : undefined;
}

/** Whole days typed by an operator, or undefined if not a positive whole number. */
export function parseDays(text: string): number | undefined {
  const t = text.trim().replace(/d$/i, '');
  if (!/^\d+$/.test(t)) return undefined;
  const n = Number(t);
  return n > 0 ? n : undefined;
}

const MIN = 60_000;
const HOUR = 60 * MIN;
const DAY = 24 * HOUR;

/** How long ago: just now, 5m ago, 3h ago, 2d ago. */
export function ago(iso: string, now: number): string {
  const diff = Math.max(0, now - Date.parse(iso));
  if (diff < 45_000) return 'just now';
  if (diff < HOUR) return `${Math.max(1, Math.round(diff / MIN))}m ago`;
  if (diff < DAY) return `${Math.round(diff / HOUR)}h ago`;
  return `${Math.round(diff / DAY)}d ago`;
}

/** How long since a moment, to the second for the first minute: just now, 12s ago, 3m ago. */
export function since(then: number, now: number): string {
  const secs = Math.max(0, Math.round((now - then) / 1000));
  if (secs < 5) return 'just now';
  if (secs < 60) return `${secs}s ago`;
  return ago(new Date(then).toISOString(), now);
}

/** How long until: in 3d, in 5h, in 40m, or expired. */
export function until(iso: string, now: number): string {
  const diff = Date.parse(iso) - now;
  if (diff <= 0) return 'expired';
  if (diff < HOUR) return `in ${Math.max(1, Math.round(diff / MIN))}m`;
  if (diff < DAY) return `in ${Math.round(diff / HOUR)}h`;
  // Rounded up: a key made for 15 days is "in 15d", not "in 14d" a millisecond later.
  return `in ${Math.ceil(diff / DAY)}d`;
}

/** The days left, rounded down; negative once past. */
export function daysLeft(iso: string, now: number): number {
  return Math.floor((Date.parse(iso) - now) / DAY);
}

/** The days left, rounded up: what a person means by "3 days left". */
export function daysCeil(iso: string, now: number): number {
  return Math.ceil((Date.parse(iso) - now) / DAY);
}

/** 2026-11-17 12:13, in the operator's local time. */
export function dateTime(iso: string): string {
  const d = new Date(iso);
  const p = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

/** 2026-11-17 */
export function dateOnly(iso: string): string {
  return dateTime(iso).slice(0, 10);
}

/** 12:13:07, or the date alone for something from another day. */
export function clock(iso: string, now: number): string {
  const d = new Date(iso);
  const p = (n: number) => String(n).padStart(2, '0');
  const same = new Date(now).toDateString() === d.toDateString();
  return same
    ? `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
    : `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

/** Fit text to a width, ending in an ellipsis when cut. */
export function fit(text: string, width: number): string {
  if (width <= 0) return '';
  if (textWidth(text) <= width) return text;
  if (width === 1) return '…';
  return takeCells([...text], width - 1) + '…';
}

/** The terminal cells a string takes: a CJK or emoji character is two. */
export function textWidth(text: string): number {
  let n = 0;
  for (const ch of text) n += cellWidth(ch);
  return n;
}

/** The longest start of `chars` that fits in `cells`. */
function takeCells(chars: string[], cells: number): string {
  let used = 0;
  let out = '';
  for (const ch of chars) {
    const w = cellWidth(ch);
    if (used + w > cells) break;
    out += ch;
    used += w;
  }
  return out;
}

/**
 * Text wrapped on words into lines of at most `width`, with no leading spaces, in at most
 * `maxLines`. When more would be needed, the last line ends in "…".
 */
export function wrapLines(text: string, width: number, maxLines: number): string[] {
  if (width <= 0 || maxLines <= 0) return [];
  const lines: string[] = [];
  let cur = '';
  for (const word of text.split(/\s+/).filter(Boolean)) {
    let rest = [...word];
    while (rest.length > width) {
      if (cur) lines.push(cur);
      cur = '';
      lines.push(rest.slice(0, width).join(''));
      rest = rest.slice(width);
    }
    const w = rest.join('');
    if (!w) continue;
    if (!cur) cur = w;
    else if ([...cur].length + 1 + rest.length <= width) cur += ` ${w}`;
    else {
      lines.push(cur);
      cur = w;
    }
  }
  if (cur) lines.push(cur);
  if (lines.length <= maxLines) return lines;
  const kept = lines.slice(0, maxLines);
  const chars = [...kept[maxLines - 1]!];
  kept[maxLines - 1] = (chars.length >= width ? chars.slice(0, width - 1).join('') : chars.join('')) + '…';
  return kept;
}

/**
 * Fit text to a width by cutting the middle, so both ends stay: names that share a
 * prefix ("billing-prod-eu", "billing-prod-us") still differ in what is left.
 */
export function fitMiddle(text: string, width: number): string {
  if (width <= 0) return '';
  if (textWidth(text) <= width) return text;
  if (width <= 2) return fit(text, width);
  const chars = [...text];
  const head = Math.ceil((width - 1) * 0.55);
  const tail = width - 1 - head;
  return takeCells(chars, head) + '…' + takeCells([...chars].reverse(), tail).split('').reverse().join('');
}

export function padEnd(text: string, width: number): string {
  const t = fit(text, width);
  return t + ' '.repeat(Math.max(0, width - textWidth(t)));
}

export function padStart(text: string, width: number): string {
  const t = fit(text, width);
  return ' '.repeat(Math.max(0, width - textWidth(t))) + t;
}

/**
 * Items laid out in as many lines of `width` as it takes, up to `maxLines`. When some do not
 * fit, the last line ends in "+N more" (dropping items from it until that fits).
 */
export function packLines(items: string[], width: number, maxLines: number, sep = '   '): string[] {
  const lines: string[][] = [];
  let used = 0;
  for (const raw of items) {
    const item = fit(raw, width);
    const len = [...item].length;
    const last = lines[lines.length - 1];
    if (last && used + sep.length + len <= width) {
      last.push(item);
      used += sep.length + len;
    } else {
      lines.push([item]);
      used = len;
    }
  }
  if (lines.length <= maxLines) return lines.map((l) => l.join(sep));
  const kept = lines.slice(0, Math.max(1, maxLines));
  let hidden = lines.slice(kept.length).reduce((n, l) => n + l.length, 0);
  const tail = kept[kept.length - 1]!;
  const more = () => ` +${hidden} more`;
  while (tail.length > 1 && [...tail.join(sep)].length + more().length > width) {
    tail.pop();
    hidden++;
  }
  return kept.map((l, i) => l.join(sep) + (i === kept.length - 1 ? more() : ''));
}

/** A horizontal bar for a ratio of 0..1: filled and empty cells. */
export function bar(ratio: number, width: number): { filled: string; empty: string } {
  const r = Number.isFinite(ratio) ? Math.min(1, Math.max(0, ratio)) : 0;
  // Any spend at all shows at least one cell, so a small amount is not invisible.
  const cells = r > 0 ? Math.max(1, Math.round(r * width)) : 0;
  return { filled: '█'.repeat(cells), empty: '░'.repeat(width - cells) };
}

/** A count that fits a narrow card: 424, 1.2k, 35k, 1.2M. */
export function shortCount(n: number): string {
  if (n < 1000) return String(n);
  if (n < 10_000) return `${(n / 1000).toFixed(1).replace(/\.0$/, '')}k`;
  if (n < 1_000_000) return `${Math.round(n / 1000)}k`;
  return `${(n / 1_000_000).toFixed(1).replace(/\.0$/, '')}M`;
}

/** "424 requests", or "12k reqs" once the count would not fit a card. */
export function requestsText(n: number): string {
  return n < 1000 ? plural(n, 'request') : `${shortCount(n)} reqs`;
}

export function plural(n: number, one: string, many = `${one}s`): string {
  return `${n} ${n === 1 ? one : many}`;
}

/** How many terminal cells a character takes: 2 for the wide East Asian and emoji ranges, else 1. */
export function cellWidth(ch: string): number {
  const cp = ch.codePointAt(0) ?? 0;
  const wide =
    (cp >= 0x1100 && cp <= 0x115f) ||
    (cp >= 0x2e80 && cp <= 0xa4cf) ||
    (cp >= 0xac00 && cp <= 0xd7a3) ||
    (cp >= 0xf900 && cp <= 0xfaff) ||
    (cp >= 0xfe30 && cp <= 0xfe6f) ||
    (cp >= 0xff00 && cp <= 0xff60) ||
    (cp >= 0xffe0 && cp <= 0xffe6) ||
    (cp >= 0x1f000 && cp <= 0x1faff) ||
    (cp >= 0x20000 && cp <= 0x3fffd);
  return wide ? 2 : 1;
}

/** The end of `text` that fits in `max` cells, with a leading ellipsis when the start was cut. */
export function tailCells(text: string, max: number): string {
  const chars = [...text];
  const total = chars.reduce((n, c) => n + cellWidth(c), 0);
  if (total <= max) return text;
  let used = 1; // the ellipsis
  const kept: string[] = [];
  for (let i = chars.length - 1; i >= 0; i--) {
    const w = cellWidth(chars[i]!);
    if (used + w > max) break;
    kept.unshift(chars[i]!);
    used += w;
  }
  return '…' + kept.join('');
}
