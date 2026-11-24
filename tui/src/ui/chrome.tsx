import { Box, Text } from 'ink';
import { useEffect, useState } from 'react';
import { fit, wrapLines } from '../format.js';
import { Hint } from './parts.js';
import { color } from './theme.js';
import type { Link } from '../useAdmin.js';

/** The panels on the left, in order. The number is the key that jumps to each. */
export type Tab = 'status' | 'keys' | 'upstreams' | 'audit';
export const TABS: { id: Tab; label: string }[] = [
  { id: 'status', label: 'Status' },
  { id: 'keys', label: 'Keys' },
  { id: 'upstreams', label: 'Upstreams' },
  { id: 'audit', label: 'Audit' },
];

/** "live", "offline", "token refused": the connection in one word, with a glyph. */
export function linkBadge(link: Link): { glyph: string; word: string; color?: string } {
  switch (link.state) {
    case 'ok':
      return { glyph: '●', word: 'live', color: color.ok };
    case 'down':
      // The server answered, so it is there: say what kind of answer, not "offline".
      if (link.answered) {
        const st = link.status ?? 500;
        const word = st < 400 ? 'invalid reply' : st >= 500 ? 'server error' : st === 429 ? 'rate limited' : 'refused';
        return { glyph: '✖', word, color: color.danger };
      }
      return { glyph: '◌', word: link.lastOk ? 'offline' : 'cannot connect', color: color.warn };
    case 'denied':
      return { glyph: '✖', word: 'token refused', color: color.danger };
    default:
      return { glyph: '◌', word: 'connecting' };
  }
}

/** Re-renders every `ms`, so "updated 3s ago" keeps counting. */
export function useTick(ms: number): number {
  const [n, setN] = useState(0);
  useEffect(() => {
    const t = setInterval(() => setN((x) => x + 1), ms);
    return () => clearInterval(t);
  }, [ms]);
  return n;
}

export interface Notice {
  text: string;
  tone: 'ok' | 'error' | 'info';
}

export interface HintItem {
  k: string;
  label: string;
  /** Kept when space runs out; the others are dropped lowest rank first, the later one of equal rank first. */
  always?: boolean;
  /** How much it is missed (default 3): hints with a low rank go first when space runs out. */
  rank?: number;
}

const HINT_GAP = 2;
const hintWidth = (h: HintItem) => [...h.k].length + 1 + [...h.label].length;

/** The hints that fit in `width`, dropping the least missed one (the last of them) until they do. */
export function fitHints(hints: HintItem[], width: number): HintItem[] {
  const kept = [...hints];
  const total = () => kept.reduce((n, h) => n + hintWidth(h), 0) + HINT_GAP * Math.max(0, kept.length - 1);
  while (total() > width) {
    let drop = -1;
    kept.forEach((h, i) => {
      if (h.always) return;
      if (drop < 0 || (h.rank ?? 3) <= (kept[drop]!.rank ?? 3)) drop = i;
    });
    if (drop < 0) break;
    kept.splice(drop, 1);
  }
  return kept;
}

const noticeMark = (n: Notice) => `${n.tone === 'ok' ? '✔' : n.tone === 'error' ? '✖' : '·'} `;
const NOTICE_LINES = 3;

/** The lines of a notice: one for most, and up to three, hanging under the mark, for a failure. */
function noticeLines(n: Notice, width: number): string[] {
  const mark = ` ${noticeMark(n)}`;
  if (n.tone !== 'error') return [fit(mark + n.text, width)];
  return wrapLines(n.text, width - mark.length, NOTICE_LINES).map((l, i) => (i === 0 ? mark : ' '.repeat(mark.length)) + l);
}

/**
 * Rows a notice takes. A failure keeps the reason on screen until a key is pressed, so
 * it gets more rows rather than being cut; everything else is one row.
 */
export function noticeRows(notice: Notice | undefined, width: number): number {
  if (!notice || notice.tone !== 'error') return 1;
  return Math.max(1, noticeLines(notice, width).length);
}

/**
 * The rows under the panels, as in lazygit: the notice, then the options bar with the
 * keys that work right now and, at the right, the connection.
 */
export function Footer({ notice, hints, link, width }: { notice?: Notice; hints: HintItem[]; link: Link; width: number }) {
  const badge = linkBadge(link);
  const tail = ` ${badge.glyph} ${badge.word} `;
  // Two spaces at least between the last hint and the connection.
  const shown = fitHints(hints, width - [...tail].length - 3);
  const tone = notice?.tone === 'ok' ? color.ok : notice?.tone === 'error' ? color.danger : undefined;
  const lines = notice ? noticeLines(notice, width) : [' '];
  return (
    <Box flexDirection="column" width={width}>
      {lines.map((line, i) => (
        <Text key={i} color={tone} wrap="truncate-end">
          {line}
        </Text>
      ))}
      <Box width={width} justifyContent="space-between">
        <Box paddingLeft={1}>
          {shown.map((h, i) => (
            <Box key={h.k + h.label} marginRight={i < shown.length - 1 ? HINT_GAP : 0} flexShrink={0}>
              <Hint k={h.k}>{h.label}</Hint>
            </Box>
          ))}
        </Box>
        <Text color={badge.color} dimColor={badge.color === undefined} wrap="truncate-end">
          {tail}
        </Text>
      </Box>
    </Box>
  );
}
