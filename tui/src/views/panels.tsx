// The panels down the left side: Status, Keys, Upstreams and Audit. Each is a
// Frame with a list in it. The focused one is tall and shows its selected line as a
// blue bar; the others shrink until only their selected line shows.
import { Box, Text } from 'ink';
import type { AuditEntry, Incident, Key, PriceDataStatus, Upstream } from '../api.js';
import { clock, fitMiddle, padEnd, plural, since, tailCells, textWidth } from '../format.js';
import { keyLabel } from '../keys.js';
import { compareTop, incidentRisk, keyRisks, topRisk, unknowns, upstreamRisks } from '../risk.js';
import { hasFilterRow, listArea } from '../layout.js';
import type { Attention, Severity, Risk, Unknown, UpstreamAttention, Watch } from '../risk.js';
import { linkBadge, useTick } from '../ui/chrome.js';
import { Frame, Strip, stripLabelWidth, subtitleFits, subtitleWidth } from '../ui/frame.js';
import type { ReactNode } from 'react';
import { Row } from '../ui/row.js';
import type { Seg } from '../ui/row.js';
import { color, okGlyph, severityColor, severityGlyph, statusStyle } from '../ui/theme.js';
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

// "1 of 1" says nothing, so a list of one has no position.
const position = (selected: number, count: number) => (count > 1 ? `${selected + 1} of ${count}` : undefined);

// ---- Status ---------------------------------------------------------------

const segLen = (segs: Seg[]) => segs.reduce((n, x) => n + textWidth(x.t), 0);

/** The segments padded with spaces to `w` columns. */
const pad = (segs: Seg[], w: number): Seg[] => [...segs, { t: ' '.repeat(Math.max(0, w - segLen(segs))) }];

/** Between two parts of a summary line. */
const SEPARATOR: Seg = { t: '·', dim: true };

/**
 * The segments without a separator at the start, and the space after it: a line has none
 * to open it. It is told by identity, as the mark for a note is the same dot and stays.
 */
function withoutLeadingSeparator(segs: Seg[]): Seg[] {
  if (segs[0] !== SEPARATOR) return segs;
  const rest = segs.slice(1);
  while (rest[0] && rest[0].t.trim() === '') rest.shift();
  if (rest[0]) rest[0] = { ...rest[0], t: rest[0].t.trimStart() };
  return rest;
}

/** The parts that fit in `max` columns, in order of priority, one space between. */
function fitParts(parts: Seg[][], max: number): Seg[] {
  const out: Seg[] = [];
  let used = 0;
  for (const whole of parts) {
    // What opens the line takes no room for a separator.
    const part = out.length ? whole : withoutLeadingSeparator(whole);
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

const STATUS_TITLE = '[1] Status';

/** How wide Status must be for its top border to carry the address. */
export const addressWidth = (url: string) => subtitleWidth(shortUrl(url), STATUS_TITLE);

/**
 * "owner @ address" in `room` cells. The scheme goes first, then the owner and the host
 * are each cut in the middle, the host keeping the larger share: it says which install.
 */
function whoWhere(actor: string | undefined, address: string, room: number): string {
  const whole = actor ? `${actor} @ ${address}` : address;
  if (textWidth(whole) <= room) return whole;
  const host = address.replace(/^https:\/\//, '');
  if (!actor) return fitMiddle(host, room);
  const both = room - 3;
  const forHost = Math.min(textWidth(host), Math.max(Math.ceil(both * 0.65), both - textWidth(actor)));
  return `${fitMiddle(actor, both - forHost)} @ ${fitMiddle(host, forHost)}`;
}

/**
 * As many of `lines` as there are `rows`, in their order. Those in `spare` give up their
 * row first, in that order, and then the last that `must` does not hold.
 */
function intoRows(lines: Seg[][], rows: number, spare: (Seg[] | undefined)[], must: (line: Seg[]) => boolean): Seg[][] {
  let out = lines;
  for (const line of spare) if (out.length > rows) out = out.filter((l) => l !== line);
  while (out.length > Math.max(0, rows)) {
    let at = out.length - 1;
    while (at >= 0 && must(out[at]!)) at--;
    const gone = out[at < 0 ? out.length - 1 : at];
    out = out.filter((l) => l !== gone);
  }
  return out;
}

export interface Item {
  id: string;
  name: string;
  risks: Risk[];
  top: Severity;
  /** Exactly one of these: what the row is. */
  key?: Key;
  upstream?: Upstream;
  /** An incident about a key that is not in the key list: a canary. */
  incident?: Incident;
  /** Incidents or exposure that did not load. */
  unknown?: Unknown;
  /** Nothing to act on yet: an install with no upstream is told so here. */
  noUpstream?: boolean;
}

/** Everything that needs a look as one list, worst first: a key and an upstream rank alike. */
export function attentionItems(needs: Attention[], ups: UpstreamAttention[], upstreamCount = 1, canaries: Incident[] = [], missing: Unknown[] = []): Item[] {
  const setup: Item[] =
    upstreamCount === 0
      ? [
          {
            id: 'setup:upstream',
            name: 'no upstream',
            risks: [{ severity: 'danger', label: 'no upstream: add one with kman upstream add', short: 'add one', weight: 3000 }],
            top: 'danger',
            noUpstream: true,
          },
        ]
      : [];
  const all: Item[] = [
    ...setup,
    ...missing.map((u) => ({ id: `unknown:${u.what}`, name: u.what, risks: [u.risk], top: u.risk.severity, unknown: u })),
    ...canaries.map((i) => {
      const r = incidentRisk(i);
      return { id: `incident:${i.id}`, name: i.key_name || i.key_id, risks: [r], top: r.severity, incident: i };
    }),
    ...needs.map((n) => ({ id: n.key.id, name: keyLabel(n.key), risks: n.risks, top: n.top, key: n.key })),
    ...ups.map((u) => ({ id: `upstream:${u.upstream.id}`, name: u.upstream.name, risks: u.risks, top: u.top, upstream: u.upstream })),
  ];
  // The sort is stable: keys already came worst first, and stay in that order among equals.
  return all.sort(compareTop);
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
  watch = {},
}: PanelProps & {
  link: Link;
  url: string;
  actor?: string;
  keys: Key[];
  items: Item[];
  selected: number;
  price?: PriceDataStatus;
  upstreamCount: number;
  watch?: Watch;
}) {
  useTick(1000);
  const badge = linkBadge(link);
  const live = keys.filter((k) => k.status === 'active').length;
  const disabled = keys.filter((k) => k.status === 'disabled').length;
  const revoked = keys.filter((k) => k.status === 'revoked').length;
  // Who and where comes before the money: the open incidents, then the keys that are easy to abuse.
  const unresolved = (watch.incidents ?? []).filter((i) => i.state !== 'resolved');
  const open = unresolved.length;
  // Red only while one of them is: an acknowledged or a lesser incident is amber, as in the list.
  const openTone: Severity = unresolved.some((i) => incidentRisk(i).severity === 'danger') ? 'danger' : 'warn';
  const missing = unknowns(watch);
  const incidentsMissing = missing.some((u) => u.what === 'incidents');
  const exposed = new Set((watch.findings ?? []).map((f) => f.key_id)).size;
  const title = STATUS_TITLE;
  const strip = height <= 1;
  const urlOnly = shortUrl(url);
  // The incidents, in words. A strip's own line says them, so its label does not too, and
  // how many rows need a look is on the bottom border. Where the words would push the
  // address off the border they give way: a line of the panel says the incidents too.
  const words = !strip && open ? `${plural(open, 'incident')}${incidentsMissing ? ' (old)' : ''}` : undefined;
  const count = words && subtitleFits(urlOnly, title, words, width) === subtitleFits(urlOnly, title, undefined, width) ? words : undefined;
  const inner = innerWidth(width, height, title, count);
  const lead: Seg[] = strip ? [] : [{ t: ' ' }];
  // Who this is and where: in the top border when it fits, else a line of the summary.
  const where = `${actor ? `${actor} @ ` : ''}${shortUrl(url)}`;
  // The whole of it if the border has room, else just the address; the rest is then said
  // by a line of the summary, or else by the bottom border.
  const inBorder = !strip && subtitleFits(where, title, count, width);
  const borderText = inBorder ? where : !strip && subtitleFits(urlOnly, title, count, width) ? urlOnly : undefined;

  const linkText =
    link.state === 'ok'
      ? `updated ${since(link.at, Date.now())}`
      : link.state === 'down' && link.lastOk
        ? `data ${since(link.lastOk, Date.now())}`
        : link.state === 'denied'
          ? link.error
          : '';
  /** `compact` shortens the warning of stale prices. */
  const priceSeg = (compact: boolean): Seg[] =>
    price
      ? price.stale
        ? [{ t: compact ? `${severityGlyph.warn} price stale` : `${severityGlyph.warn} price stale (${price.age_days}d) · upgrade kman`, color: color.warn }]
        : [{ t: `prices ${price.age_days}d old`, dim: true }]
      : [{ t: 'price data unknown', dim: true }];
  const badgeSeg: Seg = { t: `${badge.glyph} ${badge.word}`, color: badge.color, dim: badge.color === undefined };
  // The connection is in the footer: here it is only said when it is not simply live.
  const badgePart: Seg[][] = link.state === 'ok' ? [] : [[badgeSeg]];
  const whereSeg: Seg[] = [SEPARATOR, { t: ' ' + where, dim: true }];
  /** The incidents: how many are open, and never "none" while they did not load. */
  const incidentSegs = (compact: boolean): Seg[] => {
    if (open) {
      const words = `${open} ${compact ? '' : 'open '}${open === 1 ? 'incident' : 'incidents'}${incidentsMissing ? ' (old)' : ''}`;
      return [{ t: `${severityGlyph[openTone]} ${words}`, color: severityColor[openTone], bold: true }];
    }
    if (incidentsMissing) return [{ t: `${severityGlyph.warn} incidents unknown`, color: color.warn, bold: true }];
    // An install with no keys has nothing to watch yet: no line for it.
    if (watch.incidents === undefined || keys.length === 0) return [];
    return [{ t: `${okGlyph} ${compact ? 'no incidents' : 'no open incidents'}`, color: color.ok }];
  };
  const exposureSegs: Seg[] = missing.some((u) => u.what === 'exposure')
    ? [{ t: `${severityGlyph.warn} ${exposed ? `${exposed} exposed (old)` : 'exposure unknown'}`, color: color.warn }]
    : exposed && keys.length > 0
      ? [{ t: `${severityGlyph.warn} ${exposed} exposed`, color: color.warn }]
      : [];
  const watchSegs = (compact: boolean): Seg[] => {
    const inc = incidentSegs(compact);
    if (!inc.length) return exposureSegs;
    return exposureSegs.length ? [...inc, { t: ' ' }, SEPARATOR, { t: ' ' }, ...exposureSegs] : inc;
  };
  const priceOk = price && !price.stale ? [[SEPARATOR, { t: ' ' }, ...priceSeg(true)]] : [];
  const priceStale = price?.stale ? [[SEPARATOR, { t: ' ' }, ...priceSeg(true)]] : [];
  // One line that carries the connection, the count, the incidents, who and where, and the
  // price: the summary when there is room for only one line. What does not fit is dropped
  // from the end.
  const noKeys = keys.length === 0;
  const topItem = items[0];
  const attentionPart: Seg[] | undefined = topItem
    ? [
        // A note's mark is a dot, which already parts it from what is before it: never two.
        ...(topItem.top === 'info' ? [] : [SEPARATOR, { t: ' ' }]),
        { t: severityGlyph[topItem.top], color: severityColor[topItem.top], dim: topItem.top === 'info' },
        // The end of a long name is what tells keys apart.
        { t: ` ${tailCells(topItem.name, 14)} · ${topRisk(topItem.risks)!.short}${items.length > 1 ? ` · +${items.length - 1} more` : ''}`, dim: topItem.top === 'info' },
      ]
    : undefined;
  const buildCompact = (whereAs?: Seg[]): Seg[] =>
    fitParts(
      [
        ...badgePart,
        ...priceStale,
        // The first thing to look at comes ahead of the counts: it cannot be seen from another panel.
        ...(strip && attentionPart ? [attentionPart] : []),
        // An incident, or not knowing of one, comes ahead of the count.
        ...(open || incidentsMissing ? [[SEPARATOR, { t: ' ' }, ...incidentSegs(true)]] : []),
        ...(noKeys
          ? []
          : [
              [SEPARATOR, { t: ` ${live} active`, color: live > 0 ? color.ok : undefined }],
            ]),
        ...(!(open || incidentsMissing) && incidentSegs(true).length ? [[SEPARATOR, { t: ' ' }, ...incidentSegs(true)]] : []),
        ...(exposureSegs.length ? [[SEPARATOR, { t: ' ' }, ...exposureSegs]] : []),
        ...(whereAs ? [whereAs] : []),
        ...priceOk,
      ],
      inner - 1,
    );
  // Who and where, in full if it fits, else just the address, so the line never loses where it is.
  const whereShort: Seg[] = [SEPARATOR, { t: ' ' + urlOnly, dim: true }];
  const withFullWhere = buildCompact(inBorder ? undefined : whereSeg);
  const compactLine: Seg[] =
    inBorder || withFullWhere.some((x) => x.t.includes(where)) ? withFullWhere : buildCompact(whereShort).some((x) => x.t.includes(urlOnly)) ? buildCompact(whereShort) : withFullWhere;
  // When the border carries the address the line says only who: not the address twice.
  // It stops a cell short of the border, as the line starts one in.
  const whereLine: Seg[] = [
    { t: borderText === urlOnly && actor ? fitMiddle(`signed in as ${actor}`, inner - 2) : whoWhere(actor, urlOnly, inner - 2), dim: true },
  ];
  // Zeros tell nothing, and Keys and the detail already say there are no keys: no counts then.
  const countsLine: Seg[] = noKeys
    ? []
    : [
        { t: `${live} active`, color: live > 0 ? color.ok : undefined },
        { t: ` · ${disabled} disabled · ${revoked} revoked`, dim: true },
      ];
  /** The longest of the incidents' wordings that fits a line: never dropped for being long. */
  const watchFitted =
    [watchSegs(false), watchSegs(true), incidentSegs(true)].find((s) => s.length > 0 && segLen(s) <= inner - 1) ?? watchSegs(true);
  // A fresh price joins the first line; a stale or unknown one keeps a line to itself.
  const priceJoins = price !== undefined && !price.stale;
  const fullLines: Seg[][] = [
    // Whole parts only: the price, and then how old the data is, give way to a narrow panel.
    fitParts(
      [
        ...(link.state === 'ok' ? [[{ t: linkText, dim: true }]] : [[badgeSeg], ...(linkText ? [[SEPARATOR, { t: ` ${linkText}`, dim: true }]] : [])]),
        ...(priceJoins ? [[SEPARATOR, { t: ' ' }, ...priceSeg(true)]] : []),
      ],
      inner - 1,
    ),
    countsLine,
    watchFitted,
    // Who and where come after the numbers: the border carries the address when it fits.
    ...(inBorder ? [] : [whereLine]),
    ...(priceJoins ? [] : [priceSeg(false)]),
  ];
  const activePart: Seg[] = noKeys
    ? []
    : [
        SEPARATOR,
        { t: ` ${live} active`, color: live > 0 ? color.ok : undefined },
        { t: ` · ${disabled} disabled · ${revoked} revoked`, dim: true },
      ];
  /** The connection, a stale price, and the count when `withCount`. */
  const head = (withCount: boolean): Seg[] =>
    fitParts([...(link.state === 'ok' ? [[{ t: linkText, dim: true }]] : badgePart), ...priceStale, ...(withCount ? [activePart] : []), ...priceOk], inner - 1);
  /** The incidents, with the price when the line above does not already carry it. */
  const watch2 = (above: Seg[]): Seg[] =>
    fitParts(
      [
        ...(watchFitted.length ? [watchFitted] : []),
        ...(above.some((x) => x.t.includes('price')) ? [] : price && !noKeys ? [[SEPARATOR, { t: ' ' }, ...priceSeg(true)]] : []),
      ],
      inner - 1,
    );
  // As rows run out the summary shrinks, keeping the connection, the count, the incidents,
  // the price and a stale warning longest. Nothing is said twice.
  const tierFor = (n: number): Seg[][] => {
    // With no keys to count, the one row is what is left: updated and the price (and the problem, if stacked short).
    if (n <= 1) return [noKeys && !strip && !open && !incidentsMissing ? head(false) : compactLine];
    if (n === 2) return [head(true), watch2(head(true))];
    return [head(false), countsLine, watch2(head(false)), ...(inBorder ? [] : [whereLine])];
  };

  const rows = Math.max(1, height - 2);
  // Focused, the panel is a list of what needs attention under as much of the summary
  // as fits; otherwise it is the summary alone.
  const list = focused ? listArea(height, { tab: 'status', items: items.length, noKeys }) : { header: 0, rows: 0 };
  const summaryHeight = rows - list.header - list.rows;
  const chosen = (summaryHeight >= fullLines.length ? fullLines : tierFor(summaryHeight)).filter((l) => l.length > 0);
  // The incidents' line says nothing the screen lacks when the title counts them, or there
  // are none, and it carries no warning of exposed keys.
  const isWatch = (l: Seg[]) => watchFitted.length > 0 && l.includes(watchFitted[0]!);
  const watchSpare = exposureSegs.length === 0 && (open ? count !== undefined : !incidentsMissing);
  const staleLine = (l: Seg[]) => l.some((x) => x.t.includes('price stale'));
  // What must keep its row, as far as rows go: the first line, what only a line says, and an
  // address the top border cannot carry.
  const must = (l: Seg[]) => l === chosen[0] || (!borderText && l === whereLine) || (isWatch(l) && !watchSpare) || staleLine(l);
  const spare = (lines: Seg[][]) => {
    const watch = watchSpare ? lines.find(isWatch) : undefined;
    // With incidents open their line is worth more than the counts of keys, which the Keys panel gives too.
    return open ? [countsLine, watch] : [watch, countsLine];
  };
  // Such an address takes a row where one can be had, even one the shorter summaries leave it out of.
  const wanted = !borderText && !strip && summaryHeight >= 2 && !chosen.includes(whereLine) ? [...chosen, whereLine] : chosen;
  const base = borderText || strip ? chosen : intoRows(wanted, summaryHeight, spare(wanted), must);
  // A tall summary with a row to spare says what the list would: the first thing to look at.
  const first = items[0];
  const reason = ` · ${first ? topRisk(first.risks)!.short : ''}${items.length > 1 ? ` · +${items.length - 1} more` : ''}`;
  const attentionLine: Seg[] = first
    ? [
        { t: severityGlyph[first.top], color: severityColor[first.top], dim: first.top === 'info' },
        // The name is cut in the middle, so the end that tells similar keys apart stays beside the reason.
        { t: ` ${fitMiddle(first.name, Math.max(10, inner - 3 - textWidth(reason)))}${reason}`, dim: first.top === 'info' },
      ]
    : [{ t: 'Nothing to review.', dim: true }];
  // Unfocused, the last line of the summary is the first thing to look at, whatever it has to
  // replace: a problem must not be hidden behind the price.
  // A stale price keeps its line, though: it too is something to act on.
  const replaced = (): Seg[][] => {
    const kept = intoRows(base, summaryHeight - 1, spare(base), must);
    const last = kept[kept.length - 1];
    return last && staleLine(last) ? [...kept.slice(0, -1), attentionLine, last] : [...kept, attentionLine];
  };
  const lines =
    !focused && !strip && items.length > 0 && summaryHeight >= 3
      ? replaced()
      : !focused && summaryHeight > base.length && summaryHeight >= fullLines.length
        ? [...base, attentionLine]
        : base;
  const sel = Math.min(selected, Math.max(0, items.length - 1));
  const start = windowStart(sel, items.length, Math.max(1, list.rows));
  const shown = items.slice(start, start + list.rows);
  const nameW = Math.min(24, Math.max(4, ...shown.map((n) => textWidth(n.name))));
  const standing = focused && items.length > 1 ? `${sel + 1} of ${items.length}` : items.length > 0 ? `${items.length} to review` : undefined;
  // What neither the top border nor a row says of who and where rides on the bottom one,
  // beside the position, which always stays whole.
  const visible = lines.slice(0, summaryHeight);
  const rowSays = (text: string) => visible.some((l) => l.some((s) => s.t.includes(text)));
  const bottom = (): string | undefined => {
    if (strip || inBorder) return standing;
    const room = width - 6 - (standing ? textWidth(standing) + 3 : 0);
    // With no address in the top border, the line of who and where holds it, cut or whole.
    const whereShown = visible.includes(whereLine);
    if (borderText || whereShown || rowSays(urlOnly)) {
      const named = !actor || whereShown || rowSays(`${actor} @ `);
      return [named ? undefined : fitMiddle(actor, room), standing].filter(Boolean).join(' · ') || undefined;
    }
    return [whoWhere(actor, urlOnly, room), standing].filter(Boolean).join(' · ');
  };

  return (
    <Shell
      title={title}
      count={count}
      subtitle={borderText}
      position={bottom()}
      focused={focused}
      width={width}
      height={height}
    >
      {lines.slice(0, summaryHeight).map((segs, i) => (
        <Row key={i} left={[...lead, ...withoutLeadingSeparator(segs)]} width={inner} />
      ))}
      {list.rows > 0 ? (
        <>
          {list.header > 0 ? (
            <Text dimColor bold wrap="truncate-end">
              {' '}NEEDS ATTENTION
            </Text>
          ) : null}
          {items.length === 0 ? (
            keys.some((k) => k.status === 'active') ? (
              <Text color={color.ok} wrap="truncate-end"> {okGlyph} nothing to review</Text>
            ) : (
              <Text dimColor wrap="truncate-end">
                {keys.length > 0 ? ' No active keys. n creates one.' : upstreamCount === 0 ? ' No upstream yet: kman upstream add' : ' Nothing to review.'}
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
                  { t: ` ${padEnd(fitMiddle(name, nameW), nameW)}` },
                ]}
                right={[
                  { t: topRisk(risks)!.listLabel ?? topRisk(risks)!.short, color: severityColor[top], dim: top === 'info' },
                  { t: risks.length > 1 ? ` +${risks.length - 1} ${risks.length === 2 ? 'issue' : 'issues'} ` : ' ', dim: true },
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
  all,
  watch = {},
}: PanelProps & {
  keys: Key[];
  /** Incidents and exposure findings, for each row's warning. */
  watch?: Watch;
  /** Every key, filtered or not: the columns are sized by these. */
  all: Key[];
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
  const showFilter = focused && hasFilterRow(filter, filtering);
  // The filter takes the last row, so a list that is empty still shows its message above it.
  const { rows } = listArea(height, { tab: 'keys', filterRow: showFilter });
  const { shown } = windowed(keys, sel, Math.max(1, rows));
  /** What a key's row says on its right: the mark and word, and its worst warning. */
  const parts = (k: Key) => {
    const risks = keyRisks(k, now, watch);
    // An expired key's badge says so; an incident still comes first.
    const risk = topRisk(lapsed0(k) ? risks.filter((r) => r.label !== 'expired') : risks);
    const lapsed = k.expired && k.status === 'active';
    const st = statusStyle[k.status];
    // Active is the usual state and is dim, so that the others stand out from it.
    const usual = k.status === 'active' && !lapsed;
    const tone = lapsed ? color.danger : usual ? st.color : k.status === 'disabled' ? color.warn : undefined;
    const status: Seg[] = [
      { t: lapsed ? severityGlyph.danger : st.glyph, color: tone, dim: usual },
      { t: ` ${lapsed ? 'expired' : k.status}`, color: usual ? undefined : tone, dim: usual },
    ];
    const warn: Seg[] = risk ? [{ t: `${severityGlyph[risk.severity]} ${risk.short}`, color: severityColor[risk.severity], dim: risk.severity === 'info' }] : [];
    return { label: keyLabel(k), status, risk: warn };
  };
  // The columns are as wide as the widest of all keys, not of those shown, so they do not
  // move while filtering or scrolling.
  const rowsInfo = (rows > 0 ? all : []).map(parts);
  const cols = {
    s: Math.max(0, ...rowsInfo.map((p) => segLen(p.status))),
    r: Math.max(0, ...rowsInfo.map((p) => segLen(p.risk))),
    mode: 'tight' as 'all' | 'marks' | 'tight',
  };
  {
    // The names get what they need (up to 20 cells) before a column does; as room runs
    // out the warning's words go (its mark stays), always in columns.
    const longest = Math.max(0, ...rowsInfo.map((p) => textWidth(p.label)));
    // A row is: a space, the name, a space, the columns and a space.
    const base = cols.s + (cols.r ? 1 + cols.r : 0);
    if (base <= inner - 3 - Math.min(longest, 12)) cols.mode = 'all';
    else if (cols.s + (cols.r ? 2 : 0) <= inner - 3 - Math.min(longest, 12)) cols.mode = 'marks';
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
      {/* With a filter the title says 0 of n and the detail says why; the list says it shortly. */}
      {keys.length === 0 && rows > 0 && filtered ? (
        <Text dimColor wrap="truncate-end">
          {lead.length ? ' ' : ''}no match
        </Text>
      ) : null}
      {keys.length === 0 && rows > 0 && !filtered ? (
        <Text dimColor wrap="truncate-end">
          {lead.length ? ' ' : ''}
          {hasUpstreams ? 'No keys yet. n creates one.' : 'No keys: add an upstream first'}
        </Text>
      ) : null}
      {(rows > 0 ? shown : []).map((k) => {
        const p = parts(k);
        const nameLen = textWidth(p.label);
        // Aligned columns when they fit beside a reasonable part of the names; if not the
        // alignment goes: the mark, its word and the warning are never dropped.
        const right: Seg[] =
          cols.mode === 'all'
            ? [...pad(p.status, cols.s), ...(cols.r ? [{ t: ' ' }, ...pad(p.risk, cols.r)] : [])]
            : cols.mode === 'marks'
              ? [
                  ...pad(p.status, cols.s),
                  ...(cols.r ? [{ t: ' ' }, ...pad(p.risk.slice(0, 1).map((x) => ({ ...x, t: [...x.t][0] ?? ' ' })), 1)] : []),
                ]
              : fitParts([p.status, ...(p.risk.length ? [p.risk] : [])], Math.max(0, inner - 2 - Math.min(nameLen, 6) - 1));
        return (
          <Row
            key={k.id}
            left={[...lead, { t: fitMiddle(p.label, Math.max(6, inner - 3 - segLen(right))) }]}
            right={[...right, { t: ' ' }]}
            width={inner}
            selected={k.id === selectedId}
            focused={focused}
          />
        );
      })}
      {/* The prompt is on the last row of the panel, whether or not anything matched. */}
      {showFilter ? <Box flexGrow={1} /> : null}
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
  const { rows } = listArea(height, { tab: 'upstreams' });
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
            // The warnings are glyphs when the name needs the room: ! no credential, ▲ http.
            ...fitParts(
              [
                // The same marks as Status gives them: no credential is !, plain http ▲, an update ·.
                ...upstreamRisks(u).map((r) => [
                  { t: `${severityGlyph[r.severity]} ${r.short === 'plain http' ? 'http' : r.short}`, color: severityColor[r.severity], dim: r.severity === 'info' },
                ]),
                ...(keys.length === 0 ? [] : [[{ t: ` ${live(u)} active`, dim: true }]]),
              ],
              Math.max(0, inner - 2 - Math.min(textWidth(u.name), 14) - 1),
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
  const { rows } = listArea(height, { tab: 'audit' });
  const { shown } = windowed(audit, sel, rows);
  // The columns are as wide as what is shown needs, so the target, which is what tells
  // entries apart, keeps the rest. The action already names the kind, so only a name follows.
  const timeW = Math.max(...shown.map((e) => textWidth(clock(e.time, now))), 0) + 1;
  const actionW = Math.max(...shown.map((e) => textWidth(e.action)), 0) + 1;
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

// ---- Requests -------------------------------------------------------------

/** Whose requests the table shows, and how many of those loaded are flagged. The table is the main panel. */
export function RequestsPanel({
  width,
  height,
  focused,
  keyName,
  danger,
  warn,
  loaded,
  brief,
}: PanelProps & {
  keyName?: string;
  /** In a strip too narrow for more than the counts. */
  brief?: boolean;
  /** Rows marked danger, and warn, among those loaded. */
  danger: number;
  warn: number;
  /** Undefined before anything has been answered. */
  loaded?: number;
}) {
  const title = '[5] Requests';
  const inner = innerWidth(width, height, title);
  const lead: Seg[] = height <= 1 ? [] : [{ t: ' ' }];
  const who: Seg = { t: keyName ?? 'all keys' };
  const counts: Seg[] =
    loaded === undefined
      ? [{ t: 'loading…', dim: true }]
      : [
          ...(danger ? [{ t: `${severityGlyph.danger} ${danger} `, color: severityColor.danger }] : []),
          ...(warn ? [{ t: `${severityGlyph.warn} ${warn} `, color: severityColor.warn }] : []),
          // "of" only follows a count of the flagged.
          { t: `${danger || warn ? 'of ' : ''}${loaded} loaded`, dim: true },
        ];
  return (
    <Shell title={title} focused={focused} width={width} height={height}>
      {height <= 3 ? (
        <Row width={inner} left={brief ? counts : [...lead, who, { t: ' · ', dim: true }, ...counts]} />
      ) : (
        <>
          <Row width={inner} left={[...lead, who]} />
          <Row width={inner} left={[...lead, ...counts]} />
        </>
      )}
    </Shell>
  );
}
