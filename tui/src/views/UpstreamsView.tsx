import { Box, Text } from 'ink';
import type { Key, Upstream } from '../api.js';
import { padEnd, plural } from '../format.js';
import { Field, Panel, SectionTitle, windowStart } from '../ui/parts.js';
import { color } from '../ui/theme.js';

interface Props {
  upstreams: Upstream[];
  keys: Key[];
  selected: number;
  width: number;
  height: number;
}

const DETAIL_H = 10;

export function UpstreamsView({ upstreams, keys, selected, width, height }: Props) {
  const rows = Math.max(1, height - 2 - DETAIL_H);
  const sel = Math.min(selected, Math.max(0, upstreams.length - 1));
  const start = windowStart(sel, upstreams.length, rows);
  const shown = upstreams.slice(start, start + rows);
  const chosen = upstreams[sel];
  const liveKeys = (u: Upstream) => keys.filter((k) => k.upstream === u.name && k.status === 'active').length;
  const addrW = 24;
  const nameW = Math.min(28, Math.max(12, ...upstreams.map((u) => u.name.length + 2)));

  return (
    <Box flexDirection="column" width={width} height={height}>
      <Text dimColor bold wrap="truncate-end">
        {'  '}
        {padEnd('NAME', nameW)} {padEnd('ADDRESS', addrW)} {padEnd('PROFILE', 10)} {padEnd('PRESET', 14)} {padEnd('KEYS', 9)}
      </Text>
      <Box flexDirection="column" overflow="hidden">
        {upstreams.length === 0 ? (
          <Text dimColor>No upstreams yet. Add one with: kman upstream add openai --preset openai</Text>
        ) : null}
        {shown.map((u) => {
          const isSel = u === chosen;
          return (
            <Text key={u.id} bold={isSel} wrap="truncate-end">
              <Text color={color.accent}>{isSel ? '▌' : ' '}</Text>
              <Text> {padEnd(u.name, nameW)} </Text>
              <Text dimColor={!isSel}>{padEnd(`${u.host}:${u.port}${u.tls === false ? ' (http)' : ''}`, addrW)} </Text>
              <Text dimColor={!isSel}>{padEnd(u.profile, 10)} </Text>
              <Text dimColor={!isSel}>{padEnd(u.preset ? `${u.preset} v${u.preset_version ?? '?'}` : 'custom', 14)} </Text>
              <Text>{padEnd(`${liveKeys(u)} active`, 9)}</Text>
              {u.update_available ? <Text color={color.warn}>▲ preset update</Text> : null}
            </Text>
          );
        })}
      </Box>
      <Box marginTop={1}>
      <Panel title={chosen?.name ?? 'Upstream'} width={width} height={DETAIL_H}>
        {chosen ? (
          <>
            <Field label={chosen.credentials.length === 1 ? 'credential' : 'credentials'}>
              {chosen.credentials.length
                ? chosen.credentials.map((c) => `${c.name} ····${c.last4 ?? ''}`).join('   ')
                : 'none: add one before creating keys'}
            </Field>
            <Field label="sdk env">{chosen.sdk_env ?? '—'}</Field>
            <Field label="keys">
              {plural(keys.filter((k) => k.upstream === chosen.name).length, 'key')} ({liveKeys(chosen)} active)
            </Field>
            <Box flexDirection="column" marginTop={1}>
              <SectionTitle>ROUTES</SectionTitle>
              <Text dimColor wrap="wrap">
                {chosen.path_allowlist.length ? chosen.path_allowlist.join('   ') : 'none'}
              </Text>
            </Box>
          </>
        ) : (
          <Text dimColor>No upstream selected.</Text>
        )}
      </Panel>
      </Box>
    </Box>
  );
}
