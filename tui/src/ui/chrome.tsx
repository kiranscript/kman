import { Box, Text } from 'ink';
import { useEffect, useState } from 'react';
import { fit, since } from '../format.js';
import type { Link } from '../useAdmin.js';
import { Hint } from './parts.js';
import { color } from './theme.js';

export type Tab = 'overview' | 'keys' | 'upstreams' | 'audit';
export const TABS: { id: Tab; label: string }[] = [
  { id: 'overview', label: 'Overview' },
  { id: 'keys', label: 'Keys' },
  { id: 'upstreams', label: 'Upstreams' },
  { id: 'audit', label: 'Audit' },
];

export function Header({ link, url, actor, width }: { link: Link; url: string; actor?: string; width: number }) {
  const tick = useTick(1000);
  void tick;
  let status: { glyph: string; text: string; color?: string };
  switch (link.state) {
    case 'ok':
      status = { glyph: '●', text: `live · updated ${since(link.at, Date.now())}`, color: color.ok };
      break;
    case 'down':
      status = {
        glyph: '○',
        text: link.lastOk ? `connection lost · last data ${since(link.lastOk, Date.now())}` : 'cannot connect',
        color: color.warn,
      };
      break;
    case 'denied':
      status = { glyph: '✖', text: 'token refused', color: color.danger };
      break;
    default:
      status = { glyph: '○', text: 'connecting…' };
  }
  const where = `${actor ? `${actor} @ ` : ''}${url}`;
  return (
    <Box width={width} justifyContent="space-between">
      <Text wrap="truncate-end">
        <Text bold color={color.accent}>
          kman
        </Text>
        <Text dimColor> admin  </Text>
        <Text color={status.color}>{status.glyph} </Text>
        <Text color={status.color} dimColor={status.color === undefined}>
          {status.text}
        </Text>
      </Text>
      <Text dimColor wrap="truncate-start">
        {fit(where, Math.max(10, Math.floor(width / 2) - 2))}
      </Text>
    </Box>
  );
}

function useTick(ms: number): number {
  const [n, setN] = useState(0);
  useEffect(() => {
    const t = setInterval(() => setN((x) => x + 1), ms);
    return () => clearInterval(t);
  }, [ms]);
  return n;
}

export function Tabs({
  active,
  counts,
  warnings,
  infos,
  width,
}: {
  active: Tab;
  counts: Partial<Record<Tab, number>>;
  /** Keys with a warning or worse. */
  warnings: number;
  /** Keys with only a note, such as no spend cap. */
  infos: number;
  width: number;
}) {
  return (
    <Box width={width} borderStyle="single" borderTop={false} borderLeft={false} borderRight={false} borderDimColor>
      {TABS.map((t, i) => {
        const on = t.id === active;
        const count = counts[t.id];
        return (
          <Box key={t.id} marginRight={2}>
            <Text inverse={on} bold={on} color={on ? color.accent : undefined}>
              {` ${i + 1} ${t.label}`}
              {count !== undefined ? ` ${count}` : ''}
              {t.id === 'overview' && warnings > 0 ? ` ▲${warnings}` : ''}
              {t.id === 'overview' && infos > 0 ? ` ○${infos}` : ''}{' '}
            </Text>
          </Box>
        );
      })}
    </Box>
  );
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

export function Footer({ notice, hints, width }: { notice?: Notice; hints: HintItem[]; width: number }) {
  const shown = fitHints(hints, width);
  const tone = notice?.tone === 'ok' ? color.ok : notice?.tone === 'error' ? color.danger : undefined;
  return (
    <Box flexDirection="column" width={width}>
      <Text color={tone} wrap="truncate-end">
        {notice ? `${notice.tone === 'ok' ? '✔' : notice.tone === 'error' ? '✖' : '·'} ${notice.text}` : ' '}
      </Text>
      <Box>
        {shown.map((h, i) => (
          <Box key={h.k + h.label} marginRight={i < shown.length - 1 ? HINT_GAP : 0} flexShrink={0}>
            <Hint k={h.k}>{h.label}</Hint>
          </Box>
        ))}
      </Box>
    </Box>
  );
}
