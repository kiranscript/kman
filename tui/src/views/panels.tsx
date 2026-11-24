// The panels down the left side: Status, Keys, Upstreams and Audit. Each is a
// Frame with a list in it. The focused one is tall and shows its selected line as a
// blue bar; the others shrink until only their selected line shows.
import { Text } from 'ink';
import type { AuditEntry, Key, PriceDataStatus, Upstream } from '../api.js';
import { clock, fitMiddle, money, padEnd, since, tailCells } from '../format.js';
import { keyLabel } from '../keys.js';
import { keyRisks, topRisk } from '../risk.js';
import type { Attention, Severity, Risk, UpstreamAttention } from '../risk.js';
import { linkBadge, useTick } from '../ui/chrome.js';
import { Frame, Strip, stripLabelWidth, subtitleFits } from '../ui/frame.js';
import type { ReactNode } from 'react';
import { Row } from '../ui/row.js';
import type { Seg } from '../ui/row.js';
import { color, ratioColor, severityColor, severityGlyph, statusStyle } from '../ui/theme.js';
import { capRatio } from '../risk.js';
import type { Link } from '../useAdmin.js';
import { actionColor, rowTarget } from './audit.js';
import type { Names } from './audit.js';
import { windowStart } from '../ui/parts.js';

interface PanelProps {
  width: number;
  height: number;
  focused: boolean;
}

interface ShellProps {
  title: string;
  count?: string;
  position?: string;
  subtitle?: string;
  focused: boolean;
  tone?: string;
  width: number;
  height: number;
  children: ReactNode;
}

/** A bordered panel, or, one row high, a borderless strip: the same rows either way. */
function Shell({ children, ...p }: ShellProps) {
  if (p.height <= 1) {
    return (
      <Strip title={p.title} count={p.count} width={p.width}>
        {children}
      </Strip>
    );
  }
  return <Frame {...p}>{children}</Frame>;
}

/** The columns a panel's rows have: inside the border, or after a strip's label. */
const innerWidth = (width: number, height: number, title: string, count?: string) =>
  height <= 1 ? width - stripLabelWidth(title, count) : width - 2;

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

/** The segments padded with spaces to `w` columns. */
const pad = (segs: Seg[], w: number): Seg[] => [...segs, { t: ' '.repeat(Math.max(0, w - segLen(segs))) }];
/** The segments right-aligned in `w` columns. */
const padStartSegs = (segs: Seg[], w: number): Seg[] => [{ t: ' '.repeat(Math.max(0, w - segLen(segs))) }, ...segs];

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

/** The address without the scheme a local admin API always has: "127.0.0.1:8081". https stays, as it says something. */
const shortUrl = (url: string) => url.replace(/^http:\/\//, '').replace(/\/$/, '');

export interface Item {
  id: string;
  name: string;
  risks: Risk[];
  top: Severity;
  /** Exactly one of these: what the row is. */
  key?: Key;
  upstream?: Upstream;
  /** Nothing to act on yet: an install with no upstream is told so here. */
  noUpstream?: boolean;
}

const severityOrder: Record<Severity, number> = { danger: 0, warn: 1, info: 2 };
const weightOf = (i: Item) => topRisk(i.risks)?.weight ?? 0;

/** Everything that needs a look as one list, worst first: a key and an upstream rank alike. */
export function attentionItems(needs: Attention[], ups: UpstreamAttention[], upstreamCount = 1): Item[] {
  const setup: Item[] =
    upstreamCount === 0
      ? [
          {
            id: 'setup:upstream',
            name: 'no upstream',
            risks: [{ severity: 'danger', label: 'no upstream: add one with kman upstream add', short: 'kman upstream add', weight: 3000 }],
            top: 'danger',
            noUpstream: true,
          },
        ]
      : [];
  const all: Item[] = [
    ...setup,
    ...needs.map((n) => ({ id: n.key.id, name: keyLabel(n.key), risks: n.risks, top: n.top, key: n.key })),
    ...ups.map((u) => ({ id: `upstream:${u.upstream.id}`, name: u.upstream.name, risks: u.risks, top: u.top, upstream: u.upstream })),
  ];
  // The sort is stable: keys already came worst first, and stay in that order among equals.
  return all.sort((a, b) => severityOrder[a.top] - severityOrder[b.top] || weightOf(b) - weightOf(a));
}

/** Status: the summary of the install and, when focused, what needs attention. */
export function StatusPanel({
  width,
  height,
  focused,
  link,
  url,
  actor,
  keys,
  items,
  selected,
  price,
  upstreamCount,
}: PanelProps & {
  link: Link;
  url: string;
  actor?: string;
  keys: Key[];
  items: Item[];
  selected: number;
  price?: PriceDataStatus;
  upstreamCount: number;
}) {
  useTick(1000);
  const badge = linkBadge(link);
  const live = keys.filter((k) => k.status === 'active').length;
  const disabled = keys.filter((k) => k.status === 'disabled').length;
  const revoked = keys.filter((k) => k.status === 'revoked').length;
  const spend = (which: 'today' | 'month') => keys.reduce((n, k) => n + (k.spend?.[which].cost_micro ?? 0), 0);
  const title = '[1] Status';
  // How many need a look, by how much: "✖1 ▲3 ○1". The border counts the same rows.
  const count =
    (['danger', 'warn', 'info'] as const)
      .map((sev) => ({ sev, n: items.filter((i) => i.top === sev).length }))
      .filter((c) => c.n > 0)
      .map((c) => `${severityGlyph[c.sev]}${c.n}`)
      .join(' ') || undefined;
  const strip = height <= 1;
  const inner = innerWidth(width, height, title, count);
  const lead: Seg[] = strip ? [] : [{ t: ' ' }];
  // Who this is and where: in the top border when it fits, else a line of the summary.
  const where = `${actor ? `${actor} @ ` : ''}${shortUrl(url)}`;
  const inBorder = !strip && subtitleFits(where, title, count, width);

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
  const dot: Seg = { t: '·', dim: true };
  const whereSeg: Seg[] = [dot, { t: ' ' + where, dim: true }];
  const priceOk = price && !price.stale ? [[dot, { t: ' ' }, ...priceSeg(true)]] : [];
  const priceStale = price?.stale ? [[dot, { t: ' ' }, ...priceSeg(true)]] : [];
  // One line that carries the connection, the count, the spend, who and where, and the
  // price: the summary when there is room for only one line. What does not fit is dropped
  // from the end.
  const compactLine: Seg[] = fitParts(
    [
      [badgeSeg],
      ...priceStale,
      [dot, { t: ` ${live} active`, color: live > 0 ? color.ok : undefined }],
      [dot, { t: ` ${money(spend('today'))} today`, bold: true }],
      ...(inBorder ? [] : [whereSeg]),
      ...priceOk,
    ],
    inner - 1,
  );
  const whereLine: Seg[] = [{ t: where, dim: true }];
  const countsLine: Seg[] = [
    { t: `${live} active`, color: live > 0 ? color.ok : undefined },
    { t: ` · ${disabled} disabled · ${revoked} revoked`, dim: true },
  ];
  const fullLines: Seg[][] = [
    [badgeSeg, { t: linkText ? ` · ${linkText}` : '', dim: true }],
    ...(inBorder ? [] : [whereLine]),
    countsLine,
    [
      { t: `${money(spend('today'))} today`, bold: true },
      { t: ` · ${money(spend('month'))} this month`, dim: true },
    ],
    priceSeg(false),
  ];
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
    return [head(false), money2(head(false)), countsLine, ...(inBorder ? [] : [whereLine])];
  };

  const rows = Math.max(1, height - 2);
  // Focused, the panel is a list of what needs attention under as much of the summary
  // as fits; otherwise it is the summary alone.
  // The list is sized to what it holds, an empty one being a single line, and the
  // summary gets everything else, down to a few lines.
  // The label sits on the list only when there is room; otherwise the border says it
  // (`1 of 7 need attention`), and at least one selectable line is always kept.
  const header = items.length > 0 && rows >= 8 ? 1 : 0;
  const needed = items.length === 0 ? 1 : header + items.length;
  const listArea = focused ? Math.min(needed, Math.max(header + 1, rows >= 8 ? rows - 3 : rows - 2)) : 0;
  const summaryRows = focused ? rows - listArea : rows;
  const lines = summaryRows >= 5 ? fullLines : tierFor(summaryRows);
  const listRows = listArea - header;
  const sel = Math.min(selected, Math.max(0, items.length - 1));
  const start = windowStart(sel, items.length, Math.max(1, listRows));
  const shown = items.slice(start, start + listRows);
  const nameW = Math.min(24, Math.max(4, ...shown.map((n) => [...n.name].length)));

  return (
    <Shell
      title={title}
      count={count}
      subtitle={inBorder ? where : undefined}
      position={
        focused && items.length > 0 ? `${sel + 1} of ${items.length}` : items.length > 0 ? `${items.length} to review` : undefined
      }
      focused={focused}
      width={width}
      height={height}
    >
      {lines.slice(0, summaryRows).map((segs, i) => (
        <Row key={i} left={[...lead, ...segs]} width={inner} />
      ))}
      {focused && listRows > 0 ? (
        <>
          {header > 0 ? (
            <Text dimColor bold wrap="truncate-end">
              {' '}NEEDS ATTENTION ({items.length})
            </Text>
          ) : null}
          {items.length === 0 ? (
            keys.some((k) => k.status === 'active') ? (
              <Text color={color.ok}> ✔ every key is capped and current</Text>
            ) : (
              <Text dimColor wrap="truncate-end">
                {keys.length > 0 ? ' no active keys: n creates one' : upstreamCount === 0 ? ' no upstream yet: kman upstream add' : ' no keys yet: n creates one'}
              </Text>
            )
          ) : (
            shown.map(({ id, name, risks, top }, i) => (
              <Row
                key={id}
                width={inner}
                selected={start + i === sel}
                focused
                left={[
                  { t: ' ' },
                  { t: severityGlyph[top], color: severityColor[top], dim: top === 'info' },
                  { t: ` ${padEnd(name, nameW)}` },
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
    </Shell>
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
  hasUpstreams,
  now,
}: PanelProps & {
  keys: Key[];
  total: number;
  selectedId?: string;
  filter: string;
  filtering: boolean;
  hasUpstreams: boolean;
  now: number;
}) {
  const title = '[2] Keys';
  const filtered = filter !== '';
  const count = filtered ? `(${keys.length}/${total})` : `(${total})`;
  const inner = innerWidth(width, height, title, count);
  const lead: Seg[] = height <= 1 ? [] : [{ t: ' ' }];
  const sel = Math.max(0, keys.findIndex((k) => k.id === selectedId));
  const showFilter = focused && (filtering || filter !== '');
  // The filter takes the last row, so a list that is empty still shows its message above it.
  const rows = height <= 1 ? 1 : Math.max(0, height - 2 - (showFilter ? 1 : 0));
  const { shown } = windowed(keys, sel, Math.max(1, rows));
  /** What a key's row says on its right: the mark and word, the warning, today's spend. */
  const parts = (k: Key) => {
    const risks = keyRisks(k, now);
    const risk = lapsed0(k) ? undefined : topRisk(risks);
    const today = k.spend?.today;
    const ratio = capRatio(today);
    const gone = k.status !== 'active' || k.expired;
    const lapsed = k.expired && k.status === 'active';
    const st = statusStyle[k.status];
    const status: Seg[] = [
      { t: lapsed ? '✖' : st.glyph, color: lapsed ? color.danger : st.color, dim: st.dim },
      { t: ` ${lapsed ? 'expired' : k.status}`, color: lapsed ? color.danger : undefined, dim: !lapsed },
    ];
    const warn: Seg[] = risk ? [{ t: `${severityGlyph[risk.severity]} ${risk.short}`, color: severityColor[risk.severity], dim: risk.severity === 'info' }] : [];
    const spend: Seg[] =
      !gone && today
        ? [{ t: money(today.cost_micro), color: ratio === undefined ? undefined : ratioColor(ratio), dim: ratio === undefined || ratio < 0.8 }]
        : [];
    return { label: keyLabel(k), status, risk: warn, spend };
  };
  const rowsInfo = (rows > 0 ? shown : []).map(parts);
  const cols = {
    s: Math.max(0, ...rowsInfo.map((p) => segLen(p.status))),
    r: Math.max(0, ...rowsInfo.map((p) => segLen(p.risk))),
    c: Math.max(0, ...rowsInfo.map((p) => segLen(p.spend))),
    mode: 'tight' as 'all' | 'noSpend' | 'tight',
  };
  {
    const room = inner - 4 - Math.min(Math.max(0, ...rowsInfo.map((p) => [...p.label].length)), 20);
    const base = cols.s + (cols.r ? 1 + cols.r : 0);
    if (base + (cols.c ? 1 + cols.c : 0) <= room) cols.mode = 'all';
    else if (base <= room) cols.mode = 'noSpend';
  }
  return (
    <Shell
      title={title}
      count={count}
      position={position(sel, keys.length)}
      focused={focused}
      tone={filtering ? color.accent : undefined}
      width={width}
      height={height}
    >
      {/* With a filter the title says 0 of n and the detail says why: not three times. */}
      {keys.length === 0 && rows > 0 && !filtered ? (
        <Text dimColor wrap="truncate-end">
          {lead.length ? ' ' : ''}
          {hasUpstreams ? 'No keys yet. n creates one.' : 'No keys: add an upstream first'}
        </Text>
      ) : null}
      {(rows > 0 ? shown : []).map((k) => {
        const p = parts(k);
        const nameLen = [...p.label].length;
        // Aligned columns when they fit beside a reasonable part of the names; if not the
        // spend goes, then the alignment: the mark, its word and the warning are never dropped.
        const right: Seg[] =
          cols.mode === 'all'
            ? [
                ...pad(p.status, cols.s),
                ...(cols.r ? [{ t: ' ' }, ...pad(p.risk, cols.r)] : []),
                ...(cols.c ? [{ t: ' ' }, ...padStartSegs(p.spend, cols.c)] : []),
              ]
            : cols.mode === 'noSpend'
              ? [...pad(p.status, cols.s), ...(cols.r ? [{ t: ' ' }, ...pad(p.risk, cols.r)] : [])]
              : fitParts([p.status, ...(p.risk.length ? [p.risk] : [])], Math.max(0, inner - 2 - Math.min(nameLen, 6) - 1));
        return (
          <Row
            key={k.id}
            left={[...lead, { t: fitMiddle(p.label, Math.max(6, inner - 2 - segLen(right) - 2)), dim: k.status === 'revoked' }]}
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
    </Shell>
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
  const title = '[3] Upstreams';
  const count = `(${upstreams.length})`;
  const inner = innerWidth(width, height, title, count);
  const lead: Seg[] = height <= 1 ? [] : [{ t: ' ' }];
  const sel = Math.min(selected, Math.max(0, upstreams.length - 1));
  const rows = Math.max(1, height - 2);
  const { shown } = windowed(upstreams, sel, rows);
  const live = (u: Upstream) => keys.filter((k) => k.upstream === u.name && k.status === 'active').length;
  return (
    <Shell
      title={title}
      count={count}
      position={position(sel, upstreams.length)}
      focused={focused}
      width={width}
      height={height}
    >
      {upstreams.length === 0 ? (
        <Text dimColor wrap="truncate-end">
          {lead.length ? ' ' : ''}
          None yet: kman upstream add
        </Text>
      ) : null}
      {shown.map((u) => (
        <Row
          key={u.id}
          width={inner}
          selected={u === upstreams[sel]}
          focused={focused}
          left={[...lead, { t: u.name }]}
          right={[
            // The warnings are glyphs when the name needs the room: ▲ no credential, ▲ http.
            ...fitParts(
              [
                ...(u.credentials.length === 0 ? [[{ t: '▲ no credential', color: color.warn }]] : []),
                ...(u.tls === false ? [[{ t: '▲ http', color: color.warn }]] : []),
                ...(u.update_available ? [[{ t: '▲ update', color: color.warn }]] : []),
                [{ t: `${live(u)} active`, dim: true }],
              ],
              Math.max(0, inner - 2 - Math.min([...u.name].length, 14) - 1),
            ),
            { t: ' ' },
          ]}
        />
      ))}
    </Shell>
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
  const title = '[4] Audit';
  const count = `(${audit.length})`;
  const lead = height <= 1 ? '' : ' ';
  const sel = Math.min(selected, Math.max(0, audit.length - 1));
  const rows = Math.max(1, height - 2);
  const { shown } = windowed(audit, sel, rows);
  // The columns are as wide as what is shown needs, so the target, which is what tells
  // entries apart, keeps the rest. The action already names the kind, so only a name follows.
  const timeW = Math.max(...shown.map((e) => [...clock(e.time, now)].length), 0) + 1;
  const actionW = Math.max(...shown.map((e) => [...e.action].length), 0) + 1;
  return (
    <Shell
      title={title}
      count={count}
      position={position(sel, audit.length)}
      focused={focused}
      width={width}
      height={height}
    >
      {audit.length === 0 ? (
        <Text dimColor wrap="truncate-end">
          {lead}The audit log is empty: nothing yet.
        </Text>
      ) : null}
      {shown.map((e) => (
        <Row
          key={e.id}
          width={innerWidth(width, height, title, count)}
          selected={e === audit[sel]}
          focused={focused}
          left={[
            { t: fresh.has(e.id) ? '◆' : lead, color: color.accent },
            { t: padEnd(clock(e.time, now), timeW), dim: true },
            { t: padEnd(e.action, actionW), color: actionColor(e.action) },
            { t: rowTarget(e, names) },
          ]}
        />
      ))}
    </Shell>
  );
}
