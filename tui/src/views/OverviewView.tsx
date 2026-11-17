import { Box, Text } from 'ink';
import type { AuditEntry, Key, PriceDataStatus, Upstream } from '../api.js';
import { clock, fit, money, padEnd, plural } from '../format.js';
import { keyLabel } from '../keys.js';
import type { Attention } from '../risk.js';
import { Panel, scrollHint, windowStart } from '../ui/parts.js';
import { color, severityColor, severityGlyph } from '../ui/theme.js';
import { actionColor, targetText } from './AuditView.js';
import type { Names } from './AuditView.js';

interface Props {
  keys: Key[];
  upstreams: Upstream[];
  audit: AuditEntry[];
  price?: PriceDataStatus;
  needs: Attention[];
  selected: number;
  width: number;
  height: number;
  now: number;
}

const CARD_HEIGHT = 5;
// "admin_token.create" is the longest action, 18.
const ACTIVITY_ACTION_W = 18;
/** The built-in price table counts as stale after this many days (SPEC). */
const STALE_DAYS = 60;

export function OverviewView({ keys, upstreams, audit, price, needs, selected, width, height, now }: Props) {
  const live = keys.filter((k) => k.status === 'active').length;
  const disabled = keys.filter((k) => k.status === 'disabled').length;
  const revoked = keys.filter((k) => k.status === 'revoked').length;
  const today = sum(keys, 'today');
  const month = sum(keys, 'month');

  // The keys card is wider because its second line is the longest.
  const unit = Math.floor(width / 4.8);
  const widths = [Math.floor(unit * 1.8), unit, unit, 0];
  widths[3] = width - widths[0]! - widths[1]! - widths[2]!;

  const wide = width >= 100;
  const panelsH = height - CARD_HEIGHT;
  const attentionH = wide ? panelsH : Math.min(panelsH - 6, Math.max(5, needs.length + 3));
  const activityH = wide ? panelsH : panelsH - attentionH;
  const attentionW = wide ? Math.floor((width - 1) * 0.45) : width;
  const activityW = wide ? width - attentionW - 1 : width;
  const worstIsUrgent = needs.some((n) => n.top !== 'info');
  const attnRows = Math.max(1, attentionH - 3);
  const attnSel = Math.min(selected, Math.max(0, needs.length - 1));
  const attnStart = windowStart(attnSel, needs.length, attnRows);
  const attnShown = needs.slice(attnStart, attnStart + attnRows);
  const actRows = Math.max(1, activityH - 3);
  const actShown = audit.slice(0, actRows);

  return (
    <Box flexDirection="column" width={width} height={height}>
      <Box height={CARD_HEIGHT}>
        <Card width={widths[0]!} label="KEYS" value={`${live} active`} sub={`${disabled} disabled · ${revoked} revoked`} />
        <Card width={widths[1]!} label="SPEND TODAY" value={money(today.cost)} sub={plural(today.requests, 'request')} />
        <Card width={widths[2]!} label="THIS MONTH" value={money(month.cost)} sub={plural(month.requests, 'request')} />
        <PriceCard width={widths[3]!} price={price} />
      </Box>
      <Box flexDirection={wide ? 'row' : 'column'} height={panelsH}>
        <Panel
          title={needs.length > 0 ? `Needs attention · ${needs.length}` : 'Needs attention'}
          hint={scrollHint(attnStart, attnShown.length, needs.length)}
          width={attentionW}
          height={attentionH}
          tone={worstIsUrgent ? color.warn : undefined}
        >
          <AttentionList items={attnShown} selectedId={needs[attnSel]?.key.id} width={attentionW - 4} hasKeys={live > 0} none={needs.length === 0} />
        </Panel>
        {wide ? <Box width={1} /> : null}
        <Panel title="Recent activity" hint={audit.length > actShown.length ? `${audit.length - actShown.length} older on Audit (4)` : ''} width={activityW} height={activityH}>
          <Activity audit={actShown} names={{ keys, upstreams }} width={activityW - 4} now={now} />
        </Panel>
      </Box>
    </Box>
  );
}

function sum(keys: Key[], which: 'today' | 'month') {
  return keys.reduce(
    (acc, k) => ({
      cost: acc.cost + (k.spend?.[which].cost_micro ?? 0),
      requests: acc.requests + (k.spend?.[which].requests ?? 0),
    }),
    { cost: 0, requests: 0 },
  );
}

function Card({
  width,
  label,
  value,
  sub,
  tone,
  last,
}: {
  width: number;
  label: string;
  value: string;
  sub: string;
  tone?: string;
  last?: boolean;
}) {
  return (
    <Box width={width} height={CARD_HEIGHT} paddingRight={last ? 0 : 1}>
      <Panel width={last ? width : width - 1} height={CARD_HEIGHT}>
        <Text dimColor bold>
          {label}
        </Text>
        <Text bold color={tone} wrap="truncate-end">
          {value}
        </Text>
        <Text dimColor wrap="truncate-end">
          {sub}
        </Text>
      </Panel>
    </Box>
  );
}

function PriceCard({ width, price }: { width: number; price?: PriceDataStatus }) {
  if (!price) return <Card last width={width} label="PRICE DATA" value="unknown" sub="age not readable" />;
  return (
    <Card
      last
      width={width}
      label="PRICE DATA"
      value={price.stale ? `▲ ${price.age_days} days old` : `${price.age_days} days old`}
      tone={price.stale ? color.warn : undefined}
      sub={price.stale ? 'stale: upgrade kman' : `limit ${STALE_DAYS} days`}
    />
  );
}

function AttentionList({
  items,
  selectedId,
  width,
  hasKeys,
  none,
}: {
  /** The rows to show: the caller has already windowed them. */
  items: Attention[];
  selectedId: string | undefined;
  width: number;
  hasKeys: boolean;
  none: boolean;
}) {
  if (!hasKeys) return <Text dimColor>No active keys.</Text>;
  if (none) return <Text color={color.ok}>✔ Every key is capped, current and under its limits.</Text>;
  const nameW = Math.min(20, Math.max(...items.map((i) => keyLabel(i.key).length)));
  return (
    <Box flexDirection="column">
      {items.map(({ key, risks, top }) => {
        const isSel = key.id === selectedId;
        return (
          <Text key={key.id} wrap="truncate-end">
            <Text color={color.accent}>{isSel ? '▌' : ' '}</Text>
            <Text color={severityColor[top]} dimColor={top === 'info'}>
              {severityGlyph[top]}{' '}
            </Text>
            <Text bold={isSel} color={isSel ? color.accent : undefined}>
              {padEnd(keyLabel(key), nameW)}
            </Text>
            <Text color={severityColor[top]} dimColor={top === 'info'}>
              {' '}
              {fit(risks.map((r) => r.label).join(' · '), Math.max(8, width - nameW - 5))}
            </Text>
          </Text>
        );
      })}
    </Box>
  );
}

function Activity({ audit, names, width, now }: { audit: AuditEntry[]; names: Names; width: number; now: number }) {
  if (audit.length === 0) return <Text dimColor>Nothing has changed yet.</Text>;
  const targetW = Math.max(6, width - 11 - ACTIVITY_ACTION_W - 1);
  return (
    <Box flexDirection="column">
      {audit.map((e) => (
        <Text key={e.id} wrap="truncate-end">
          <Text dimColor>{padEnd(clock(e.time, now), 11)}</Text>
          <Text color={actionColor(e.action)}>{padEnd(e.action, ACTIVITY_ACTION_W)}</Text>
          <Text> {fit(targetText(e, names), targetW)}</Text>
        </Text>
      ))}
    </Box>
  );
}
