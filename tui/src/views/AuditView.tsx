import { Box, Text } from 'ink';
import type { AuditEntry, Key, Upstream } from '../api.js';
import { clock, dateTime, padEnd } from '../format.js';
import { keyLabel } from '../keys.js';
import { Panel, scrollHint, windowStart } from '../ui/parts.js';
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

/** "key leaked-demo": the target's name where kman still knows it, else its id. */
export function targetText(e: AuditEntry, { keys, upstreams }: Names): string {
  let name: string | undefined;
  if (e.target_kind === 'key') {
    const k = keys.find((x) => x.id === e.target_id);
    name = k && keyLabel(k);
  } else if (e.target_kind === 'upstream') {
    name = upstreams.find((u) => u.id === e.target_id)?.name;
  } else if (e.target_kind === 'credential') {
    name = upstreams.flatMap((u) => u.credentials).find((c) => c.id === e.target_id)?.name;
  }
  return `${e.target_kind.replace('_', ' ')} ${name ?? e.target_id}`;
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

const DETAIL_H = 4;
export const ACTION_W = 20;
const ACTOR_W = 14;

export function AuditView({ audit, names, fresh, selected, width, height, now }: Props) {
  const rows = Math.max(1, height - 1 - DETAIL_H);
  const sel = Math.min(selected, Math.max(0, audit.length - 1));
  const start = windowStart(sel, audit.length, rows);
  const shown = audit.slice(start, start + rows);
  const chosen = audit[sel];
  const targetW = Math.max(10, width - 2 - 12 - ACTOR_W - ACTION_W);

  return (
    <Box flexDirection="column" width={width} height={height}>
      <Box width={width} justifyContent="space-between">
        <Text dimColor bold>
          {'  '}
          {padEnd('TIME', 12)}
          {padEnd('ACTOR', ACTOR_W)}
          {padEnd('ACTION', ACTION_W)}
          {padEnd('TARGET', Math.max(6, targetW - 22))}
        </Text>
        <Text dimColor>{scrollHint(start, shown.length, audit.length)}</Text>
      </Box>
      <Box flexDirection="column" height={rows} overflow="hidden">
        {audit.length === 0 ? <Text dimColor>The audit log is empty.</Text> : null}
        {shown.map((e) => {
          const isSel = e === chosen;
          return (
            <Text key={e.id} bold={isSel} wrap="truncate-end">
              <Text color={color.accent}>{isSel ? '▌' : fresh.has(e.id) ? '●' : ' '}</Text>
              <Text> </Text>
              <Text dimColor={!isSel}>{padEnd(clock(e.time, now), 12)}</Text>
              <Text>{padEnd(e.actor, ACTOR_W)}</Text>
              <Text color={actionColor(e.action)}>{padEnd(e.action, ACTION_W)}</Text>
              <Text>{padEnd(targetText(e, names), targetW)}</Text>
            </Text>
          );
        })}
      </Box>
      <Panel title={chosen ? `Entry #${chosen.id}` : undefined} width={width} height={DETAIL_H}>
        {chosen ? (
          <>
            <Text dimColor wrap="truncate-end">
              {dateTime(chosen.time)} · {chosen.actor} · {chosen.action} · {targetText(chosen, names)}
            </Text>
            <Text dimColor wrap="truncate-end">
              {chosen.target_kind} id {chosen.target_id}
              {chosen.client_ip ? ` · from ${chosen.client_ip}` : ''}
            </Text>
          </>
        ) : (
          <Text dimColor>No entry selected.</Text>
        )}
      </Panel>
    </Box>
  );
}
