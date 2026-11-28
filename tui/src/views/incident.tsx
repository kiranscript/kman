// One incident in the main panel: what fired, where from, and what has been done about it,
// with the keys that answer it kept on screen under it.
import { Box, Text } from 'ink';
import type { ReactNode } from 'react';
import { useEffect } from 'react';
import type { Incident, Key } from '../api.js';
import { ago, clock, fit, padEnd, plural, textWidth, wrapLines } from '../format.js';
import { actionKeys, actions, allRefused, isCanary, resolvedText, timeline, usualText, whatFired, whereFrom } from '../incidents.js';
import type { Action } from '../incidents.js';
import { incidentRisk } from '../risk.js';
import { Frame } from '../ui/frame.js';
import { color, okGlyph, severityColor, severityGlyph } from '../ui/theme.js';
import type { IncidentState } from '../useAdmin.js';
import type { Scroll } from './detail.js';

/** Origins listed before the rest are only counted. */
const ORIGINS = 20;

const actionLabels: Record<Action, string> = {
  acknowledge: 'acknowledge',
  thisWasMe: 'this was me…',
  resolve: 'resolve…',
  disable: 'disable…',
  enable: 'enable…',
  rotate: 'rotate…',
  revoke: 'revoke…',
  reset: 'reset canary…',
};

const ofTheCase: ReadonlySet<Action> = new Set<Action>(['acknowledge', 'thisWasMe', 'resolve', 'reset']);

/** The actions in two rows, as they are shown: the case first, then the key. */
function actionRows(list: Action[]): Action[][] {
  const first = list.filter((a) => ofTheCase.has(a));
  return [first, list.filter((a) => !first.includes(a))].filter((r) => r.length > 0);
}

/** `text` on as many lines as it needs, the later ones under where it starts. */
function hanging(key: string, text: string, width: number, indent: number, style: { dim?: boolean; tone?: string } = {}): ReactNode[] {
  return wrapLines(text, Math.max(8, width - indent), 50).map((line, i) => (
    <Text key={`${key}:${i}`} color={style.tone} dimColor={style.dim} wrap="truncate-end">
      {' '.repeat(indent)}
      {line}
    </Text>
  ));
}

const title = (key: string, text: string) => (
  <Text key={key} dimColor bold>
    {text}
  </Text>
);

/** The lines of the incident, one to a row of the screen, so scrolling moves by what is seen. */
function lines(state: IncidentState, listed: Incident | undefined, key: Key | undefined, width: number, now: number): ReactNode[] {
  const d = state.detail;
  const i = d ?? listed;
  const out: ReactNode[] = [];
  const blank = () => out.push(<Text key={`blank:${out.length}`}> </Text>);
  if (state.error) {
    out.push(...hanging('error', `${severityGlyph.warn} ${d ? 'Not updated' : 'Could not load it'}: ${state.error}`, width, 0, { tone: color.warn }));
  }
  if (!i) return state.error ? out : [<Text key="loading" dimColor>Loading…</Text>];
  const risk = incidentRisk(i);
  const resolved = i.state === 'resolved';
  const head = [
    i.severity.toUpperCase(),
    `${i.state.toUpperCase()}${i.quiet ? ', quiet' : ''}`,
    `opened ${ago(i.opened_at, now)}`,
    `last fired ${ago(i.last_fired_at, now)}`,
    plural(i.detection_count, 'detection'),
  ].join(' · ');
  out.push(
    ...hanging('head', `${resolved ? okGlyph : severityGlyph[risk.severity]} ${head}`, width, 0, { tone: resolved ? color.ok : severityColor[risk.severity] }),
  );
  if (resolved) {
    const text = resolvedText(i);
    out.push(...hanging('resolved', `${text[0]!.toUpperCase()}${text.slice(1)}${i.resolved_at ? `, ${ago(i.resolved_at, now)}` : ''}.`, width, 2));
    if (key?.status === 'disabled') out.push(...hanging('disabled', 'The key is still disabled: enable it in [2] Keys.', width, 2, { dim: true }));
  } else if (i.state === 'acknowledged' && i.acknowledged_by) {
    out.push(...hanging('ack', `Acknowledged by ${i.acknowledged_by}${i.acknowledged_at ? `, ${ago(i.acknowledged_at, now)}` : ''}.`, width, 2, { dim: true }));
  }
  if (!d) {
    if (!state.error) out.push(<Text key="loading" dimColor>Loading…</Text>);
    return out;
  }

  blank();
  out.push(title('fired', 'WHAT FIRED'));
  const fired = whatFired(d.detections);
  // Said once when it is the same for all of them: the rows are few at the smallest size.
  const together = allRefused(fired);
  // Never more than half: a long rule name must leave room for what it saw.
  const whatW = Math.min(Math.floor(width / 2), Math.max(0, ...fired.map((f) => textWidth(f.what))) + 2);
  const valueW = Math.min(24, Math.max(0, ...fired.map((f) => textWidth(f.observed)))) + 2;
  fired.forEach((f, n) => {
    const value = `${f.observed}${f.times > 1 ? ` ×${f.times}` : ''}`;
    const usual = f.usual ? usualText(f.usual) : '';
    // The usual stands beside what was seen when both fit, and under it when not.
    const column = Math.max(valueW, textWidth(value) + 2);
    const beside = usual !== '' && whatW + column + textWidth(usual) <= width;
    if (beside || textWidth(value) <= width - whatW) {
      out.push(
        <Text key={`f${n}`} wrap="truncate-end">
          {padEnd(f.what, whatW)}
          <Text bold>{beside ? padEnd(value, column) : value}</Text>
          {beside ? <Text dimColor>{usual}</Text> : null}
        </Text>,
      );
    } else {
      const [first = '', ...rest] = wrapLines(value, width - whatW, 4);
      out.push(
        <Text key={`f${n}`} wrap="truncate-end">
          {padEnd(f.what, whatW)}
          <Text bold>{first}</Text>
        </Text>,
        ...rest.flatMap((line, j) => hanging(`f${n}v${j}`, line, width, whatW)),
      );
    }
    if (usual && !beside) out.push(...hanging(`f${n}u`, usual, width, whatW, { dim: true }));
    if (f.refused !== undefined && together === undefined) {
      out.push(...hanging(`f${n}r`, `${f.forwarded ? 'some refused' : 'refused'}${f.refused ? `: ${f.refused}` : ''}`, width, whatW, { tone: color.warn }));
    }
  });
  if (together !== undefined) out.push(...hanging('refused', `All refused${together ? `: ${together}` : ''}`, width, 0, { tone: color.warn }));
  if (fired.length === 0) out.push(<Text key="nofired" dimColor>No detections are listed.</Text>);
  const unlisted = d.detection_count - d.detections.length;
  if (unlisted > 0) out.push(...hanging('unlisted', `${plural(unlisted, 'more detection is', 'more detections are')} not listed.`, width, 0, { dim: true }));

  blank();
  out.push(title('from', 'WHERE FROM'));
  const origins = whereFrom(d.detections);
  origins.slice(0, ORIGINS).forEach((o, n) => out.push(...hanging(`o${n}`, o, width, 0)));
  if (origins.length > ORIGINS) out.push(<Text key="moreorigins" dimColor>{`+${origins.length - ORIGINS} more: kman incident show ${d.id}`}</Text>);
  if (origins.length === 0) out.push(<Text key="noorigin" dimColor>Not recorded.</Text>);
  if (d.addresses_withheld) {
    out.push(...hanging('withheld', 'Addresses are withheld: kman settings set expose_client_addresses on', width, 0, { dim: true }));
  }

  blank();
  out.push(title('timeline', 'TIMELINE'));
  const done = timeline(d);
  const timeW = Math.max(0, ...done.map((a) => textWidth(clock(a.time, now)))) + 2;
  done.forEach((a) => {
    const [first = '', ...rest] = wrapLines(a.text, width - timeW, 4);
    out.push(
      <Text key={`a${a.id}`} wrap="truncate-end">
        <Text dimColor>{padEnd(clock(a.time, now), timeW)}</Text>
        {first}
      </Text>,
      ...rest.flatMap((line, j) => hanging(`a${a.id}v${j}`, line, width, timeW)),
    );
  });
  if (done.length === 0) out.push(<Text key="noaction" dimColor>Nothing yet.</Text>);
  return out;
}

export function IncidentView({
  id,
  state,
  listed,
  keyData,
  width,
  height,
  now,
  scroll,
  offline,
}: {
  id: number;
  state: IncidentState;
  /** The incident as the list has it, for what to show before its detail arrives. */
  listed?: Incident;
  /** Its key as listed; a canary is not. */
  keyData?: Key;
  width: number;
  height: number;
  now: number;
  scroll: Scroll;
  /** kman is not answering, so no action can be sent. */
  offline: boolean;
}) {
  const d = state.detail;
  const inner = width - 4;
  const known = d ?? listed;
  const offered = d ? actions(d, keyData) : [];
  const rows = actionRows(offered);
  const notes: { text: string; tone?: string }[] = [];
  if (d && isCanary(d)) notes.push({ text: 'A fired canary stays silent until it is reset.' });
  if (d && offered.length === 0) notes.push({ text: 'Resolved: there is nothing left to answer.' });
  if (offline) notes.push({ text: 'Actions are off while kman does not answer.', tone: color.warn });
  const noteLines = notes.flatMap((n) => wrapLines(n.text, inner, 2).map((line) => ({ ...n, text: line })));
  // The actions stay at the foot, under a rule; what is above them scrolls.
  const foot = d ? 1 + rows.length + noteLines.length : 0;
  const view = Math.max(1, height - 2 - foot);
  const body = lines(state, listed, keyData, inner, now);
  const max = Math.max(0, body.length - view);
  const off = Math.min(scroll.offset, max);
  const { onMax, onView } = scroll;
  useEffect(() => {
    onView?.(view);
    onMax(max);
  });
  const more = max === 0 ? '' : off === 0 ? '↓ more · ' : off >= max ? '↑ back · ' : '↑↓ more · ';
  const name = known ? known.key_name || known.key_id : '';
  const cell = Math.floor(inner / 3);
  return (
    <Frame title={`Incident #${id}${name ? ` · ${name}` : ''}`} position={`${more}Esc back`} focused width={width} height={height}>
      <Box flexDirection="column" paddingX={1} height={view} flexShrink={0} overflow="hidden">
        {body.slice(off, off + view)}
      </Box>
      {foot > 0 ? (
        <Box flexDirection="column" paddingX={1} flexShrink={0}>
          <Text dimColor>{'─'.repeat(inner)}</Text>
          {rows.map((row) => (
            <Box key={row.join()}>
              {row.map((a) => (
                <Box key={a} width={cell} flexShrink={0}>
                  <Text wrap="truncate-end" dimColor={offline}>
                    <Text bold color={offline ? undefined : color.key}>
                      {actionKeys[a]}
                    </Text>{' '}
                    {fit(actionLabels[a], cell - 3)}
                  </Text>
                </Box>
              ))}
            </Box>
          ))}
          {noteLines.map((n, i) => (
            <Text key={i} color={n.tone} dimColor={!n.tone}>
              {n.text}
            </Text>
          ))}
        </Box>
      ) : null}
    </Frame>
  );
}
