// The main panel, on the right: the details of whatever is selected on the left,
// or, with Status focused, what needs attention and what has just happened.
import { Box, Text, measureElement } from 'ink';
import type { DOMElement } from 'ink';
import { useEffect, useRef, useState } from 'react';
import type { AuditEntry, Key, Upstream } from '../api.js';
import { capMoney, clock, dateTime, fit, money, padEnd, plural, requestsText, until } from '../format.js';
import { keyLabel } from '../keys.js';
import { keyRisks } from '../risk.js';
import { Frame, titleFits } from '../ui/frame.js';
import { Field, Meter, RiskLine, SectionTitle, StatusBadge, Wrapped } from '../ui/parts.js';
import { color, severityColor, severityGlyph } from '../ui/theme.js';
import { AUDIT_LIMIT } from '../useAdmin.js';
import { actionColor, rowTarget, targetName, targetText } from './audit.js';
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
          <Wrapped
            text={canCreate ? 'Press n to create the first one.' : 'Add an upstream first: kman upstream add openai --preset openai'}
            width={width - 4}
            maxLines={3}
          />
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

/** "today  ████░░░░ $0.9 / $1" and, when there is room, its request count under it. */
function SpendRow({ which, k, barW, roomy }: { which: 'today' | 'month'; k: Key; barW: number; roomy: boolean }) {
  const s = k.spend?.[which];
  if (!s) return null;
  const amount = s.cap_micro ? `${money(s.cost_micro)} / ${capMoney(s.cap_micro)}` : `${money(s.cost_micro)}, no cap`;
  const requests = `${requestsText(s.requests)}${s.unmetered_requests ? `, ${s.unmetered_requests} unmetered` : ''}`;
  return (
    <Box flexDirection="column">
      <Box>
        <Text dimColor>{padEnd(which, 6)}</Text>
        <Meter period={s} width={barW} muted={k.status === 'revoked'} />
        <Text wrap="truncate-end">
          {' '}
          {amount}
          {roomy ? '' : <Text dimColor>{`  ${requests}`}</Text>}
        </Text>
      </Box>
      {roomy ? (
        <Text dimColor>
          {'      '}
          {requests}
        </Text>
      ) : null}
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
  none = 'No key selected.',
}: Size & { keyData: Key | undefined; now: number; compact: boolean; scroll: Scroll; none?: string }) {
  const { onMax } = scroll;
  useEffect(() => {
    if (!k) onMax(0);
  });
  if (!k) {
    return (
      <Frame title="Key" width={width} height={height}>
        <Box paddingX={1}>
          <Text dimColor wrap="wrap">
            {none}
          </Text>
        </Box>
      </Frame>
    );
  }
  // The badge already says "expired", so that risk is not said twice.
  const risks = keyRisks(k, now).filter((r) => !(k.expired && k.status === 'active' && r.label === 'expired'));
  const p = k.policy;
  const gap = compact ? 0 : 1;
  // The title is cut at the panel's width; two names can differ only at the end.
  const label = keyLabel(k);
  const titleClipped = !titleFits(`Key: ${label}`, undefined, width);
  const barW = compact ? 10 : Math.min(14, Math.max(8, width - 4 - 26));
  const expires = k.status === 'revoked' ? 'ended for good' : `expires ${dateTime(k.expires_at)} (${until(k.expires_at, now)})`;
  return (
    <ScrollFrame title={`Key: ${label}`} width={width} height={height} scroll={scroll}>
      <Body>
        {titleClipped ? (
          <Text bold wrap="wrap">
            {label}
          </Text>
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
              risks.map((r) => <RiskLine key={r.label} risk={r} />)
            )}
          </Box>
        ) : null}

        {k.spend ? (
          <Box flexDirection="column" marginTop={gap}>
            {compact ? null : <SectionTitle>SPEND</SectionTitle>}
            <SpendRow which="today" k={k} barW={barW} roomy={!compact} />
            <SpendRow which="month" k={k} barW={barW} roomy={!compact} />
          </Box>
        ) : null}

        <Box flexDirection="column" marginTop={gap}>
          {compact ? null : <SectionTitle>POLICY</SectionTitle>}
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
        <Box paddingX={1}>
          <Text dimColor wrap="wrap">
            {none}
          </Text>
        </Box>
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
          {u.tls === false ? <Text color={color.warn}>▲ plain http </Text> : null}
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
        <Box paddingX={1}>
          <Text dimColor wrap="wrap">
            {none}
          </Text>
        </Box>
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
