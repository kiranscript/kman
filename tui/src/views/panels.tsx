// The panels down the left side: Status, Keys, Upstreams and Audit. Each is a
// Frame with a list in it. The focused one is tall and shows its selected line as a
// blue bar; the others shrink until only their selected line shows.
import { Text } from 'ink';
import type { AuditEntry, Key, PriceDataStatus, Upstream } from '../api.js';
import { clock, money, padEnd, since, tailCells } from '../format.js';
import { keyLabel } from '../keys.js';
import { keyRisks, topRisk } from '../risk.js';
import type { Attention } from '../risk.js';
import { linkBadge, useTick } from '../ui/chrome.js';
import { Frame } from '../ui/frame.js';
import { Row } from '../ui/row.js';
import type { Seg } from '../ui/row.js';
import { color, ratioColor, severityColor, severityGlyph, statusStyle } from '../ui/theme.js';
import { capRatio } from '../risk.js';
import type { Link } from '../useAdmin.js';
import { actionColor, targetText } from './audit.js';
import type { Names } from './audit.js';
import { windowStart } from '../ui/parts.js';

interface PanelProps {
  width: number;
  height: number;
  focused: boolean;
}

/** The rows that fit, scrolled to keep `selected` in view, and what was cut. */
function windowed<T>(items: T[], selected: number, rows: number) {
  const start = windowStart(selected, items.length, rows);
  return { shown: items.slice(start, start + rows), start };
}

/** Expired but not yet marked as such by its status. */
const lapsed0 = (k: Key) => k.expired && k.status === 'active';

const position = (selected: number, count: number) => (count > 0 ? `${selected + 1} of ${count}` : undefined);

// ---- Status ---------------------------------------------------------------

const segLen = (segs: Seg[]) => segs.reduce((n, x) => n + [...x.t].length, 0);

/** The parts that fit in `max` columns, in order of priority, one space between. */
function fitParts(parts: Seg[][], max: number): Seg[] {
  const out: Seg[] = [];
  let used = 0;
  for (const part of parts) {
    const need = segLen(part) + (out.length ? 1 : 0);
    if (used + need > max) continue;
    if (out.length) out.push({ t: ' ' });
    out.push(...part);
    used += need;
  }
  return out;
}

/** Status: the summary of the install and, when focused, the keys that need attention. */
export function StatusPanel({
  width,
  height,
  focused,
  link,
  url,
  actor,
  keys,
  needs,
  selected,
  price,
}: PanelProps & {
  link: Link;
  url: string;
  actor?: string;
  keys: Key[];
  needs: Attention[];
  selected: number;
  price?: PriceDataStatus;
}) {
  useTick(1000);
  const badge = linkBadge(link);
  const live = keys.filter((k) => k.status === 'active').length;
  const disabled = keys.filter((k) => k.status === 'disabled').length;
  const revoked = keys.filter((k) => k.status === 'revoked').length;
  const spend = (which: 'today' | 'month') => keys.reduce((n, k) => n + (k.spend?.[which].cost_micro ?? 0), 0);
  const urgent = needs.filter((n) => n.top !== 'info').length;
  const inner = width - 2;

  const linkText =
    link.state === 'ok'
      ? `updated ${since(link.at, Date.now())}`
      : link.state === 'down' && link.lastOk
        ? `last data ${since(link.lastOk, Date.now())}`
        : link.state === 'denied'
          ? link.error
          : '';
  const priceSeg = (compact: boolean): Seg[] =>
    price
      ? price.stale
        ? [{ t: compact ? '▲ price stale' : `▲ price stale (${price.age_days}d) · upgrade kman`, color: color.warn }]
        : [{ t: `price ${compact ? `${price.age_days}d` : `data ${price.age_days} days old`}`, dim: true }]
      : [{ t: 'price data unknown', dim: true }];
  const badgeSeg: Seg = { t: `${badge.glyph} ${badge.word}`, color: badge.color, dim: badge.color === undefined };
  // One line that carries the connection, the count, the spend and the price: the
  // summary when there is room for only one line. What does not fit is dropped from the end.
  const compactLine: Seg[] = fitParts(
    [
      [badgeSeg],
      ...(price?.stale ? [[{ t: '·', dim: true }, { t: ' ' }, ...priceSeg(true)]] : []),
      [{ t: '·', dim: true }, { t: ` ${live} active`, color: live > 0 ? color.ok : undefined }],
      [{ t: '·', dim: true }, { t: ` ${money(spend('today'))} today`, bold: true }],
      ...(price && !price.stale ? [[{ t: '·', dim: true }, { t: ' ' }, ...priceSeg(true)]] : []),
    ],
    inner - 1,
  );
  const whereLine: Seg[] = [{ t: `${actor ? `${actor} @ ` : ''}${url}`, dim: true }];
  const countsLine: Seg[] = [
    { t: `${live} active`, color: live > 0 ? color.ok : undefined },
    { t: ` · ${disabled} disabled · ${revoked} revoked`, dim: true },
  ];
  const fullLines: Seg[][] = [
    [badgeSeg, { t: linkText ? ` · ${linkText}` : '', dim: true }],
    whereLine,
    countsLine,
    [
      { t: `${money(spend('today'))} today`, bold: true },
      { t: ` · ${money(spend('month'))} this month`, dim: true },
    ],
    priceSeg(false),
  ];
  const dot: Seg = { t: '·', dim: true };
  const priceOk = price && !price.stale ? [[dot, { t: ' ' }, ...priceSeg(true)]] : [];
  const priceStale = price?.stale ? [[dot, { t: ' ' }, ...priceSeg(true)]] : [];
  const activePart: Seg[] = [
    dot,
    { t: ` ${live} active`, color: live > 0 ? color.ok : undefined },
    { t: ` · ${disabled} disabled · ${revoked} revoked`, dim: true },
  ];
  /** The connection, a stale price, and the count when `withCount`. */
  const head = (withCount: boolean): Seg[] =>
    fitParts([[badgeSeg], ...priceStale, ...(withCount ? [activePart] : []), ...priceOk], inner - 1);
  /** The spend, with the price when the line above does not already carry it. */
  const money2 = (above: Seg[]): Seg[] =>
    fitParts(
      [
        [{ t: `${money(spend('today'))} today`, bold: true }],
        ...(above.some((x) => x.t.includes('price')) ? [] : price ? [[dot, { t: ' ' }, ...priceSeg(true)]] : []),
        [dot, { t: ` ${money(spend('month'))} month`, dim: true }],
      ],
      inner - 1,
    );
  // As rows run out the summary shrinks, keeping the connection, the count, the spend,
  // the price and a stale warning longest. Nothing is said twice.
  const tierFor = (n: number): Seg[][] => {
    if (n <= 1) return [compactLine];
    if (n === 2) return [head(true), money2(head(true))];
    return [head(false), money2(head(false)), countsLine, whereLine];
  };

  const rows = Math.max(1, height - 2);
  // Focused, the panel is a list of what needs attention under as much of the summary
  // as fits; otherwise it is the summary alone.
  // The list is sized to what it holds, an empty one being a single line, and the
  // summary gets everything else, down to a few lines.
  // The label sits on the list only when there is room; otherwise the border says it
  // (`1 of 7 need attention`), and at least one selectable line is always kept.
  const header = needs.length > 0 && rows >= 8 ? 1 : 0;
  const needed = needs.length === 0 ? 1 : header + needs.length;
  const listArea = focused ? Math.min(needed, Math.max(header + 1, rows >= 8 ? rows - 3 : rows - 2)) : 0;
  const summaryRows = focused ? rows - listArea : rows;
  const lines = summaryRows >= 5 ? fullLines : tierFor(summaryRows);
  const listRows = listArea - header;
  const sel = Math.min(selected, Math.max(0, needs.length - 1));
  const start = windowStart(sel, needs.length, Math.max(1, listRows));
  const shown = needs.slice(start, start + listRows);
  const nameW = Math.min(24, Math.max(4, ...shown.map((n) => [...keyLabel(n.key)].length)));

  return (
    <Frame
      title="[1] Status"
      count={urgent > 0 ? `▲${urgent}` : undefined}
      position={focused && needs.length > 0 ? `${sel + 1} of ${needs.length} need attention` : needs.length > 0 ? `${needs.length} to look at` : undefined}
      focused={focused}
      width={width}
      height={height}
    >
      {lines.slice(0, summaryRows).map((segs, i) => (
        <Row key={i} left={[{ t: ' ' }, ...segs]} width={inner} />
      ))}
      {focused && listRows > 0 ? (
        <>
          {header > 0 ? (
            <Text dimColor bold wrap="truncate-end">
              {' '}NEEDS ATTENTION ({needs.length})
            </Text>
          ) : null}
          {needs.length === 0 ? (
            keys.some((k) => k.status === 'active') ? (
              <Text color={color.ok}> ✔ every key is capped and current</Text>
            ) : (
              <Text dimColor> no active keys</Text>
            )
          ) : (
            shown.map(({ key, risks, top }, i) => (
              <Row
                key={key.id}
                width={inner}
                selected={start + i === sel}
                focused
                left={[
                  { t: ' ' },
                  { t: severityGlyph[top], color: severityColor[top], dim: top === 'info' },
                  { t: ` ${padEnd(keyLabel(key), nameW)}` },
                ]}
                right={[
                  { t: topRisk(risks)!.short, color: severityColor[top], dim: top === 'info' },
                  { t: risks.length > 1 ? ` +${risks.length - 1} ` : ' ', dim: true },
                ]}
              />
            ))
          )}
        </>
      ) : null}
    </Frame>
  );
}

// ---- Keys -----------------------------------------------------------------

export function KeysPanel({
  width,
  height,
  focused,
  keys,
  total,
  selectedId,
  filter,
  filtering,
  now,
}: PanelProps & { keys: Key[]; total: number; selectedId?: string; filter: string; filtering: boolean; now: number }) {
  const inner = width - 2;
  const sel = Math.max(0, keys.findIndex((k) => k.id === selectedId));
  const showFilter = focused && (filtering || filter !== '');
  // The filter takes the last row, so a list that is empty still shows its message above it.
  const rows = Math.max(0, height - 2 - (showFilter ? 1 : 0));
  const { shown } = windowed(keys, sel, Math.max(1, rows));
  const filtered = filter !== '';
  return (
    <Frame
      title="[2] Keys"
      count={filtered ? `(${keys.length}/${total})` : `(${total})`}
      position={position(sel, keys.length)}
      focused={focused}
      tone={filtering ? color.accent : undefined}
      width={width}
      height={height}
    >
      {keys.length === 0 && rows > 0 ? (
        <Text dimColor wrap="truncate-end">
          {' '}
          {total === 0 ? 'No keys yet. n creates one.' : 'No key matches.'}
        </Text>
      ) : null}
      {(rows > 0 ? shown : []).map((k) => {
        const risks = keyRisks(k, now);
        const risk = lapsed0(k) ? undefined : topRisk(risks);
        const top = risk?.severity;
        const today = k.spend?.today;
        const ratio = capRatio(today);
        const gone = k.status !== 'active' || k.expired;
        const lapsed = k.expired && k.status === 'active';
        const s = statusStyle[k.status];
        const label = keyLabel(k);
        // The mark and its word come first and are never dropped for the spend.
        const word = lapsed ? 'expired' : k.status;
        const statusPart: Seg[] = [
          { t: s.glyph, color: lapsed ? color.danger : s.color, dim: s.dim },
          { t: ` ${word}`, color: lapsed ? color.danger : undefined, dim: !lapsed },
        ];
        const riskPart: Seg[][] = risk && top ? [[{ t: `${severityGlyph[top]} ${risk.short}`, color: severityColor[top], dim: top === 'info' }]] : [];
        const spendPart: Seg[][] =
          !gone && today
            ? [[{ t: money(today.cost_micro), color: ratio === undefined ? undefined : ratioColor(ratio), dim: ratio === undefined || ratio < 0.8 }]]
            : [];
        const nameLen = [...label].length;
        // The mark, its word and the warning are never dropped for the spend: if all
        // three cannot sit beside a reasonable part of the name, the spend goes and
        // the name is cut shorter.
        const roomy = fitParts([statusPart, ...riskPart, ...spendPart], Math.max(0, inner - 2 - Math.min(nameLen, 14) - 1));
        const right =
          riskPart.length === 0 || segLen(roomy) >= segLen(statusPart) + 1 + segLen(riskPart[0]!)
            ? roomy
            : fitParts([statusPart, ...riskPart], Math.max(0, inner - 2 - Math.min(nameLen, 6) - 1));
        return (
          <Row
            key={k.id}
            left={[{ t: ' ' }, { t: label, dim: k.status === 'revoked' }]}
            right={[...right, { t: ' ' }]}
            width={inner}
            selected={k.id === selectedId}
            focused={focused}
          />
        );
      })}
      {showFilter ? (
        <Text wrap="truncate-end">
          <Text color={color.accent}> / </Text>
          <Text>{tailCells(filter, filtering ? inner - 5 : inner - 3 - 12)}</Text>
          {filtering ? <Text inverse> </Text> : <Text dimColor>  Esc clears</Text>}
        </Text>
      ) : null}
    </Frame>
  );
}

// ---- Upstreams ------------------------------------------------------------

export function UpstreamsPanel({
  width,
  height,
  focused,
  upstreams,
  keys,
  selected,
}: PanelProps & { upstreams: Upstream[]; keys: Key[]; selected: number }) {
  const inner = width - 2;
  const sel = Math.min(selected, Math.max(0, upstreams.length - 1));
  const rows = Math.max(1, height - 2);
  const { shown } = windowed(upstreams, sel, rows);
  const live = (u: Upstream) => keys.filter((k) => k.upstream === u.name && k.status === 'active').length;
  return (
    <Frame
      title="[3] Upstreams"
      count={`(${upstreams.length})`}
      position={position(sel, upstreams.length)}
      focused={focused}
      width={width}
      height={height}
    >
      {upstreams.length === 0 ? (
        <Text dimColor wrap="truncate-end">
          {' '}
          None yet: kman upstream add
        </Text>
      ) : null}
      {shown.map((u) => (
        <Row
          key={u.id}
          width={inner}
          selected={u === upstreams[sel]}
          focused={focused}
          left={[{ t: ' ' }, { t: u.name }]}
          right={[
            ...fitParts(
              [
                ...(u.credentials.length === 0 ? [[{ t: '▲ no credential', color: color.warn }]] : []),
                ...(u.update_available ? [[{ t: '▲ update', color: color.warn }]] : []),
                [{ t: `${live(u)} active`, dim: true }],
              ],
              Math.max(0, inner - 2 - Math.min([...u.name].length, 10) - 1),
            ),
            { t: ' ' },
          ]}
        />
      ))}
    </Frame>
  );
}

// ---- Audit ----------------------------------------------------------------

export function AuditPanel({
  width,
  height,
  focused,
  audit,
  names,
  fresh,
  selected,
  now,
}: PanelProps & { audit: AuditEntry[]; names: Names; fresh: ReadonlySet<number>; selected: number; now: number }) {
  const inner = width - 2;
  const sel = Math.min(selected, Math.max(0, audit.length - 1));
  const rows = Math.max(1, height - 2);
  const { shown } = windowed(audit, sel, rows);
  return (
    <Frame
      title="[4] Audit"
      count={`(${audit.length})`}
      position={position(sel, audit.length)}
      focused={focused}
      width={width}
      height={height}
    >
      {audit.length === 0 ? (
        <Text dimColor wrap="truncate-end">
          {' '}
          The audit log is empty.
        </Text>
      ) : null}
      {shown.map((e) => (
        <Row
          key={e.id}
          width={inner}
          selected={e === audit[sel]}
          focused={focused}
          left={[
            { t: fresh.has(e.id) ? '●' : ' ', color: color.accent },
            { t: padEnd(clock(e.time, now), 12), dim: true },
            { t: padEnd(e.action, 17), color: actionColor(e.action) },
            { t: targetText(e, names), dim: true },
          ]}
        />
      ))}
    </Frame>
  );
}
