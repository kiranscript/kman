import { Box, Text } from 'ink';
import type { Key } from '../api.js';
import { capMoney, dateOnly, dateTime, daysLeft, fit, money, padEnd, plural, until } from '../format.js';
import { keyLabel } from '../keys.js';
import { keyRisks, worst } from '../risk.js';
import { Field, Meter, Panel, RiskLine, SectionTitle, StatusBadge, scrollHint, spendText, windowStart } from '../ui/parts.js';
import { color, severityColor, severityGlyph } from '../ui/theme.js';

export const DETAIL_WIDTH = 46;
/** Below this the detail pane does not fit beside the list, so it goes under it. */
export const WIDE = 100;
/** Rows the compact detail needs for a key: its border and one row per fact it shows. */
function compactHeight(k: Key | undefined, now: number): number {
  if (!k) return 3;
  return 2 + 1 + (keyRisks(k, now).length > 0 ? 1 : 0) + (k.spend ? 2 : 0) + 1 + (k.note ? 1 : 0);
}

interface Props {
  keys: Key[];
  total: number;
  selectedId: string | undefined;
  filter: string;
  filtering: boolean;
  width: number;
  height: number;
  now: number;
}

export function KeysView({ keys, total, selectedId, filter, filtering, width, height, now }: Props) {
  const selected = keys.find((k) => k.id === selectedId);
  const wide = width >= WIDE;
  const table = { keys, total, selectedId, filter, filtering, now };

  if (wide) {
    return (
      <Box width={width} height={height}>
        <KeyTable {...table} width={width - DETAIL_WIDTH - 1} height={height} />
        <Box marginLeft={1}>
          <KeyDetail keyData={selected} width={DETAIL_WIDTH} height={height} now={now} />
        </Box>
      </Box>
    );
  }

  // Stacked: the detail takes only the rows it needs, and the table gets the rest.
  const detailH = compactHeight(selected, now);
  const tableH = Math.max(2 + 3, Math.min(2 + Math.max(1, keys.length), height - detailH));
  return (
    <Box width={width} height={height} flexDirection="column">
      <KeyTable {...table} width={width} height={tableH} />
      <KeyDetail keyData={selected} width={width} height={Math.min(detailH, height - tableH)} now={now} compact />
    </Box>
  );
}

const COL_STATUS = 10;
const COL_EXPIRES = 9;
const BAR = 8;
const AMOUNT = 15;

function KeyTable({
  keys,
  total,
  selectedId,
  filter,
  filtering,
  width,
  height,
  now,
}: Omit<Props, 'width' | 'height'> & { width: number; height: number }) {
  const spendW = BAR + 1 + AMOUNT;
  const nameW = Math.min(36, Math.max(12, width - 3 - COL_STATUS - spendW - COL_EXPIRES - 3));
  // The filter row is always there, so opening the filter moves nothing. Next to the
  // header it takes two rows.
  const rows = Math.max(1, height - 2);
  const sel = Math.max(0, keys.findIndex((k) => k.id === selectedId));
  const start = windowStart(sel, keys.length, rows);
  const shown = keys.slice(start, start + rows);
  const more = scrollHint(start, shown.length, keys.length);

  return (
    <Box flexDirection="column" width={width} height={height} overflow="hidden">
      <Box width={width} justifyContent="space-between">
        <Text>
          {filtering || filter !== '' ? (
            <>
              <Text color={color.accent}>/ </Text>
              <Text>{filter}</Text>
              {filtering ? <Text inverse> </Text> : null}
              <Text dimColor>
                {'  '}
                {keys.length} of {plural(total, 'key')}
                {filtering ? '' : ' · Esc clears'}
              </Text>
            </>
          ) : (
            <Text dimColor>/ to filter</Text>
          )}
        </Text>
        <Text dimColor>{more}</Text>
      </Box>
      <Text dimColor bold>
        {'   '}
        {padEnd('NAME', nameW)} {padEnd('STATUS', COL_STATUS)} {padEnd('SPEND TODAY', spendW)} {padEnd('EXPIRES', COL_EXPIRES)}
      </Text>
      {keys.length === 0 ? (
        <Box marginTop={1} flexDirection="column">
          <Text dimColor>{total === 0 ? 'No virtual keys yet. Press n to create one.' : `No key matches "${filter}".`}</Text>
        </Box>
      ) : null}
      {shown.map((k) => {
        const isSel = k.id === selectedId;
        const risks = keyRisks(k, now);
        const top = worst(risks);
        const dim = k.status === 'revoked';
        const today = k.spend?.today;
        return (
          <Box key={k.id}>
            <Text color={color.accent}>{isSel ? '▌' : ' '}</Text>
            {top ? (
              <Text color={severityColor[top]} dimColor={top === 'info'}>
                {severityGlyph[top]}
              </Text>
            ) : (
              <Text> </Text>
            )}
            <Text bold={isSel} color={isSel ? color.accent : undefined} dimColor={dim}>
              {' '}
              {padEnd(keyLabel(k), nameW)}
            </Text>
            <Text> </Text>
            <Box width={COL_STATUS}>
              <StatusBadge status={k.status} expired={k.expired} />
            </Box>
            <Text> </Text>
            <Box width={spendW}>
              {dim ? (
                <Text dimColor>—</Text>
              ) : (
                <>
                  <Meter period={today} width={BAR} />
                  <Text dimColor={!today?.cap_micro}> {padEnd(spendText(today), AMOUNT)}</Text>
                </>
              )}
            </Box>
            <Text> </Text>
            <Text
              color={dim ? undefined : k.expired ? color.danger : daysLeft(k.expires_at, now) < 7 ? color.warn : undefined}
              dimColor={dim || (!isSel && !k.expired && daysLeft(k.expires_at, now) >= 7)}
            >
              {padEnd(dim ? '—' : until(k.expires_at, now), COL_EXPIRES)}
            </Text>
          </Box>
        );
      })}
    </Box>
  );
}

function KeyDetail({
  keyData: k,
  width,
  height,
  now,
  compact,
}: {
  keyData: Key | undefined;
  width: number;
  height: number;
  now: number;
  compact?: boolean;
}) {
  if (!k) {
    return (
      <Panel width={width} height={height}>
        <Text dimColor>No key selected.</Text>
      </Panel>
    );
  }
  if (compact) return <CompactDetail keyData={k} width={width} height={height} now={now} />;
  const risks = keyRisks(k, now);
  const inner = width - 4;
  const p = k.policy;
  const barW = Math.min(14, Math.max(8, inner - 24));
  return (
    <Panel width={width} height={height}>
      <Text bold wrap="truncate-end">
        {keyLabel(k)}
      </Text>
      <Text wrap="truncate-end">
        <StatusBadge status={k.status} expired={k.expired} />
        <Text dimColor>
          {'  '}
          id {k.id} · {k.upstream ?? 'no upstream'}
        </Text>
      </Text>
      <Text dimColor wrap="truncate-end">
        {k.status === 'revoked' ? 'ended for good' : `expires ${dateTime(k.expires_at)} (${until(k.expires_at, now)})`}
      </Text>

      {risks.length > 0 ? (
        <Box flexDirection="column" marginTop={1}>
          {risks.map((r) => (
            <RiskLine key={r.label} risk={r} />
          ))}
        </Box>
      ) : null}

      {k.spend ? (
        <Box flexDirection="column" marginTop={1}>
          <SectionTitle>SPEND</SectionTitle>
          {(['today', 'month'] as const).map((which) => {
            const s = k.spend![which];
            return (
              <Box key={which} flexDirection="column">
                <Box>
                  <Text dimColor>{padEnd(which, 6)}</Text>
                  <Meter period={s} width={barW} />
                  <Text> {s.cap_micro ? `${money(s.cost_micro)} / ${capMoney(s.cap_micro)}` : `${money(s.cost_micro)}, no cap`}</Text>
                </Box>
                <Text dimColor>
                  {'      '}
                  {plural(s.requests, 'request')}
                  {s.unmetered_requests ? `, ${s.unmetered_requests} unmetered` : ''}
                </Text>
              </Box>
            );
          })}
        </Box>
      ) : null}

      <Box flexDirection="column" marginTop={1}>
        <SectionTitle>POLICY</SectionTitle>
        <Field label="models">{p.allowed_models?.length ? p.allowed_models.join(', ') : 'any'}</Field>
        <Field label="rate">{p.rpm_limit ? `${p.rpm_limit} requests/min` : 'unlimited'}</Field>
        <Field label="paths">{p.allowed_paths?.length ? p.allowed_paths.join(', ') : 'upstream default'}</Field>
      </Box>

      <Box flexDirection="column" marginTop={1}>
        {k.tags.length > 0 ? <Field label="tags">{k.tags.join('  ')}</Field> : null}
        {k.credential ? <Field label="credential">{k.credential}</Field> : null}
        <Field label="created">{dateTime(k.created_at)}</Field>
        {k.note ? <Text dimColor>{fit(k.note, inner * 2)}</Text> : null}
      </Box>
    </Panel>
  );
}

/** The same facts on fewer rows, for a panel under the list. */
function CompactDetail({ keyData: k, width, height, now }: { keyData: Key; width: number; height: number; now: number }) {
  const risks = keyRisks(k, now);
  const p = k.policy;
  const row = (which: 'today' | 'month') => {
    const s = k.spend?.[which];
    if (!s) return null;
    return (
      <Box key={which}>
        <Text dimColor>{padEnd(which, 6)}</Text>
        <Meter period={s} width={12} />
        <Text>
          {' '}
          {padEnd(s.cap_micro ? `${money(s.cost_micro)} / ${capMoney(s.cap_micro)}` : `${money(s.cost_micro)}, no cap`, 22)}
        </Text>
        <Text dimColor>
          {plural(s.requests, 'request')}
          {s.unmetered_requests ? `, ${s.unmetered_requests} unmetered` : ''}
        </Text>
      </Box>
    );
  };
  return (
    <Panel width={width} height={height}>
      <Text wrap="truncate-end">
        <Text bold>{keyLabel(k)}</Text>
        <Text>{'  '}</Text>
        <StatusBadge status={k.status} expired={k.expired} />
        <Text dimColor>
          {'  '}
          id {k.id} · {k.upstream ?? 'no upstream'} ·{' '}
          {k.status === 'revoked' ? 'ended for good' : `expires ${dateOnly(k.expires_at)} (${until(k.expires_at, now)})`}
        </Text>
      </Text>
      {risks.length > 0 ? (
        <Text wrap="truncate-end">
          {risks.map((r, i) => (
            <Text key={r.label} color={severityColor[r.severity]} dimColor={r.severity === 'info'}>
              {i > 0 ? '   ' : ''}
              {severityGlyph[r.severity]} {r.label}
            </Text>
          ))}
        </Text>
      ) : null}
      {row('today')}
      {row('month')}
      <Text dimColor wrap="truncate-end">
        models {p.allowed_models?.length ? p.allowed_models.join(', ') : 'any'} · rate{' '}
        {p.rpm_limit ? `${p.rpm_limit}/min` : 'unlimited'}
        {k.tags.length ? ` · tags ${k.tags.join(' ')}` : ''}
      </Text>
      {k.note ? (
        <Text dimColor wrap="truncate-end">
          {k.note}
        </Text>
      ) : null}
    </Panel>
  );
}
