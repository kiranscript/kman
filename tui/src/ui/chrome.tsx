import { Box, Text } from 'ink';
import { useEffect, useState } from 'react';
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
      return { glyph: '○', word: link.lastOk ? 'offline' : 'cannot connect', color: color.warn };
    case 'denied':
      return { glyph: '✖', word: 'token refused', color: color.danger };
    default:
      return { glyph: '○', word: 'connecting' };
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
  /** Kept when space runs out; the others are dropped from the end first. */
  always?: boolean;
}

const HINT_GAP = 2;
const hintWidth = (h: HintItem) => [...h.k].length + 1 + [...h.label].length;

/** The hints that fit in `width`, dropping the last droppable one until they do. */
export function fitHints(hints: HintItem[], width: number): HintItem[] {
  const kept = [...hints];
  const total = () => kept.reduce((n, h) => n + hintWidth(h), 0) + HINT_GAP * Math.max(0, kept.length - 1);
  while (total() > width) {
    const i = kept.map((h) => !h.always).lastIndexOf(true);
    if (i < 0) break;
    kept.splice(i, 1);
  }
  return kept;
}

/**
 * The two rows under the panels, as in lazygit: a notice row, then the options bar
 * with the keys that work right now and, at the right, the connection.
 */
export function Footer({ notice, hints, link, width }: { notice?: Notice; hints: HintItem[]; link: Link; width: number }) {
  const badge = linkBadge(link);
  const tail = ` ${badge.glyph} ${badge.word} `;
  const shown = fitHints(hints, width - [...tail].length - 1);
  const tone = notice?.tone === 'ok' ? color.ok : notice?.tone === 'error' ? color.danger : undefined;
  return (
    <Box flexDirection="column" width={width}>
      <Text color={tone} wrap="truncate-end">
        {notice ? ` ${notice.tone === 'ok' ? '✔' : notice.tone === 'error' ? '✖' : '·'} ${notice.text}` : ' '}
      </Text>
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
