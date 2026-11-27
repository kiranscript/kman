// The main panel, on the right: the details of whatever is selected on the left,
// or, with Status focused, what needs attention and what has just happened.
import { Box, Text, measureElement } from 'ink';
import type { DOMElement } from 'ink';
import { useEffect, useRef, useState } from 'react';
import type { AuditEntry, Incident, Key, Place, PlaceKind, Upstream } from '../api.js';
import { ago, capMoney, clock, dateTime, fit, fitMiddle, money, padEnd, plural, requestsText, shortCount, textWidth, until } from '../format.js';
import { incidentFor } from '../incidents.js';
import { keyLabel } from '../keys.js';
import { incidentRisk, keyRisks, neverExpires, ruleText, unknowns } from '../risk.js';
import type { Risk, Unknown, Watch } from '../risk.js';
import { Frame, titleFits } from '../ui/frame.js';
import { Field, Meter, RiskLine, SectionTitle, StatusBadge, UpstreamSetup, Wrapped } from '../ui/parts.js';
import { color, severityColor, severityGlyph } from '../ui/theme.js';
import { AUDIT_LIMIT } from '../useAdmin.js';
import type { PlacesState } from '../useAdmin.js';
import { actionColor, rowTarget, targetName, targetText } from './audit.js';
import { LastRequests } from './requests.js';
import type { Flagged } from './requests.js';
import type { Names } from './audit.js';

interface Size {
  width: number;
  height: number;
}

/** Text inside a frame, one column in from the border. */
function Body({ children }: { children: React.ReactNode }) {
  return (
    <Box flexDirection="column" paddingX={1} flexShrink={0}>
      {children}
    </Box>
  );
}

/** The text of an empty panel, wrapped by words inside its border; a command under it stays whole on one line. */
function Empty({ text, setup, width }: { text: string; setup?: boolean; width: number }) {
  return (
    <Box flexDirection="column" paddingX={1}>
      <Wrapped text={text} width={width - 4} maxLines={6} dim />
      {setup ? <UpstreamSetup /> : null}
    </Box>
  );
}

// ---- Status: what has just happened ---------------------------------------

/** The main panel with Status focused and nothing needing attention to show. */
export function StatusMain({
  width,
  height,
  audit,
  names,
  now,
  scroll,
}: Size & { audit: AuditEntry[]; names: Names; now: number; scroll: Scroll }) {
  // Nothing here scrolls: say so, so the keys do not offer it.
  const { onMax } = scroll;
  useEffect(() => onMax(0));
  const inner = width - 2;
  const rows = Math.max(1, height - 2);
  const shown = audit.slice(0, rows);
  const targetW = Math.max(6, inner - 1 - 12 - 18 - 1);
  return (
    <Frame
      title="Recent activity"
      position={audit.length > shown.length ? `${audit.length - shown.length} older in [4] Audit` : undefined}
      width={width}
      height={height}
    >
      {audit.length === 0 ? <Text dimColor> Nothing has changed yet.</Text> : null}
      {shown.map((e) => (
        <Text key={e.id} wrap="truncate-end">
          <Text dimColor> {padEnd(clock(e.time, now), 12)}</Text>
          <Text color={actionColor(e.action)}>{padEnd(e.action, 18)}</Text>
          {/* "key.create" already says it is a key, so only the name follows. */}
          <Text> {fit(rowTarget(e, names), targetW)}</Text>
        </Text>
      ))}
    </Frame>
  );
}

// ---- An install with nothing in it yet ---------------------------------------

/** The main panel for an install with no keys: what a key is and how to make one. */
export function StartMain({ width, height, scroll, canCreate }: Size & { scroll: Scroll; canCreate: boolean }) {
  const { onMax } = scroll;
  useEffect(() => onMax(0));
  return (
    <Frame title="Getting started" width={width} height={height}>
      <Box flexDirection="column" paddingX={1}>
        <Text>No keys yet.</Text>
        <Wrapped text="A virtual key is what an app uses in place of your real API key: kman checks it, applies its limits, and forwards the request." width={width - 4} maxLines={6} dim />
        <Box marginTop={1} flexDirection="column">
          <Wrapped text={canCreate ? 'Press n to create the first one.' : 'Add an upstream first:'} width={width - 4} maxLines={3} />
          {canCreate ? null : <UpstreamSetup />}
        </Box>
      </Box>
    </Frame>
  );
}

// ---- A panel whose content scrolls -----------------------------------------

export interface Scroll {
  /** How many lines the content is scrolled up. */
  offset: number;
  /** Told how far the content can scroll, so the keys know where to stop. */
  onMax: (max: number) => void;
  /** The panel has the keyboard (after Enter): its border is the bright one. */
  focused?: boolean;
  /** How many lines one page of the content is, so PgUp and PgDn know. Told by the panel. */
  onView?: (rows: number) => void;
}

/**
 * A Frame for content that may be taller than it. J and K scroll it, and the bottom
 * border says so, so nothing is lost at a small size.
 */
function ScrollFrame({
  title,
  width,
  height,
  scroll,
  children,
}: Size & { title: string; scroll: Scroll; children: React.ReactNode }) {
  const ref = useRef<DOMElement>(null);
  const [content, setContent] = useState(0);
  const view = Math.max(1, height - 2);
  const { onMax, onView } = scroll;
  useEffect(() => {
    onView?.(view);
    if (!ref.current) return;
    const h = measureElement(ref.current).height;
    setContent((c) => (c === h ? c : h));
    onMax(Math.max(0, h - view));
  });
  const max = Math.max(0, content - view);
  const off = Math.min(scroll.offset, max);
  // With the keyboard on the panel the border says where it is, as the lists do ("4-12 of 30");
  // otherwise it says which keys scroll it.
  const hint =
    max === 0
      ? undefined
      : scroll.focused
        ? `${off + 1}-${Math.min(content, off + view)} of ${content}`
        : off === 0
          ? '↓ J more'
          : off >= max
            ? '↑ K back'
            : '↑↓ J K more';
  return (
    <Frame title={title} position={hint} focused={scroll.focused} width={width} height={height}>
      <Box ref={ref} flexDirection="column" flexShrink={0} marginTop={-off}>
        {children}
      </Box>
    </Frame>
  );
}

// ---- Key ------------------------------------------------------------------

/**
 * "today  ████░░░░ $0.9 / $1", with its request count beside it on a narrow screen where
 * there is room, and otherwise under it: nothing is cut for the count.
 */
function SpendRow({ which, k, barW, roomy, width }: { which: 'today' | 'month'; k: Key; barW: number; roomy: boolean; width: number }) {
  const s = k.spend?.[which];
  if (!s) return null;
  const amount = s.cap_micro ? `${money(s.cost_micro)} / ${capMoney(s.cap_micro)}` : `${money(s.cost_micro)}, no cap`;
  const remaining = s.cap_micro && k.status === 'active' ? Math.max(0, s.cap_micro - s.cost_micro - (s.reserved_micro ?? 0)) : undefined;
  const requests = `${requestsText(s.requests)}${s.unmetered_requests ? `, ${s.unmetered_requests} unmetered` : ''}`;
  const spent = `${amount}${remaining === undefined ? '' : ` · ${money(remaining)} left`}`;
  // The frame's two borders and two spaces, the label, the bar and the gaps around them.
  const under = roomy || 6 + barW + 1 + textWidth(spent) + 2 + textWidth(requests) > width - 4;
  return (
    <Box flexDirection="column">
      <Box>
        <Box flexShrink={0}>
          <Text dimColor>{padEnd(which, 6)}</Text>
          <Meter period={s} width={barW} muted={k.status === 'revoked'} />
        </Box>
        <Text wrap="truncate-end">
          {' '}
          {spent}
          {under ? '' : <Text dimColor>{`  ${requests}`}</Text>}
        </Text>
      </Box>
      {under ? (
        <Text dimColor>
          {'      '}
          {requests}
        </Text>
      ) : null}
    </Box>
  );
}

/** A risk, and under it the command that answers it, wrapped: a cut command cannot be run. */
function RiskWithFix({ risk }: { risk: Risk }) {
  return (
    <Box flexDirection="column">
      <RiskLine risk={risk} />
      {risk.fix ? (
        <Box paddingLeft={2}>
          <Text dimColor wrap="wrap">
            {risk.fix}
          </Text>
        </Box>
      ) : null}
    </Box>
  );
}

const placeKinds: { kind: PlaceKind; label: string }[] = [
  { kind: 'country', label: 'country' },
  { kind: 'asn', label: 'ASN' },
  { kind: 'network', label: 'network' },
  { kind: 'client', label: 'client' },
];

/** "AS3320 Deutsche Telekom AG", "DE", "openai-python/3", or "others" for the row past the first 1,000. */
function placeName(p: Place): string {
  if (p.other) return 'others';
  const v = p.kind === 'asn' ? `AS${p.value}` : p.value || 'none';
  return p.label ? `${v} ${p.label}` : v;
}

/**
 * Where a key was used from, a kind to a block, most recently seen first: each place with
 * when it was last seen and how many requests were forwarded and refused from it. A
 * refused request is shown in colour: it is what a locked key turning up elsewhere looks like.
 */
function Places({ state, k, width, perKind, now }: { state: PlacesState | undefined; k: Key; width: number; perKind: number; now: number }) {
  if (!state || (!state.places && !state.error)) return <Text dimColor>loading…</Text>;
  if (!state.places) return <Text dimColor wrap="wrap">{`Could not load: ${state.error}`}</Text>;
  const { places, networks_withheld: withheld } = state.places;
  const groups = placeKinds.map(({ kind, label }) => ({ kind, label, all: places.filter((p) => p.kind === kind) }));
  const more = groups.reduce((n, g) => n + Math.max(0, g.all.length - perKind), 0);
  const rows = groups.flatMap(({ kind, label, all }) =>
    all.slice(0, perKind).map((p, i) => ({
      p,
      kind,
      label: i === 0 ? label : '',
      seen: ago(p.last_seen, now),
      forwarded: p.requests ? ` · ${shortCount(p.requests)} fwd` : '',
      refused: p.refused ? ` · ${shortCount(p.refused)} refused` : '',
    })),
  );
  // The value column is what the longest value needs, beside the longest counts. Where that
  // would leave it too narrow, every row's counts go on a line of their own under it; and a
  // single value too long to stand beside them, such as an AS's organisation, takes its
  // whole line, with its counts under, rather than be cut.
  const room = Math.max(6, width - 12);
  const wide = (r: (typeof rows)[number]) => textWidth(placeName(r.p));
  const counts = Math.max(0, ...rows.map((r) => 1 + textWidth(r.seen + r.forwarded + r.refused)));
  const split = room - counts < Math.min(Math.max(0, ...rows.map(wide)), 24);
  const beside = split ? room : room - counts;
  const under = (r: (typeof rows)[number]) => split || wide(r) > beside;
  const valueW = Math.min(beside, Math.max(0, ...rows.filter((r) => wide(r) <= beside).map(wide)));
  // Left to right the same on one line or two.
  const stats = (r: (typeof rows)[number]) => (
    <Text wrap="truncate-end">
      <Text dimColor>{`${under(r) ? '' : ' '}${r.seen}${r.forwarded}`}</Text>
      {r.refused ? <Text color={color.warn}>{r.refused}</Text> : null}
    </Text>
  );
  // A network's end says how wide it is, so it is cut in the middle.
  const value = (r: (typeof rows)[number]) => {
    const w = under(r) ? room : valueW;
    return r.kind === 'network' ? fitMiddle(placeName(r.p), w) : fit(placeName(r.p), w);
  };
  // An id still names a revoked key, whose name no longer does.
  const ref = k.status === 'revoked' || !k.name ? k.id : k.name;
  return (
    <Box flexDirection="column">
      {state.error ? <Text color={color.warn} wrap="wrap">{`${severityGlyph.warn} not updated: ${state.error}`}</Text> : null}
      {places.length === 0 && !withheld ? <Text dimColor>Not used yet.</Text> : null}
      {rows.map((r) => (
        <Box key={`${r.kind}:${r.p.value}:${r.p.other ? 'o' : ''}`} flexDirection="column">
          <Field label={r.label}>
            <Text>{under(r) ? value(r) : padEnd(value(r), valueW)}</Text>
            {under(r) ? null : stats(r)}
          </Field>
          {under(r) ? <Field label="">{stats(r)}</Field> : null}
        </Box>
      ))}
      {more > 0 ? <Text dimColor wrap="wrap">{`+${more} more: kman key places ${ref}`}</Text> : null}
      {withheld ? <Text dimColor wrap="wrap">networks hidden: kman settings set expose_client_addresses on</Text> : null}
    </Box>
  );
}

/**
 * Every fact about a key, at every size. Beside the list it is laid out with headings;
 * under it (portrait) the same facts are packed tighter, and J and K scroll to the rest.
 */
export function KeyMain({
  keyData: k,
  width,
  height,
  now,
  compact,
  scroll,
  watch,
  places,
  requests,
  none = 'No key selected.',
}: Size & {
  keyData: Key | undefined;
  now: number;
  compact: boolean;
  scroll: Scroll;
  watch?: Watch;
  places?: PlacesState;
  /** Its last few requests; absent on a kman without events. */
  requests?: { rows?: Flagged[]; error?: string };
  none?: string;
}) {
  const { onMax } = scroll;
  useEffect(() => {
    if (!k) onMax(0);
  });
  if (!k) {
    return (
      <Frame title="Key" width={width} height={height}>
        <Empty text={none} width={width} />
      </Frame>
    );
  }
  const incident = incidentFor(watch?.incidents, k.id);
  const incidentMark = incident ? incidentRisk(incident) : undefined;
  // The badge already says "expired", and the head of the detail the incident, so neither
  // risk is said twice.
  const risks = [
    ...keyRisks(k, now, watch).filter((r) => !r.incident && !(k.expired && k.status === 'active' && r.label === 'expired')),
    // Without these, no warning here does not mean there is none.
    ...unknowns(watch ?? {}).map((u) => u.risk),
  ];
  const p = k.policy;
  const gap = compact ? 0 : 1;
  // The title is cut at the panel's width; two names can differ only at the end.
  const label = keyLabel(k);
  const titleClipped = !titleFits(`Key: ${label}`, undefined, width);
  const barW = compact ? 10 : Math.min(14, Math.max(8, width - 4 - 26));
  // An expiry warning already says how long is left, so the line then gives only the date.
  const soon = risks.some((r) => r.label.startsWith('expires'));
  const expires =
    k.status === 'revoked'
      ? 'ended for good'
      : neverExpires(k.expires_at)
        ? 'never expires'
        : `expires ${dateTime(k.expires_at)}${soon ? '' : ` (${until(k.expires_at, now)})`}`;
  return (
    <ScrollFrame title={`Key: ${label}`} width={width} height={height} scroll={scroll}>
      <Body>
        {titleClipped ? (
          <Text bold wrap="wrap">
            {label}
          </Text>
        ) : null}
        {incident && incidentMark ? (
          // First, whatever else there is to say: it is what is to be done about this key.
          <Box flexDirection="column" marginBottom={gap}>
            <Text color={severityColor[incidentMark.severity]} bold wrap="wrap">
              {severityGlyph[incidentMark.severity]} incident #{incident.id} · Enter to answer
            </Text>
            {/* Short of rows, what fired shares the command's line. */}
            <Box paddingLeft={2} flexDirection={compact ? 'row' : 'column'} columnGap={3} flexWrap="wrap">
              <Text dimColor wrap="wrap">
                {incidentMark.fix}
              </Text>
              <Text wrap="wrap">
                {incident.severity}
                {incident.state === 'acknowledged' ? ', acknowledged' : ''}
                {incident.quiet ? ', quiet' : ''}: {incident.rules.map(ruleText).join(', ') || 'a detection'}
              </Text>
            </Box>
          </Box>
        ) : null}
        <Text wrap="wrap">
          <StatusBadge status={k.status} expired={k.expired} />
          <Text dimColor>
            {'  '}
            {label === k.id ? '' : `id ${k.id} · `}
            {k.upstream ?? 'no upstream'}
            {compact ? ` · ${expires}` : ''}
          </Text>
        </Text>
        {compact ? null : (
          <Text dimColor wrap="wrap">
            {expires}
          </Text>
        )}

        {risks.length > 0 ? (
          <Box flexDirection="column" marginTop={gap}>
            {compact ? (
              <Text wrap="wrap">
                {risks.map((r, i) => (
                  <Text key={r.label} color={severityColor[r.severity]} dimColor={r.severity === 'info'}>
                    {i > 0 ? '   ' : ''}
                    {severityGlyph[r.severity]} {r.label}
                  </Text>
                ))}
              </Text>
            ) : (
              risks.map((r) => <RiskWithFix key={r.label} risk={r} />)
            )}
          </Box>
        ) : null}

        {places?.id === k.id && places.unsupported ? null : (
          <Box flexDirection="column" marginTop={gap}>
            <SectionTitle>WHERE IT IS USED</SectionTitle>
            <Places state={places?.id === k.id ? places : undefined} k={k} width={width - 4} perKind={compact ? 2 : 3} now={now} />
          </Box>
        )}

        {/* A key with none has "Not used yet" above, which says it. */}
        {requests?.rows?.length || requests?.error ? (
          <Box flexDirection="column" marginTop={gap}>
            <SectionTitle>LAST REQUESTS · v shows more</SectionTitle>
            {requests.error ? <Text color={color.warn} wrap="wrap">{`${severityGlyph.warn} ${requests.rows ? 'not updated' : 'could not load'}: ${requests.error}`}</Text> : null}
            {requests.rows?.length ? <LastRequests rows={requests.rows} width={width - 4} now={now} /> : null}
          </Box>
        ) : null}

        <Box flexDirection="column" marginTop={gap}>
          {compact ? null : <SectionTitle>POLICY</SectionTitle>}
          <Field label="from">
            {p.allowed_cidrs?.length || p.allowed_countries?.length ? (
              [...(p.allowed_cidrs ?? []), ...(p.allowed_countries ?? [])].join(', ')
            ) : (
              <Text color={color.warn}>{severityGlyph.warn} any address</Text>
            )}
          </Field>
          <Field label="models">{p.allowed_models?.length ? p.allowed_models.join(', ') : 'any'}</Field>
          <Field label="rate">{p.rpm_limit ? `${p.rpm_limit} requests/min` : 'unlimited'}</Field>
          <Field label="paths">{p.allowed_paths?.length ? p.allowed_paths.join(', ') : 'upstream default'}</Field>
        </Box>

        <Box flexDirection="column" marginTop={gap}>
          {k.tags.length > 0 ? <Field label="tags">{k.tags.join('  ')}</Field> : null}
          {k.credential ? <Field label="credential">{k.credential}</Field> : null}
          <Field label="created">{dateTime(k.created_at)}</Field>
          {k.note ? <Field label="note">{k.note}</Field> : null}
        </Box>

        {/* The spend comes last: what it bounds is the loss from a leak, which the rest is for catching. */}
        {k.spend ? (
          <Box flexDirection="column" marginTop={gap}>
            {compact ? null : <SectionTitle>SPEND</SectionTitle>}
            <SpendRow which="today" k={k} barW={barW} roomy={!compact} width={width} />
            <SpendRow which="month" k={k} barW={barW} roomy={!compact} width={width} />
          </Box>
        ) : null}
      </Body>
    </ScrollFrame>
  );
}

// ---- Incident about a canary ------------------------------------------------

/** An incident whose key is not in the key list: a canary. The commands that answer it, and what fired. */
export function IncidentMain({ incident: i, width, height, scroll, now }: Size & { incident: Incident; scroll: Scroll; now: number }) {
  const r = incidentRisk(i);
  const canary = i.rules.includes('canary');
  // By id: a revoked canary may have passed its name on.
  const commands = [
    `kman incident show ${i.id}`,
    ...(canary ? [`kman canary show ${i.key_id}`] : []),
    `kman incident resolve ${i.id}`,
    ...(canary ? [`kman canary reset ${i.key_id}`] : []),
  ];
  return (
    <ScrollFrame title={`Incident #${i.id}`} width={width} height={height} scroll={scroll}>
      <Body>
        <RiskLine risk={r} />
        <Text wrap="wrap">Enter opens it, to answer it here.</Text>
        {/* First, so they show without scrolling at the smallest size. */}
        <Box flexDirection="column" marginTop={1}>
          <SectionTitle>ANSWER IT</SectionTitle>
          {commands.map((c) => (
            <Text key={c} wrap="wrap">
              {c}
            </Text>
          ))}
          {canary ? <Text dimColor wrap="wrap">A fired canary stays silent until it is reset.</Text> : null}
        </Box>
        <Box flexDirection="column" marginTop={1}>
          <Field label="key">{i.key_name ? `${i.key_name} (${i.key_id})` : i.key_id}</Field>
          <Field label="rules">{i.rules.map(ruleText).join(', ') || '—'}</Field>
          <Field label="state">{`${i.state}${i.quiet ? ', quiet' : ''}`}</Field>
          <Field label="opened">{`${dateTime(i.opened_at)} (${ago(i.opened_at, now)})`}</Field>
          <Field label="last fired">{`${dateTime(i.last_fired_at)} (${ago(i.last_fired_at, now)})`}</Field>
          <Field label="detections">{String(i.detection_count)}</Field>
        </Box>
      </Body>
    </ScrollFrame>
  );
}

/** Incidents or exposure that did not load: what that means, and where else to look. */
export function UnknownMain({ unknown: u, width, height, scroll }: Size & { unknown: Unknown; scroll: Scroll }) {
  const what = u.what === 'incidents' ? 'open incidents' : 'exposed keys';
  return (
    <ScrollFrame title={u.what === 'incidents' ? 'Incidents' : 'Exposure'} width={width} height={height} scroll={scroll}>
      <Body>
        <RiskLine risk={u.risk} />
        <Box flexDirection="column" marginTop={1}>
          <Text wrap="wrap">
            {u.stale
              ? `What is shown about ${what} is from an earlier answer. There may be newer ones.`
              : `kman could not say whether there are ${what}. An empty list does not mean there are none.`}
          </Text>
          <Text dimColor wrap="wrap">It is tried again on every poll. r tries now.</Text>
        </Box>
        <Box flexDirection="column" marginTop={1}>
          <SectionTitle>CHECK IT</SectionTitle>
          <Text wrap="wrap">{u.risk.fix}</Text>
        </Box>
      </Body>
    </ScrollFrame>
  );
}

// ---- Upstream -------------------------------------------------------------

export function UpstreamMain({
  upstream: u,
  keys,
  width,
  height,
  scroll,
  none = 'No upstream selected.',
}: Size & { upstream: Upstream | undefined; keys: Key[]; scroll: Scroll; none?: string }) {
  const { onMax } = scroll;
  useEffect(() => {
    if (!u) onMax(0);
  });
  if (!u) {
    return (
      <Frame title="Upstream" width={width} height={height}>
        <Empty text={none} setup width={width} />
      </Frame>
    );
  }
  const own = keys.filter((k) => k.upstream === u.name);
  const live = own.filter((k) => k.status === 'active').length;
  return (
    <ScrollFrame title={`Upstream: ${u.name}`} width={width} height={height} scroll={scroll}>
      <Body>
        {titleFits(`Upstream: ${u.name}`, undefined, width) ? null : (
          <Text bold wrap="wrap">
            {u.name}
          </Text>
        )}
        <Field label="address">
          {u.tls === false ? <Text color={color.warn}>{severityGlyph.warn} plain http </Text> : null}
          {`${u.host}:${u.port}`}
        </Field>
        <Field label="profile">{u.profile}</Field>
        <Field label="preset">
          {u.update_available ? (
            <Text dimColor>{severityGlyph.info} {u.preset ? `${u.preset} v${u.preset_version ?? '?'}` : 'custom'}: a newer version exists</Text>
          ) : u.preset ? (
            `${u.preset} v${u.preset_version ?? '?'}`
          ) : (
            'custom'
          )}
        </Field>
        <Field label="credentials">
          {u.credentials.length ? (
            u.credentials.map((c) => `${c.name} ····${c.last4 ?? ''}`).join('   ')
          ) : (
            <Text color={severityColor.danger}>{severityGlyph.danger} none: add one before creating keys</Text>
          )}
        </Field>
        <Field label="sdk env">{u.sdk_env ?? '—'}</Field>
        <Field label="keys">{own.length === 0 ? 'none' : `${own.length} (${live} active)`}</Field>
        {/* One route a line: a method and its path are never split across two. */}
        {u.path_allowlist.length ? (
          u.path_allowlist.map((r, i) => (
            <Field key={r} label={i === 0 ? 'routes' : ''}>
              {r}
            </Field>
          ))
        ) : (
          <Field label="routes">none</Field>
        )}
      </Body>
    </ScrollFrame>
  );
}

// ---- Audit entry ----------------------------------------------------------

export function AuditMain({
  entry: e,
  names,
  limited,
  width,
  height,
  scroll,
  none = 'No entry selected.',
}: Size & { entry: AuditEntry | undefined; names: Names; limited: boolean; scroll: Scroll; none?: string }) {
  const { onMax } = scroll;
  useEffect(() => {
    if (!e) onMax(0);
  });
  if (!e) {
    return (
      <Frame title="Entry" width={width} height={height}>
        <Empty text={none} width={width} />
      </Frame>
    );
  }
  return (
    <ScrollFrame title={`Entry #${e.id}`} width={width} height={height} scroll={scroll}>
      <Body>
        <Field label="time">{dateTime(e.time)}</Field>
        <Field label="actor">{e.actor}</Field>
        <Field label="action">
          <Text color={actionColor(e.action)}>{e.action}</Text>
        </Field>
        <Field label="target">{targetText(e, names)}</Field>
        {targetName(e, names) === e.target_id ? null : <Field label="target id">{e.target_id}</Field>}
        {e.client_ip ? <Field label="from">{e.client_ip}</Field> : null}
        {limited ? (
          <Text dimColor wrap="wrap">
            The latest {AUDIT_LIMIT} entries; older ones are not loaded.
          </Text>
        ) : null}
      </Body>
    </ScrollFrame>
  );
}
