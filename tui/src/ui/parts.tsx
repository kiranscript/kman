import { Box, Text } from 'ink';
import type { ReactNode } from 'react';
import type { KeyStatus, SpendPeriod } from '../api.js';
import { bar, capMoney, money, padEnd } from '../format.js';
import { capRatio } from '../risk.js';
import type { Risk } from '../risk.js';
import { color, ratioColor, severityColor, severityGlyph, statusStyle } from './theme.js';

export function StatusBadge({ status, expired }: { status: KeyStatus; expired?: boolean }) {
  const s = statusStyle[status];
  const word = status === 'active' && expired ? 'expired' : status;
  return (
    <Text color={expired && status === 'active' ? color.danger : s.color} dimColor={s.dim}>
      {s.glyph} {word}
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
export function Meter({ period, width }: { period: SpendPeriod | undefined; width: number }) {
  const ratio = capRatio(period);
  if (ratio === undefined) {
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

/** A bordered box with its title set into the top edge. */
export function Panel({
  title,
  hint,
  width,
  height,
  children,
  tone,
}: {
  title?: string;
  /** Dim text at the right end of the title row, such as a scroll indicator. */
  hint?: string;
  width?: number | string;
  height?: number;
  children: ReactNode;
  tone?: string;
}) {
  return (
    <Box
      flexDirection="column"
      width={width}
      height={height}
      borderStyle="round"
      borderColor={tone}
      borderDimColor={tone === undefined}
      paddingX={1}
      overflow="hidden"
    >
      {title ? (
        <Box justifyContent="space-between">
          <Text bold color={tone} wrap="truncate-end">
            {title}
          </Text>
          {hint ? <Text dimColor>{hint}</Text> : null}
        </Box>
      ) : null}
      {/* Content keeps its own height and is clipped by the panel, never squashed. */}
      <Box flexDirection="column" flexShrink={0}>
        {children}
      </Box>
    </Box>
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
      <Text dimColor>{padEnd(label, labelWidth)}</Text>
      <Box flexShrink={1}>
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
export function scrollHint(start: number, shown: number, count: number): string {
  const above = start;
  const below = Math.max(0, count - start - shown);
  return [above > 0 ? `↑ ${above} more` : '', below > 0 ? `↓ ${below} more` : ''].filter(Boolean).join(' · ');
}
