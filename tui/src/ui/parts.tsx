import { Box, Text } from 'ink';
import type { ReactNode } from 'react';
import type { KeyStatus, SpendPeriod } from '../api.js';
import { bar, capMoney, money, padEnd, wrapLines } from '../format.js';
import { capRatio } from '../risk.js';
import type { Risk } from '../risk.js';
import { Frame } from './frame.js';
import { color, ratioColor, severityColor, severityGlyph, statusStyle } from './theme.js';

export function StatusBadge({ status, expired }: { status: KeyStatus; expired?: boolean }) {
  const s = statusStyle[status];
  const lapsed = status === 'active' && expired;
  return (
    <Text color={lapsed ? color.danger : s.color} dimColor={s.dim}>
      {lapsed ? '✖' : s.glyph} {lapsed ? 'expired' : status}
    </Text>
  );
}

export function RiskLine({ risk }: { risk: Risk }) {
  return (
    <Text color={severityColor[risk.severity]} dimColor={risk.severity === 'info'}>
      {severityGlyph[risk.severity]} {risk.label}
    </Text>
  );
}

/** A spend meter: the bar when there is a cap, dots when there is none. */
export function Meter({ period, width, muted }: { period: SpendPeriod | undefined; width: number; muted?: boolean }) {
  const ratio = capRatio(period);
  // A revoked key's spend is history, not a meter: dots, as in the list.
  if (ratio === undefined || muted) {
    return <Text dimColor>{'·'.repeat(width)}</Text>;
  }
  const { filled, empty } = bar(ratio, width);
  return (
    <Text>
      <Text color={ratioColor(ratio)}>{filled}</Text>
      <Text dimColor>{empty}</Text>
    </Text>
  );
}

/** "$0.108 / $5", or "$0.108" with no cap. */
export function spendText(p: SpendPeriod | undefined): string {
  if (!p) return '—';
  return p.cap_micro ? `${money(p.cost_micro)} / ${capMoney(p.cap_micro)}` : money(p.cost_micro);
}

/** A bordered box with its title set into the top edge, as the panels have theirs. */
export function Panel({
  title,
  hint,
  width,
  children,
  tone,
}: {
  title?: string;
  /** Dim text at the right end of the bottom border, such as a scroll indicator. */
  hint?: string;
  width: number;
  children: ReactNode;
  tone?: string;
}) {
  return (
    <Frame title={title} position={hint} width={width} tone={tone} focused={tone === undefined} heavy={false}>
      {/* Content keeps its own height and is clipped by the panel, never squashed. */}
      <Box flexDirection="column" flexShrink={0} paddingX={1}>
        {children}
      </Box>
    </Frame>
  );
}

/** Text wrapped on words into at most `maxLines` lines, one Text each, so no line starts with a space. */
export function Wrapped({
  text,
  width,
  maxLines,
  tone,
  dim,
  marker = '',
}: {
  text: string;
  width: number;
  maxLines: number;
  tone?: string;
  dim?: boolean;
  /** Starts the first line ("✖ "); the lines after it are indented under the text. */
  marker?: string;
}) {
  const hang = [...marker].length;
  return (
    <>
      {wrapLines(text, width - hang, maxLines).map((line, i) => (
        <Text key={i} color={tone} dimColor={dim}>
          {i === 0 ? marker : ' '.repeat(hang)}
          {line}
        </Text>
      ))}
    </>
  );
}

export function SectionTitle({ children }: { children: string }) {
  return (
    <Text dimColor bold>
      {children}
    </Text>
  );
}

/** A label and its value on one line, labels aligned. */
export function Field({ label, children, labelWidth = 12 }: { label: string; children: ReactNode; labelWidth?: number }) {
  return (
    <Box>
      <Box width={labelWidth} flexShrink={0}>
        <Text dimColor>{label}</Text>
      </Box>
      <Box flexGrow={1} flexShrink={1} minWidth={0}>
        <Text wrap="wrap">{children}</Text>
      </Box>
    </Box>
  );
}

/** A key hint: the key bright, its meaning dim. */
export function Hint({ k, children }: { k: string; children: string }) {
  return (
    <Text>
      <Text bold color={color.key}>
        {k}
      </Text>
      <Text dimColor> {children}</Text>
    </Text>
  );
}

/** The start of a window of `height` rows that keeps `selected` in view. */
export function windowStart(selected: number, count: number, height: number): number {
  if (count <= height) return 0;
  return Math.max(0, Math.min(count - height, selected - Math.floor(height / 2)));
}

/** "↑ 2 more · ↓ 3 more" for rows scrolled out of a window, or '' when all are in view. */
export function scrollHint(start: number, shown: number, count: number, short = false): string {
  const above = start;
  const below = Math.max(0, count - start - shown);
  const word = short ? '' : ' more';
  return [above > 0 ? `↑${short ? '' : ' '}${above}${word}` : '', below > 0 ? `↓${short ? '' : ' '}${below}${word}` : ''].filter(Boolean).join(' · ');
}

/** The scroll hint in full when it fits in `room` columns, else in its short form ("↑8 · ↓79"). */
export function fittingScrollHint(start: number, shown: number, count: number, room: number): string {
  const full = scrollHint(start, shown, count);
  return [...full].length <= room ? full : scrollHint(start, shown, count, true);
}

/** Setup instructions shared by the empty install, the upstream panel and the key form. */
export function UpstreamSetup() {
  return (
    <Box flexDirection="column">
      <Text bold>kman upstream add openai --preset openai</Text>
      <Text dimColor>Custom endpoint? Set --host and --port. For plain HTTP add --no-tls.</Text>
    </Box>
  );
}
