import { Box, Text } from 'ink';
import { useEffect, useState } from 'react';
import { fit, since, wrapLines } from '../format.js';
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
        glyph: '◌',
        text: link.lastOk ? `connection lost · last data ${since(link.lastOk, Date.now())}` : 'cannot connect',
        color: color.warn,
      };
      break;
    case 'denied':
      status = { glyph: '✖', text: 'token refused', color: color.danger };
      break;
    default:
      status = { glyph: '◌', text: 'connecting…' };
  }
  // The status comes first and keeps its words; the address gives way, leaving at least
  // two columns between them: first the actor, then the end of the address.
  const used = [...`kman admin  ${status.glyph} ${status.text}`].length;
  const room = width - used - 2;
  const full = `${actor ? `${actor} @ ` : ''}${url}`;
  const where = [...full].length <= room ? full : room >= 12 ? fit(url, room) : '';
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
      {where ? <Text dimColor>{where}</Text> : null}
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

const noticeMark = (n: Notice) => `${n.tone === 'ok' ? '✔' : n.tone === 'error' ? '✖' : '·'} `;
const NOTICE_LINES = 3;

/** The lines of a notice: one for most, and up to three, hanging under the mark, for a failure. */
function noticeLines(n: Notice, width: number): string[] {
  const mark = noticeMark(n);
  if (n.tone !== 'error') return [fit(mark + n.text, width)];
  return wrapLines(n.text, width - mark.length, NOTICE_LINES).map((l, i) => (i === 0 ? mark : ' '.repeat(mark.length)) + l);
}

/**
 * Rows a notice takes. A failure keeps the reason on screen until a key is pressed, so
 * it gets a second row rather than being cut; everything else is one row.
 */
export function noticeRows(notice: Notice | undefined, width: number): number {
  if (!notice || notice.tone !== 'error') return 1;
  return Math.max(1, noticeLines(notice, width).length);
}

export function Footer({ notice, hints, width }: { notice?: Notice; hints: HintItem[]; width: number }) {
  const shown = fitHints(hints, width);
  const tone = notice?.tone === 'ok' ? color.ok : notice?.tone === 'error' ? color.danger : undefined;
  const shownLines = notice ? noticeLines(notice, width) : [' '];
  return (
    <Box flexDirection="column" width={width}>
      {shownLines.map((line, i) => (
        <Text key={i} color={tone} wrap="truncate-end">
          {line}
        </Text>
      ))}
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
