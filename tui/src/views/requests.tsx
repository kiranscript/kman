// The requests table in the main panel, newest first, with the flagged ones marked, and the
// short list of them under a key's detail.
import { Box, Text } from 'ink';
import type { RequestEvent } from '../api.js';
import { clock, fit, fitMiddle, padEnd, since, textWidth, wrapLines } from '../format.js';
import { requestFrom, requestKey, resultText } from '../requests.js';
import type { Flag, RequestFilter } from '../requests.js';
import { useTick } from '../ui/chrome.js';
import { Frame } from '../ui/frame.js';
import { windowStart } from '../ui/parts.js';
import { Row } from '../ui/row.js';
import type { Seg } from '../ui/row.js';
import { color, severityColor, severityGlyph } from '../ui/theme.js';
import type { Requests } from '../useAdmin.js';

export interface Flagged {
  e: RequestEvent;
  flag?: Flag;
}

const flagSeg = (flag: Flag | undefined): Seg => (flag ? { t: `${severityGlyph[flag.severity]} `, color: severityColor[flag.severity] } : { t: '  ' });

type Column = 'time' | 'key' | 'from' | 'org' | 'operation' | 'model' | 'address' | 'client' | 'result';

const heads: Record<Column, string> = {
  time: 'TIME',
  key: 'KEY',
  from: 'FROM',
  org: 'ORGANISATION',
  operation: 'OPERATION',
  model: 'MODEL',
  address: 'ADDRESS',
  client: 'CLIENT',
  result: 'RESULT',
};

const order: Column[] = ['time', 'key', 'from', 'org', 'operation', 'model', 'address', 'client', 'result'];

const value = ({ e, flag }: Flagged, column: Column, now: number): string => {
  switch (column) {
    case 'time':
      return clock(e.time, now);
    case 'key':
      return requestKey(e);
    case 'from':
      return requestFrom(e);
    case 'org':
      return e.asn_label ?? '';
    case 'operation':
      return e.operation ?? '';
    case 'model':
      return e.model ?? '';
    case 'address':
      return e.client_address ?? '—';
    case 'client':
      return e.client ?? '—';
    case 'result':
      return resultText(e, flag);
  }
};

/** The least each column that can be dropped needs to be worth showing, in the order they are given room. */
const optional: [Column, number][] = [
  ['org', 16],
  ['operation', 24],
  ['model', 14],
];

/**
 * The columns a table `width` wide has, and the width of each, without the flag and one
 * space between columns. Time, key, country and AS, client and result always stay; as room
 * shrinks the model goes first, then the operation, then the organisation. The address has
 * a column only while kman gives addresses out, and one key's table needs no key column.
 */
export function columns(rows: Flagged[], width: number, now: number, oneKey: boolean): { column: Column; width: number }[] {
  const longest = (c: Column) => Math.max(textWidth(heads[c]), ...rows.map((r) => textWidth(value(r, c, now))));
  const clamp = (c: Column, min: number, max: number) => Math.min(max, Math.max(min, longest(c)));
  const addresses = rows.some((r) => r.e.client_address);
  // With an address to fit too, a narrow table gives the others less.
  const tight = addresses && width < 100;
  const widths = new Map<Column, number>([
    ['time', clamp('time', 8, 11)],
    ['from', clamp('from', 10, tight ? 13 : 15)],
    ['client', clamp('client', 8, tight ? 12 : 18)],
  ]);
  if (!oneKey) widths.set('key', clamp('key', 8, tight ? 10 : width < 100 ? 12 : 20));
  if (addresses) widths.set('address', clamp('address', 9, tight ? 15 : 24));
  const wanted = clamp('result', 12, 32);
  const used = () => 2 + [...widths.values()].reduce((n, w) => n + w + 1, 0);
  for (const [c, min] of optional) {
    if (used() + min + 1 + wanted > width) break;
    widths.set(c, min);
  }
  // What is left goes to the result, and then to the columns that are cut.
  let spare = width - used() - wanted;
  // An IPv6 address is long: it is shown whole before the others grow.
  for (const c of ['address', 'org', 'operation', 'model'] as Column[]) {
    const have = widths.get(c);
    if (have === undefined || spare <= 0) continue;
    const grow = Math.max(0, Math.min(spare, longest(c) - have));
    widths.set(c, have + grow);
    spare -= grow;
  }
  widths.set('result', Math.max(6, width - used()));
  return order.filter((c) => widths.has(c)).map((c) => ({ column: c, width: widths.get(c)! }));
}

const cells = (r: Flagged, cols: ReturnType<typeof columns>, now: number): Seg[] =>
  cols.map(({ column, width }, n) => {
    const text = value(r, column, now);
    // A name's or an address's end tells it apart.
    const cut = column === 'key' || column === 'address' ? fitMiddle(text, width) : fit(text, width);
    return {
      t: `${cut}${' '.repeat(Math.max(0, width - textWidth(cut)))}${n < cols.length - 1 ? ' ' : ''}`,
      color: column === 'result' && r.flag ? severityColor[r.flag.severity] : undefined,
      dim: column === 'time',
    };
  });

/** The two lines under the selected row: what its columns may have cut, and why it is flagged. */
function under({ e, flag }: Flagged): [string, string] {
  const what = [e.operation || 'no operation', e.model, e.asn_label].filter(Boolean).join(' · ');
  const refusal = e.refusal_code && !flag?.why.includes(e.refusal_code) ? ` · refused: ${e.refusal_code}` : '';
  return [what, flag ? `${severityGlyph[flag.severity]} ${flag.why}${refusal}` : 'not flagged'];
}

/** The legend: what the marks claim, and what they cannot. */
export const LEGEND = [
  `${severityGlyph.danger} canary, or like an open incident (maybe yours) · ${severityGlyph.warn} refused`,
  'Unflagged: what no rule raised (learning, known source), or resolved since',
];

export function RequestsMain({
  width,
  height,
  rows,
  selected,
  filter,
  keyName,
  state,
  stale,
  incomplete,
  fresh,
  now,
  onView,
}: {
  width: number;
  height: number;
  /** The rows the filter lets through, newest first. */
  rows: Flagged[];
  /** The selected row's place in `rows`. */
  selected: number;
  filter: RequestFilter;
  /** Set for the table of one key. */
  keyName?: string;
  state: Requests;
  /** The rows may be old: kman did not answer. */
  stale: boolean;
  /** Why flags may be missing. */
  incomplete?: string;
  /** Rows that arrived above the selection and have not been seen. */
  fresh: number;
  now: number;
  onView: (rows: number) => void;
}) {
  useTick(1000);
  const inner = width - 2;
  const warnings = [
    ...(stale ? [`${severityGlyph.warn} Not updated${state.error ? `: ${state.error}` : ': kman does not answer'}`] : []),
    ...(incomplete ? [`${severityGlyph.warn} ${incomplete}`] : []),
  ];
  const legend = height >= 18 ? LEGEND : [`${LEGEND[0]} · ? more`];
  // The header, the warnings, the legend and the border are not rows of the list.
  const area = Math.max(1, height - 2 - 1 - warnings.length - legend.length);
  const detail = rows[selected] ? 2 : 0;
  const visible = Math.max(1, area - detail);
  onView(visible);
  const start = windowStart(selected, rows.length, visible);
  const shown = rows.slice(start, start + visible);
  const cols = columns(shown, inner - 1, now, keyName !== undefined);
  const title = `Requests · ${keyName ?? 'all keys'}${filter === 'all' ? '' : ` · ${filter}`}${fresh > 0 ? ` · ${fresh} new` : ''}`;
  const freshness = state.at === undefined ? undefined : `${stale ? 'old' : 'live'} · ${since(state.at, Date.now())}`;
  const below = rows.length - start - shown.length;
  const empty = !state.loaded
    ? state.error
      ? `Could not load them: ${state.error}`
      : 'Loading…'
    : filter === 'flagged'
      ? `No flagged request among the ${state.rows.length} loaded.${state.more ? ' ↓ looks further back.' : ''}`
      : filter === 'refused'
        ? 'No refused requests.'
        : 'No requests yet. They are listed about 5s after they are made.';
  const older = state.loadingOlder ? 'Loading older…' : below === 0 && state.more && selected >= rows.length - 1 && rows.length > 0 ? '↓ loads older' : '';
  return (
    <Frame
      title={title}
      subtitle={freshness}
      position={`${older ? `${older} · ` : ''}f all/flagged/refused · Enter open · g newest`}
      focused
      width={width}
      height={height}
    >
      {warnings.map((w) => (
        <Text key={w} color={color.warn} wrap="truncate-end">
          {' '}
          {w}
        </Text>
      ))}
      <Text dimColor bold wrap="truncate-end">
        {'   '}
        {cols.map(({ column, width: w }) => padEnd(heads[column], w)).join(' ')}
      </Text>
      <Box flexDirection="column" height={area} flexShrink={0} overflow="hidden">
        {rows.length === 0 ? wrapLines(empty, inner - 2, 4).map((l) => <Text key={l} dimColor>{` ${l}`}</Text>) : null}
        {shown.map((r, n) => {
          const on = start + n === selected;
          return (
            <Box key={r.e.id} flexDirection="column" flexShrink={0}>
              <Row left={[{ t: ' ' }, flagSeg(r.flag), ...cells(r, cols, now)]} width={inner} selected={on} focused />
              {on
                ? under(r).map((line, i) => (
                    <Text key={i} color={i === 1 && r.flag ? severityColor[r.flag.severity] : undefined} dimColor={i === 0 || !r.flag} wrap="truncate-end">
                      {'    '}
                      {line}
                    </Text>
                  ))
                : null}
            </Box>
          );
        })}
      </Box>
      {legend.map((l) => (
        <Text key={l} dimColor wrap="truncate-end">
          {' '}
          {l}
        </Text>
      ))}
    </Frame>
  );
}

/** A key's last few requests, a line each: flag, time, from, client and result. */
export function LastRequests({ rows, width, now }: { rows: Flagged[]; width: number; now: number }) {
  const timeW = Math.max(...rows.map((r) => textWidth(clock(r.e.time, now))));
  const fromW = Math.min(15, Math.max(...rows.map((r) => textWidth(requestFrom(r.e)))));
  const clientW = Math.min(18, Math.max(...rows.map((r) => textWidth(r.e.client ?? '—'))));
  return (
    <Box flexDirection="column">
      {rows.map((r) => {
        const mark = flagSeg(r.flag);
        return (
          <Text key={r.e.id} wrap="truncate-end">
            <Text color={mark.color}>{mark.t}</Text>
            <Text dimColor>{padEnd(clock(r.e.time, now), timeW + 1)}</Text>
            {padEnd(requestFrom(r.e), fromW + 1)}
            {/* The result says what came of it: the client gives way first. */}
            {padEnd(r.e.client ?? '—', Math.max(6, Math.min(clientW, width - 2 - timeW - 1 - fromW - 1 - 20)) + 1)}
            <Text color={r.flag ? severityColor[r.flag.severity] : undefined}>{resultText(r.e, r.flag)}</Text>
          </Text>
        );
      })}
    </Box>
  );
}
