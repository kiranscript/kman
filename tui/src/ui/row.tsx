// One line of a list. The selected line of the focused panel is a solid blue bar
// across the whole width (inverse without colour); in an unfocused panel the
// selected line is only bold, as in lazygit.
import { Text } from 'ink';
import { color } from './theme.js';

export interface Seg {
  t: string;
  color?: string;
  dim?: boolean;
  bold?: boolean;
}

const len = (segs: Seg[]) => segs.reduce((n, s) => n + [...s.t].length, 0);

/** The segments cut to `max` characters, ending in an ellipsis when cut. */
function clip(segs: Seg[], max: number): Seg[] {
  if (len(segs) <= max) return segs;
  const out: Seg[] = [];
  let left = Math.max(0, max - 1);
  for (const s of segs) {
    const chars = [...s.t];
    if (chars.length >= left) {
      out.push({ ...s, t: chars.slice(0, left).join('') + '…' });
      return out;
    }
    out.push(s);
    left -= chars.length;
  }
  return out;
}

export function Row({
  left,
  right = [],
  width,
  selected,
  focused,
}: {
  left: Seg[];
  right?: Seg[];
  width: number;
  selected?: boolean;
  focused?: boolean;
}) {
  // The right side goes first when there is no room for both.
  const rightW = len(right);
  const keepRight = width - rightW >= Math.min(len(left), 8);
  const r = keepRight ? right : [];
  const l = clip(left, Math.max(0, width - len(r) - (r.length ? 1 : 0)));
  const gap = Math.max(0, width - len(l) - len(r));
  const on = Boolean(selected && focused);
  const part = (s: Seg, i: number) => (
    <Text key={i} color={s.color} dimColor={s.dim && !on} bold={s.bold}>
      {s.t}
    </Text>
  );
  return (
    <Text backgroundColor={on ? color.selected : undefined} inverse={on && color.selected === undefined} bold={selected}>
      {l.map(part)}
      {' '.repeat(gap)}
      {r.map(part)}
    </Text>
  );
}
