// The main panel, on the right: the details of whatever is selected on the left,
// or, with Status focused, what needs attention and what has just happened.
import { Box, Text, measureElement } from 'ink';
import type { DOMElement } from 'ink';
import { useEffect, useRef, useState } from 'react';
import type { AuditEntry, Key, Upstream } from '../api.js';
import { capMoney, clock, dateTime, fit, money, padEnd, plural, until } from '../format.js';
import { keyLabel } from '../keys.js';
import { keyRisks } from '../risk.js';
import { Frame, titleFits } from '../ui/frame.js';
import { Field, Meter, RiskLine, SectionTitle, StatusBadge } from '../ui/parts.js';
import { severityColor, severityGlyph } from '../ui/theme.js';
import { actionColor, targetText } from './audit.js';
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
          <Text> {fit(targetText(e, names), targetW)}</Text>
        </Text>
      ))}
    </Frame>
  );
}

// ---- A panel whose content scrolls -----------------------------------------

export interface Scroll {
  /** How many lines the content is scrolled up. */
  offset: number;
  /** Told how far the content can scroll, so the keys know where to stop. */
  onMax: (max: number) => void;
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
  const { onMax } = scroll;
  useEffect(() => {
    if (!ref.current) return;
    const h = measureElement(ref.current).height;
    setContent((c) => (c === h ? c : h));
    onMax(Math.max(0, h - view));
  });
  const max = Math.max(0, content - view);
  const off = Math.min(scroll.offset, max);
  const hint = max === 0 ? undefined : off === 0 ? '↓ J more' : off >= max ? '↑ K back' : '↑↓ J K more';
  return (
    <Frame title={title} position={hint} width={width} height={height}>
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
  const requests = `${plural(s.requests, 'request')}${s.unmetered_requests ? `, ${s.unmetered_requests} unmetered` : ''}`;
  return (
    <Box flexDirection="column">
      <Box>
        <Text dimColor>{padEnd(which, 6)}</Text>
        <Meter period={s} width={barW} />
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
}: Size & { keyData: Key | undefined; now: number; compact: boolean; scroll: Scroll }) {
  const { onMax } = scroll;
  useEffect(() => {
    if (!k) onMax(0);
  });
  if (!k) {
    return (
      <Frame title="Key" width={width} height={height}>
        <Text dimColor> No key selected.</Text>
      </Frame>
    );
  }
  const risks = keyRisks(k, now);
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
            {'  '}id {k.id} · {k.upstream ?? 'no upstream'}
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
}: Size & { upstream: Upstream | undefined; keys: Key[]; scroll: Scroll }) {
  const { onMax } = scroll;
  useEffect(() => {
    if (!u) onMax(0);
  });
  if (!u) {
    return (
      <Frame title="Upstream" width={width} height={height}>
        <Text dimColor> No upstream selected.</Text>
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
        <Field label="address">{`${u.host}:${u.port}${u.tls === false ? ' (http)' : ''}`}</Field>
        <Field label="profile">{u.profile}</Field>
        <Field label="preset">
          {u.preset ? `${u.preset} v${u.preset_version ?? '?'}` : 'custom'}
          {u.update_available ? '  ▲ update available' : ''}
        </Field>
        <Field label={u.credentials.length === 1 ? 'credential' : 'credentials'}>
          {u.credentials.length ? u.credentials.map((c) => `${c.name} ····${c.last4 ?? ''}`).join('   ') : 'none: add one before creating keys'}
        </Field>
        <Field label="sdk env">{u.sdk_env ?? '—'}</Field>
        <Field label="keys">
          {plural(own.length, 'key')} ({live} active)
        </Field>
        <Box flexDirection="column" marginTop={1}>
          <SectionTitle>ROUTES</SectionTitle>
          <Text dimColor wrap="wrap">
            {u.path_allowlist.length ? u.path_allowlist.join('   ') : 'none'}
          </Text>
        </Box>
      </Body>
    </ScrollFrame>
  );
}

// ---- Audit entry ----------------------------------------------------------

export function AuditMain({
  entry: e,
  names,
  width,
  height,
  scroll,
}: Size & { entry: AuditEntry | undefined; names: Names; scroll: Scroll }) {
  const { onMax } = scroll;
  useEffect(() => {
    if (!e) onMax(0);
  });
  if (!e) {
    return (
      <Frame title="Entry" width={width} height={height}>
        <Text dimColor> No entry selected.</Text>
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
        <Field label="target id">{e.target_id}</Field>
        {e.client_ip ? <Field label="from">{e.client_ip}</Field> : null}
      </Body>
    </ScrollFrame>
  );
}
