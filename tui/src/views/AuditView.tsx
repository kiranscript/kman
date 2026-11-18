import { Box, Text } from 'ink';
import type { AuditEntry, Key, Upstream } from '../api.js';
import { clock, dateTime, padEnd } from '../format.js';
import { keyLabel } from '../keys.js';
import { Panel, Wrapped, fittingScrollHint, windowStart } from '../ui/parts.js';
import { AUDIT_LIMIT } from '../useAdmin.js';
import { color } from '../ui/theme.js';

/** The colour of an audit action by what it does to a key or credential. */
export function actionColor(action: string): string | undefined {
  if (/(revoke|delete|remove)/.test(action)) return color.danger;
  if (/disable/.test(action)) return color.warn;
  if (/(create|enable|add)/.test(action)) return color.ok;
  return undefined;
}

export interface Names {
  keys: Key[];
  upstreams: Upstream[];
}

/** A target kind as words: price_override -> price override. */
export const kindName = (kind: string) => kind.replaceAll('_', ' ');

/** "key leaked-demo": the target's name where kman still knows it, else its id. */
export function targetText(e: AuditEntry, names: Names): string {
  return `${kindName(e.target_kind)} ${targetName(e, names)}`;
}

/** Just the target's name, or its id. */
export function targetName(e: AuditEntry, { keys, upstreams }: Names): string {
  let name: string | undefined;
  if (e.target_kind === 'key') {
    const k = keys.find((x) => x.id === e.target_id);
    name = k && keyLabel(k);
  } else if (e.target_kind === 'upstream') {
    name = upstreams.find((u) => u.id === e.target_id)?.name;
  } else if (e.target_kind === 'credential') {
    name = upstreams.flatMap((u) => u.credentials).find((c) => c.id === e.target_id)?.name;
  }
  return name ?? e.target_id;
}

interface Props {
  audit: AuditEntry[];
  names: Names;
  fresh: ReadonlySet<number>;
  selected: number;
  width: number;
  height: number;
  now: number;
}

// Border, title, the action and target, and up to two rows for when, who and which id.
const DETAIL_H = 6;
/** Each column is one wider than its longest value, so neighbours never touch. */
export const ACTION_W = 22;
const ACTOR_MIN = 14;
const TIME_W = 12;

/** Rows of entries the list shows at once. */
export const auditRows = (height: number) => Math.max(1, height - 1 - DETAIL_H);

export function AuditView({ audit, names, fresh, selected, width, height, now }: Props) {
  const rows = auditRows(height);
  if (audit.length === 0) {
    return (
      <Box width={width} height={height}>
        <Text dimColor>The audit log is empty: nothing has changed yet.</Text>
      </Box>
    );
  }
  const sel = Math.min(selected, Math.max(0, audit.length - 1));
  const start = windowStart(sel, audit.length, rows);
  const shown = audit.slice(start, start + rows);
  const chosen = audit[sel];
  // Wide enough for the longest actor in the list (not just the visible rows, so scrolling does not shift columns), while the target keeps room.
  const longestActor = Math.max(0, ...audit.map((e) => [...e.actor].length));
  const ACTOR_W = Math.max(ACTOR_MIN, Math.min(22, longestActor + 1, width - 2 - TIME_W - ACTION_W - 24));
  const targetW = Math.max(10, width - 2 - TIME_W - ACTOR_W - ACTION_W);

  return (
    <Box flexDirection="column" width={width} height={height}>
      <Box width={width} justifyContent="space-between">
        <Text dimColor bold>
          {'  '}
          {padEnd('TIME', TIME_W)}
          {padEnd('ACTOR', ACTOR_W)}
          {padEnd('ACTION', ACTION_W)}
          TARGET
        </Text>
        <Text dimColor>{fittingScrollHint(start, shown.length, audit.length, width - (2 + TIME_W + ACTOR_W + ACTION_W + 6) - 1)}</Text>
      </Box>
      <Box flexDirection="column" height={rows} overflow="hidden">
        {audit.length === 0 ? <Text dimColor>The audit log is empty.</Text> : null}
        {shown.map((e) => {
          const isSel = e === chosen;
          return (
            <Text key={e.id} bold={isSel} wrap="truncate-end">
              <Text color={color.accent}>{isSel ? '▌' : ' '}</Text>
              <Text color={color.accent}>{fresh.has(e.id) ? '◆' : ' '}</Text>
              <Text dimColor={!isSel}>{padEnd(clock(e.time, now), TIME_W)}</Text>
              <Text>{padEnd(e.actor, ACTOR_W - 1)} </Text>
              <Text color={actionColor(e.action)}>{padEnd(e.action, ACTION_W - 1)} </Text>
              <Text>{padEnd(targetText(e, names), targetW)}</Text>
            </Text>
          );
        })}
      </Box>
      <Panel
        title={chosen ? `Entry #${chosen.id}` : undefined}
        hint={audit.length >= AUDIT_LIMIT ? `the latest ${AUDIT_LIMIT} entries; older ones are not loaded` : undefined}
        width={width}
        height={DETAIL_H}
      >
        {chosen ? (
          <>
            <Text dimColor wrap="truncate-end">
              {chosen.action} · {targetText(chosen, names)}
            </Text>
            <Wrapped
              text={`${dateTime(chosen.time)} · ${chosen.actor} · ${kindName(chosen.target_kind)} id ${chosen.target_id}${chosen.client_ip ? ` · from ${chosen.client_ip}` : ''}`}
              width={width - 4}
              maxLines={2}
              dim
            />
          </>
        ) : (
          <Text dimColor>No entry selected.</Text>
        )}
      </Panel>
    </Box>
  );
}
