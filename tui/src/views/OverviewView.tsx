import { Box, Text } from 'ink';
import type { AuditEntry, Key, PriceDataStatus, Upstream } from '../api.js';
import { clock, fit, fitMiddle, money, packLines, padEnd, plural, requestsText } from '../format.js';
import { keyLabel } from '../keys.js';
import type { Attention } from '../risk.js';
import { Panel, scrollHint, windowStart } from '../ui/parts.js';
import { color, severityColor, severityGlyph } from '../ui/theme.js';
import { actionColor, targetName, targetText } from './AuditView.js';
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
// The longest actions ("price_override.delete") are 21, and a space follows each.
const ACTIVITY_ACTION_W = 22;
// "11-15 01:30" is 11, and a space follows it.
const CLOCK_W = 12;
/** The most of a key's name Needs attention gives it before cutting the middle. */
const NAME_CAP = 28;
/** Names are cut in the middle to no less than this, to leave room for the labels. */
const NAME_FLOOR = 12;
/** The narrowest Recent activity can be and still show a time, an action and a little target. */
const ACTIVITY_MIN = 4 + CLOCK_W + ACTIVITY_ACTION_W + 16;
/** The built-in price table counts as stale after this many days (SPEC). */
const STALE_DAYS = 60;

/** Rows of keys Needs attention shows at once. */
export function attentionRows(needCount: number, width: number, height: number): number {
  const panelsH = height - CARD_HEIGHT;
  const attentionH = width >= 100 ? panelsH : Math.min(panelsH - 6, Math.max(5, needCount + 3));
  return Math.max(1, attentionH - 3);
}

export function OverviewView({ keys, upstreams, audit, price, needs, selected, width, height, now }: Props) {
  const live = keys.filter((k) => k.status === 'active').length;
  const disabled = keys.filter((k) => k.status === 'disabled').length;
  const revoked = keys.filter((k) => k.status === 'revoked').length;
  const today = sum(keys, 'today');
  const month = sum(keys, 'month');

  // The keys card is wider because its second line is the longest. At 80 columns the
  // cards' text widths (their width less the border, padding and gap) are 22, 12, 12 and
  // 13: "1 disabled · 0 revoked", "999 requests", and "limit 60 days".
  const unit = Math.floor(width / 4.4);
  const widths = [Math.floor(unit * 1.6), unit, unit, 0];
  widths[3] = width - widths[0]! - widths[1]! - widths[2]!;

  const wide = width >= 100;
  const panelsH = height - CARD_HEIGHT;
  const attentionH = wide ? panelsH : Math.min(panelsH - 6, Math.max(5, needs.length + 3));
  const activityH = wide ? panelsH : panelsH - attentionH;
  // The attention list takes what its longest line needs (within bounds); activity has the rest.
  // A row is its 4 columns of marks, the padded name, and its labels; the panel adds 4 more.
  const nameMax = Math.min(NAME_CAP, Math.max(0, ...needs.map((n) => [...keyLabel(n.key)].length)));
  const labelMax = Math.max(0, ...needs.map((n) => [...n.risks.map((r) => r.label).join(' · ')].length));
  const needed = 4 + nameMax + labelMax + 4;
  const attentionW = wide
    ? Math.max(Math.floor((width - 1) * 0.4), Math.min(needed, Math.floor((width - 1) * 0.6), width - 1 - ACTIVITY_MIN))
    : width;
  const activityW = wide ? width - attentionW - 1 : width;
  // Names keep what the longest needs, up to a cap, but give way (down to a floor) so the
  // labels after them, which say why a key is here, are not what is cut. Sized from every
  // row, not the visible ones, so the columns do not shift as the list scrolls.
  const nameW = Math.min(nameMax, Math.max(NAME_FLOOR, attentionW - 4 - 4 - labelMax));
  const worstIsUrgent = needs.some((n) => n.top !== 'info');
  const attnRows = attentionRows(needs.length, width, height);
  const attnSel = Math.min(selected, Math.max(0, needs.length - 1));
  const attnStart = windowStart(attnSel, needs.length, attnRows);
  const attnShown = needs.slice(attnStart, attnStart + attnRows);
  const actRows = Math.max(1, activityH - 3);
  const actShown = audit.slice(0, actRows);

  return (
    <Box flexDirection="column" width={width} height={height}>
      <Box height={CARD_HEIGHT}>
        <Card width={widths[0]!} label="KEYS" value={`${live} active`} sub={`${disabled} disabled · ${revoked} revoked`} />
        <Card width={widths[1]!} label="SPEND TODAY" value={money(today.cost)} sub={requestsText(today.requests)} />
        <Card width={widths[2]!} label="THIS MONTH" value={money(month.cost)} sub={requestsText(month.requests)} />
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
          <AttentionList
            items={attnShown}
            selectedId={needs[attnSel]?.key.id}
            width={attentionW - 4}
            nameW={nameW}
            noKeys={keys.length === 0}
            noUpstreams={upstreams.length === 0}
            hasKeys={live > 0}
            none={needs.length === 0}
          />
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
      value={price.stale ? `▲ ${price.age_days}d old` : `${price.age_days} days old`}
      tone={price.stale ? color.warn : undefined}
      sub={price.stale ? 'upgrade kman' : `limit ${STALE_DAYS} days`}
    />
  );
}

function AttentionList({
  items,
  selectedId,
  width,
  hasKeys,
  nameW,
  noKeys,
  noUpstreams,
  none,
}: {
  /** The rows to show: the caller has already windowed them. */
  items: Attention[];
  selectedId: string | undefined;
  width: number;
  hasKeys: boolean;
  /** The name column's width, the same for every row however far the list is scrolled. */
  nameW: number;
  noKeys: boolean;
  noUpstreams: boolean;
  none: boolean;
}) {
  if (noKeys && noUpstreams) return <Text dimColor>No upstream yet. Add one: kman upstream add openai --preset openai</Text>;
  if (noKeys) return <Text dimColor>No keys yet. Press n to create one.</Text>;
  if (!hasKeys) return <Text dimColor>No active keys. Press n to create one.</Text>;
  if (none) return <Text color={color.ok}>✔ Every key is capped, current and under its limits.</Text>;
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
              {padEnd(fitMiddle(keyLabel(key), nameW), nameW)}
            </Text>
            <Text color={severityColor[top]} dimColor={top === 'info'}>
              {' '}
              {/* Whole labels, with "+N more" for the rest, so a warning is never cut mid-word. */}
              {packLines(risks.map((r) => r.label), Math.max(8, width - nameW - 4), 1, ' · ')[0]}
            </Text>
          </Text>
        );
      })}
    </Box>
  );
}

function Activity({ audit, names, width, now }: { audit: AuditEntry[]; names: Names; width: number; now: number }) {
  if (audit.length === 0) return <Text dimColor>Nothing has changed yet.</Text>;
  const targetW = Math.max(6, width - CLOCK_W - ACTIVITY_ACTION_W);
  return (
    <Box flexDirection="column">
      {audit.map((e) => (
        <Text key={e.id} wrap="truncate-end">
          <Text dimColor>{padEnd(clock(e.time, now), CLOCK_W)}</Text>
          <Text color={actionColor(e.action)}>{padEnd(e.action, ACTIVITY_ACTION_W - 1)} </Text>
          {/* "key.create" already says it is a key, so only the name follows. */}
          <Text>{fit(e.action.startsWith(`${e.target_kind}.`) ? targetName(e, names) : targetText(e, names), targetW)}</Text>
        </Text>
      ))}
    </Box>
  );
}
