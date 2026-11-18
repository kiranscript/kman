import { Box, Text } from 'ink';
import type { ReactNode } from 'react';
import type { Key } from '../api.js';
import { capMoney, dateOnly, dateTime, daysLeft, fit, fitMiddle, money, packLines, padEnd, plural, requestsText, until, wrapLines } from '../format.js';
import { keyLabel } from '../keys.js';
import { keyRisks, worst } from '../risk.js';
import type { Risk } from '../risk.js';
import { Field, Meter, Panel, RiskLine, SectionTitle, StatusBadge, Wrapped, fittingScrollHint, spendText, windowStart } from '../ui/parts.js';
import { color, severityColor, severityGlyph } from '../ui/theme.js';

export const DETAIL_WIDTH = 46;
const COL_STATUS = 10;
const COL_EXPIRES = 9;
const BAR = 8;
const AMOUNT = 15;
const MIN_NAME = 12;
/** A table row: the gutter, marks, name, and the columns after it with their gaps. */
const ROW_WIDTH = (name: number) => 3 + name + 1 + COL_STATUS + 1 + (BAR + 1 + AMOUNT) + 1 + COL_EXPIRES;
/** Below this the detail pane does not fit beside a table that fits its rows, so it goes under it. */
export const WIDE = ROW_WIDTH(MIN_NAME) + 1 + DETAIL_WIDTH;
interface Props {
  keys: Key[];
  total: number;
  selectedId: string | undefined;
  filter: string;
  filtering: boolean;
  /** Whether any upstream exists: without one, a key cannot be made. */
  hasUpstreams: boolean;
  width: number;
  height: number;
  now: number;
}

/** Most lines of a note a stacked detail gives it; the wide detail gives it what the panel has left. */
const NOTE_LINES = 3;
/** The fewest key rows the list keeps beside a compact detail, and beside a full one. */
const MIN_ROWS_COMPACT = 3;
const MIN_ROWS_FULL = 5;
/** The list's filter row and its column header. */
const TABLE_CHROME = 2;

const LABEL_W = 12;

/** A list fact (models, paths) packed into as many lines as it needs beside its label; the values of a list end in commas. */
function listLines(items: string[] | undefined, fallback: string, inner: number): string[] {
  return items?.length ? packLines(items.map((v, i) => (i < items.length - 1 ? `${v},` : v)), inner - LABEL_W, 99, ' ') : [fallback];
}

/** The risks of a compact detail laid out in rows of `inner` columns, so none is cut off the end of a line. */
function packRisks(risks: Risk[], inner: number): Risk[][] {
  const rows: Risk[][] = [];
  let used = 0;
  for (const r of risks) {
    const len = 2 + [...r.label].length;
    const last = rows[rows.length - 1];
    if (last && used + 3 + len <= inner) {
      last.push(r);
      used += 3 + len;
    } else {
      rows.push([r]);
      used = len;
    }
  }
  return rows;
}

/** The risks a detail lists: the badge already says "expired", so that one is not said twice. */
function detailRisks(k: Key, now: number) {
  return keyRisks(k, now).filter((r) => !(k.expired && k.status === 'active' && r.label === 'expired'));
}

/**
 * Rows of a full detail for a key in a panel `inner` wide, without its note. `tight` leaves out
 * the blank row before each section (the section titles still set them apart).
 */
function fullBase(k: Key, now: number, inner: number, tight = false): number {
  const risks = detailRisks(k, now).length;
  const name = Math.max(1, Math.ceil([...keyLabel(k)].length / inner));
  const p = k.policy;
  const policy = 2 + listLines(p.allowed_models, 'any', inner).length + 1 + listLines(p.allowed_paths, 'upstream default', inner).length;
  const spacers = (risks > 0 ? 1 : 0) + (k.spend ? 1 : 0) + 1 + 1;
  return 2 + name + 2 + (risks > 0 ? 1 + risks : 0) + (k.spend ? 6 : 0) + policy + 1 + (k.tags.length > 0 ? 1 : 0) + (k.credential ? 1 : 0) + 1 - (tight ? spacers : 0);
}

/** How a detail is laid out: with every key's facts in full, the same without blank rows, or compactly. */
type DetailMode = 'full' | 'tight' | 'compact';

/**
 * The layout for a list of keys in `room` rows: the roomiest that fits every key's facts and at
 * least a row of its note. It depends only on the keys and the room, never on the selection, so
 * the list does not change height as the selection moves.
 */
function detailMode(keys: Key[], now: number, inner: number, room: number): DetailMode {
  const need = (tight: boolean) => Math.max(...keys.map((k) => fullBase(k, now, inner, tight) + (k.note ? 1 : 0)));
  if (need(false) <= room) return 'full';
  if (need(true) <= room) return 'tight';
  return 'compact';
}

/** Rows of a full detail, with up to `NOTE_LINES` of its note. */
function fullHeight(k: Key, now: number, inner: number, tight = false): number {
  return fullBase(k, now, inner, tight) + (k.note ? wrapLines(k.note, inner, NOTE_LINES).length : 0);
}

/** A fact as tokens: the label joins the first value, and every value but the last ends in a comma. */
function listTokens(label: string, items: string[] | undefined, fallback: string): string[] {
  const values = items?.length ? items : [fallback];
  return values.map((v, i) => `${i === 0 ? `${label} ` : ''}${v}${i < values.length - 1 ? ',' : ''}`);
}

/**
 * The facts the compact detail lists after the spend, in the order they matter, filled into
 * lines. Facts are separated by " · "; the values of a list fact break across lines one by
 * one, so a long list is wrapped rather than cut.
 */
function compactFacts(k: Key, now: number, inner: number): string[] {
  const p = k.policy;
  const facts: string[][] = [
    [k.status === 'revoked' ? 'ended for good' : `expires ${dateOnly(k.expires_at)} (${until(k.expires_at, now)})`],
    listTokens('models', p.allowed_models, 'any'),
    [`rate ${p.rpm_limit ? `${p.rpm_limit}/min` : 'unlimited'}`],
    ...(k.tags.length ? [[`tags ${k.tags.join(' ')}`]] : []),
    ...(k.credential ? [[`credential ${k.credential}`]] : []),
    listTokens('paths', p.allowed_paths, 'upstream default'),
    [`created ${dateOnly(k.created_at)}`],
  ];
  const lines: string[] = [];
  let line = '';
  for (const fact of facts) {
    fact.forEach((token, i) => {
      const text = fit(token, inner);
      const sep = i === 0 ? ' · ' : ' ';
      if (line && [...line].length + sep.length + [...text].length <= inner) line += sep + text;
      else {
        if (line) lines.push(line);
        line = text;
      }
    });
  }
  if (line) lines.push(line);
  return lines;
}

/** The compact title's name is cut to this much of the panel. */
const titleNameW = (inner: number) => Math.max(12, Math.floor((inner + 4) * 0.4));

/** "id k_ab12 · openai"; an unnamed key is already shown by its id, so then only the upstream. */
function idText(k: Key): string {
  const upstream = k.upstream ?? 'no upstream';
  return keyLabel(k) === k.id ? upstream : `id ${k.id} · ${upstream}`;
}

/** Whether the compact title is too long for one row, so the id and upstream take a row of their own. */
function titleSplit(k: Key, inner: number): boolean {
  const name = [...fitMiddle(keyLabel(k), titleNameW(inner))].length;
  const badge = 2 + [...(k.status === 'active' && k.expired ? 'expired' : k.status)].length;
  return name + 2 + badge + 2 + [...idText(k)].length > inner;
}

/** Rows of a compact detail: its border, the title, then everything else, as many rows as that takes. */
function compactHeight(k: Key, now: number, inner: number): number {
  return (
    2 + 1 + (titleSplit(k, inner) ? 1 : 0) + packRisks(detailRisks(k, now), inner).length + (k.spend ? 2 : 0) + compactFacts(k, now, inner).length + (k.note ? wrapLines(k.note, inner, NOTE_LINES).length : 0)
  );
}

/**
 * How the stacked layout is split between the list and the detail. It depends on the keys and
 * the size, never on which key is selected, so the list does not change height as the selection
 * moves: the detail is sized for the key that needs the most rows.
 */
function stackedSplit(keys: Key[], now: number, width: number, height: number): { mode: DetailMode; detailH: number; tableH: number } {
  const inner = width - 4;
  const want = TABLE_CHROME + Math.max(1, keys.length);
  // The list is promised a quarter of the rows (or all of its keys, if fewer), however large one
  // key's detail is; the detail gets the rest and drops its least important lines if it needs to.
  const listRows = Math.min(Math.max(1, keys.length), Math.max(MIN_ROWS_FULL, Math.floor((height - TABLE_CHROME) * 0.25)));
  const avail = height - TABLE_CHROME - listRows;
  // The roomiest detail that fits every key's facts; a note takes the rows that are left.
  const mode = detailMode(keys, now, inner, avail);
  const budget =
    mode === 'compact'
      ? Math.max(...keys.map((k) => compactHeight(k, now, inner)))
      : Math.max(...keys.map((k) => fullHeight(k, now, inner, mode === 'tight')));
  const detailH = Math.min(budget, avail);
  // Never fewer rows than the list was promised, so the two always fit the body together.
  const tableH = Math.max(TABLE_CHROME + listRows, Math.min(want, height - detailH));
  return { mode, detailH, tableH };
}

/** Rows of keys the list shows at once, for paging: the same layout the view draws. */
export function keyListRows(keys: Key[], now: number, width: number, height: number): number {
  if (keys.length === 0 || width >= WIDE) return Math.max(1, height - TABLE_CHROME);
  return Math.max(1, stackedSplit(keys, now, width, height).tableH - TABLE_CHROME);
}

export function KeysView({ keys, total, selectedId, filter, filtering, hasUpstreams, width, height, now }: Props) {
  const selected = keys.find((k) => k.id === selectedId);
  const wide = width >= WIDE;
  const table = { keys, total, selectedId, filter, filtering, hasUpstreams, now };

  // With nothing to show a key's detail for, the list has the whole view.
  if (!selected) return <KeyTable {...table} width={width} height={height} />;

  if (wide) {
    // The full detail when the panel is tall enough for every key's; failing that the compact
    // one, which puts the facts in order of importance and says when it had to leave some out.
    const mode = detailMode(keys, now, DETAIL_WIDTH - 4, height);
    return (
      <Box width={width} height={height}>
        <KeyTable {...table} width={width - DETAIL_WIDTH - 1} height={height} />
        <Box marginLeft={1}>
          <KeyDetail keyData={selected} width={DETAIL_WIDTH} height={height} now={now} mode={mode} />
        </Box>
      </Box>
    );
  }

  const { mode, detailH, tableH } = stackedSplit(keys, now, width, height);
  return (
    <Box width={width} height={height} flexDirection="column">
      <KeyTable {...table} width={width} height={tableH} />
      <KeyDetail keyData={selected} width={width} height={detailH} now={now} mode={mode} />
    </Box>
  );
}

function KeyTable({
  keys,
  total,
  selectedId,
  filter,
  filtering,
  hasUpstreams,
  width,
  height,
  now,
}: Omit<Props, 'width' | 'height'> & { width: number; height: number }) {
  const spendW = BAR + 1 + AMOUNT;
  const nameW = Math.min(36, Math.max(MIN_NAME, width - ROW_WIDTH(0)));
  // The filter row is always there, so opening the filter moves nothing. Next to the
  // header it takes two rows.
  const rows = Math.max(1, height - 2);
  const sel = Math.max(0, keys.findIndex((k) => k.id === selectedId));
  const start = windowStart(sel, keys.length, rows);
  const shown = keys.slice(start, start + rows);
  // The filter row: what was typed and how many match on the left, the scroll hint on the right.
  // The hint gives way (to its short form) before the left side is cut.
  const status = `  ${keys.length} of ${plural(total, 'key')}${filtering ? '' : ' · Esc clears'}`;
  const leftLen = filtering || filter !== '' ? 2 + [...filter].length + (filtering ? 1 : 0) + [...status].length : total > 0 ? 12 : 0;
  const more = fittingScrollHint(start, shown.length, keys.length, width - leftLen - 1);

  return (
    <Box flexDirection="column" width={width} height={height} overflow="hidden">
      <Box width={width} justifyContent="space-between">
        <Text wrap="truncate-end">
          {filtering || filter !== '' ? (
            <>
              <Text color={color.accent}>/ </Text>
              <Text>{filter}</Text>
              {filtering ? <Text inverse> </Text> : null}
              <Text dimColor>{status}</Text>
            </>
          ) : total > 0 ? (
            <Text dimColor>/ to filter</Text>
          ) : (
            <Text> </Text>
          )}
        </Text>
        <Box flexShrink={0}>
          <Text dimColor>{more}</Text>
        </Box>
      </Box>
      <Text dimColor bold>
        {'   '}
        {padEnd('NAME', nameW)} {padEnd('STATUS', COL_STATUS)} {padEnd('SPEND TODAY', spendW)} {padEnd('EXPIRES', COL_EXPIRES)}
      </Text>
      {keys.length === 0 ? (
        <Box marginTop={1} flexDirection="column">
          <Text dimColor>
            {total > 0
              ? `No key matches "${filter}".`
              : hasUpstreams
                ? 'No virtual keys yet. Press n to create one.'
                : 'No keys yet. First add an upstream: kman upstream add openai --preset openai'}
          </Text>
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
              {padEnd(fitMiddle(keyLabel(k), nameW), nameW)}
            </Text>
            <Text> </Text>
            <Box width={COL_STATUS}>
              <StatusBadge status={k.status} expired={k.expired} />
            </Box>
            <Text> </Text>
            <Box width={spendW}>
              {dim ? (
                <Text dimColor>
                  {'·'.repeat(BAR)} {padEnd(spendText(today), AMOUNT)}
                </Text>
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
  mode,
}: {
  keyData: Key | undefined;
  width: number;
  height: number;
  now: number;
  mode: DetailMode;
}) {
  if (!k) {
    return (
      <Panel width={width} height={height}>
        <Text dimColor>No key selected.</Text>
      </Panel>
    );
  }
  if (mode === 'compact') return <CompactDetail keyData={k} width={width} height={height} now={now} />;
  const gap = mode === 'tight' ? 0 : 1;
  const risks = detailRisks(k, now);
  const inner = width - 4;
  const p = k.policy;
  const barW = Math.min(14, Math.max(8, inner - 24));
  return (
    <Panel width={width} height={height}>
      <Text bold wrap="wrap">
        {keyLabel(k)}
      </Text>
      <Text wrap="truncate-end">
        <StatusBadge status={k.status} expired={k.expired} />
        <Text dimColor>
          {'  '}
          {idText(k)}
        </Text>
      </Text>
      <Text dimColor wrap="truncate-end">
        {k.status === 'revoked' ? 'ended for good' : `expires ${dateTime(k.expires_at)} (${until(k.expires_at, now)})`}
      </Text>

      {risks.length > 0 ? (
        <Box flexDirection="column" marginTop={gap}>
          {risks.map((r) => (
            <RiskLine key={r.label} risk={r} />
          ))}
        </Box>
      ) : null}

      {k.spend ? (
        <Box flexDirection="column" marginTop={gap}>
          <SectionTitle>SPEND</SectionTitle>
          {(['today', 'month'] as const).map((which) => {
            const s = k.spend![which];
            return (
              <Box key={which} flexDirection="column">
                <Box>
                  <Text dimColor>{padEnd(which, 6)}</Text>
                  <Meter period={s} width={barW} muted={k.status === 'revoked'} />
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

      <Box flexDirection="column" marginTop={gap}>
        <SectionTitle>POLICY</SectionTitle>
        <ListField label="models" lines={listLines(p.allowed_models, 'any', inner)} />
        <Field label="rate">{p.rpm_limit ? `${p.rpm_limit} requests/min` : 'unlimited'}</Field>
        <ListField label="paths" lines={listLines(p.allowed_paths, 'upstream default', inner)} />
      </Box>

      <Box flexDirection="column" marginTop={gap}>
        {k.tags.length > 0 ? <Field label="tags">{k.tags.join('  ')}</Field> : null}
        {k.credential ? <Field label="credential">{k.credential}</Field> : null}
        <Field label="created">{dateTime(k.created_at)}</Field>
        {k.note ? <Wrapped text={k.note} width={inner} maxLines={Math.max(1, height - fullBase(k, now, inner, mode === 'tight'))} dim /> : null}
      </Box>
    </Panel>
  );
}

/** A label, then its value on as many lines as it was packed into, continuation lines aligned under the first. */
function ListField({ label, lines }: { label: string; lines: string[] }) {
  return (
    <Box flexDirection="column">
      {lines.map((line, i) => (
        <Text key={i} wrap="truncate-end">
          <Text dimColor>{padEnd(i === 0 ? label : '', LABEL_W)}</Text>
          {line}
        </Text>
      ))}
    </Box>
  );
}

/** The same facts in fewer rows, for a panel under the list: all of them, in as many rows as the panel has. */
function CompactDetail({ keyData: k, width, height, now }: { keyData: Key; width: number; height: number; now: number }) {
  const inner = width - 4;
  const split = titleSplit(k, inner);
  const risks = detailRisks(k, now);
  // Both spend rows share one meter width and one layout, worked out from the longer of the
  // two, so the bars are the same length. Roomy: the amounts line up in a column. Narrow (the
  // side panel): they follow one another.
  const periods = (['today', 'month'] as const).flatMap((which) => {
    const s = k.spend?.[which];
    if (!s) return [];
    const amount = s.cap_micro ? `${money(s.cost_micro)} / ${capMoney(s.cap_micro)}` : `${money(s.cost_micro)}, no cap`;
    const full = `${plural(s.requests, 'request')}${s.unmetered_requests ? `, ${s.unmetered_requests} unmetered` : ''}`;
    return [{ which, s, amount, full }];
  });
  const roomy = periods.every((p) => inner >= 6 + 12 + 1 + 22 + [...p.full].length);
  // Narrow, the unmetered count is left to the risk line above, which is laid out so as never to be cut.
  // If even a 4-cell meter leaves no room for the count in words, it is shortened ("123k reqs").
  const reqsOf = (p: (typeof periods)[number]) => {
    if (roomy) return p.full;
    const words = plural(p.s.requests, 'request');
    return inner - 6 - 1 - 4 - [...p.amount].length - 3 >= [...words].length ? words : requestsText(p.s.requests);
  };
  const meter = roomy ? 12 : Math.max(4, Math.min(12, ...periods.map((p) => inner - 6 - 1 - [...p.amount].length - 3 - [...reqsOf(p)].length)));
  const row = (which: 'today' | 'month') => {
    const p = periods.find((x) => x.which === which);
    if (!p) return null;
    return (
      <Text key={which} wrap="truncate-end">
        <Text dimColor>{padEnd(which, 6)}</Text>
        <Meter period={p.s} width={meter} muted={k.status === 'revoked'} />
        <Text> {roomy ? padEnd(p.amount, 22) : `${p.amount} · `}</Text>
        <Text dimColor>{reqsOf(p)}</Text>
      </Text>
    );
  };
  // In order of importance, so that when the panel is short it is the end that goes.
  const lines: ReactNode[] = [
    <Text key="title" wrap="truncate-end">
      <Text bold>{fitMiddle(keyLabel(k), titleNameW(inner))}</Text>
      <Text>{'  '}</Text>
      <StatusBadge status={k.status} expired={k.expired} />
      {split ? null : (
        <Text dimColor>
          {'  '}
          {idText(k)}
        </Text>
      )}
    </Text>,
  ];
  if (split) {
    lines.push(
      <Text key="id" dimColor wrap="truncate-end">
        {idText(k)}
      </Text>,
    );
  }
  packRisks(risks, inner).forEach((group, gi) =>
    lines.push(
      <Text key={`risks${gi}`} wrap="truncate-end">
        {group.map((r, i) => (
          <Text key={r.label} color={severityColor[r.severity]} dimColor={r.severity === 'info'}>
            {i > 0 ? '   ' : ''}
            {severityGlyph[r.severity]} {r.label}
          </Text>
        ))}
      </Text>,
    ),
  );
  lines.push(row('today'), row('month'));
  compactFacts(k, now, inner).forEach((line, i) =>
    lines.push(
      <Text key={`f${i}`} dimColor wrap="truncate-end">
        {line}
      </Text>,
    ),
  );
  if (k.note) {
    // At least `NOTE_LINES`, and more when the panel has rows to spare (its height was fixed
    // beforehand, from the list, so using them cannot move anything).
    const spare = Math.max(NOTE_LINES, height - 2 - lines.filter(Boolean).length);
    wrapLines(k.note, inner, spare).forEach((line, i) =>
      lines.push(
        <Text key={`n${i}`} dimColor wrap="truncate-end">
          {line}
        </Text>,
      ),
    );
  }
  const all = lines.filter(Boolean);
  const room = Math.max(1, height - 2);
  // When the panel is too short for everything, the last row says how much is not shown.
  const shown =
    all.length > room
      ? [
          ...all.slice(0, room - 1),
          <Text key="more" dimColor wrap="truncate-end">
            … {all.length - room + 1} more rows: make the terminal taller
          </Text>,
        ]
      : all;
  return <Panel width={width} height={height}>{shown}</Panel>;
}
