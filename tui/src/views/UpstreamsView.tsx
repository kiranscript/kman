import { Box, Text } from 'ink';
import type { Key, Upstream } from '../api.js';
import { fit, fitMiddle, packLines, padEnd, plural } from '../format.js';
import { Field, Panel, SectionTitle, scrollHint, windowStart } from '../ui/parts.js';
import { color } from '../ui/theme.js';

interface Props {
  upstreams: Upstream[];
  keys: Key[];
  selected: number;
  width: number;
  height: number;
}

/** The least the detail panel gets: its border, title, five facts, a gap and the ROUTES title are 10 rows. */
const DETAIL_MIN = 12;
const DETAIL_FIXED = 10;
/** The widest the other columns and gaps make a row: NAME takes what is left. */
const OTHER_COLS = 64;

/** Rows of upstreams the list shows at once: all of them, or what the detail panel's minimum leaves. */
export function upstreamRows(count: number, height: number): number {
  return Math.min(Math.max(1, count), Math.max(1, height - 2 - DETAIL_MIN));
}

export function UpstreamsView({ upstreams, keys, selected, width, height }: Props) {
  // A title row (with the scroll hint, as in the other lists), the column header, the list,
  // then the panel, which takes whatever the list leaves.
  const rows = upstreamRows(upstreams.length, height);
  const detailH = height - 2 - rows;
  const sel = Math.min(selected, Math.max(0, upstreams.length - 1));
  const start = windowStart(sel, upstreams.length, rows);
  const shown = upstreams.slice(start, start + rows);
  const chosen = upstreams[sel];
  const liveKeys = (u: Upstream) => keys.filter((k) => k.upstream === u.name && k.status === 'active').length;
  const addrW = 24;
  const longest = Math.max(...upstreams.map((u) => [...u.name].length), 0);
  const nameW = Math.max(8, Math.min(28, longest + 1, width - OTHER_COLS));

  if (upstreams.length === 0) {
    return (
      <Box flexDirection="column" width={width} height={height}>
        <Text>No upstreams yet. An upstream is an API kman sits in front of.</Text>
        <Text dimColor>Add one with: kman upstream add openai --preset openai</Text>
      </Box>
    );
  }

  return (
    <Box flexDirection="column" width={width} height={height}>
      <Box width={width} justifyContent="space-between">
        <Text dimColor>{plural(upstreams.length, 'upstream')}</Text>
        <Text dimColor>{scrollHint(start, shown.length, upstreams.length)}</Text>
      </Box>
      <Text dimColor bold wrap="truncate-end">
        {'   '}
        {padEnd('NAME', nameW)} {padEnd('ADDRESS', addrW)} {padEnd('PROFILE', 10)} {padEnd('PRESET', 14)} {padEnd('KEYS', 9)}
      </Text>
      <Box flexDirection="column" overflow="hidden">
        {shown.map((u) => {
          const isSel = u === chosen;
          return (
            <Text key={u.id} bold={isSel} wrap="truncate-end">
              <Text color={color.accent}>{isSel ? '▌' : ' '}</Text>
              {/* An upstream with no credential cannot have keys made on it, so it is marked in the list. */}
              <Text color={color.warn}>{u.credentials.length === 0 ? '▲' : ' '}</Text>
              <Text> {padEnd(fitMiddle(u.name, nameW), nameW)} </Text>
              {u.tls === false ? (
                <>
                  <Text dimColor={!isSel}>{padEnd(`${u.host}:${u.port}`, addrW - PLAIN.length)}</Text>
                  <Text color={color.warn}>{PLAIN}</Text>
                  <Text> </Text>
                </>
              ) : (
                <Text dimColor={!isSel}>{padEnd(`${u.host}:${u.port}`, addrW)} </Text>
              )}
              <Text dimColor={!isSel}>{padEnd(u.profile, 10)} </Text>
              {u.update_available ? (
                <>
                  <Text color={color.warn}>▲ </Text>
                  <Text dimColor={!isSel}>{padEnd(presetText(u), 12)} </Text>
                </>
              ) : (
                <Text dimColor={!isSel}>{padEnd(presetText(u), 14)} </Text>
              )}
              <Text>{padEnd(`${liveKeys(u)} active`, 9)}</Text>
            </Text>
          );
        })}
      </Box>
      <Panel title={chosen?.name ?? 'Upstream'} width={width} height={detailH}>
        {chosen ? (
          <>
            <Field label={chosen.credentials.length === 1 ? 'credential' : 'credentials'}>
              {chosen.credentials.length ? (
                chosen.credentials.map((c) => `${c.name} ····${c.last4 ?? ''}`).join('   ')
              ) : (
                <Text color={color.warn}>▲ none: add one before creating keys</Text>
              )}
            </Field>
            <Field label="preset">
              {chosen.update_available ? (
                <Text color={color.warn}>▲ {presetText(chosen)}: a newer version exists</Text>
              ) : (
                presetText(chosen)
              )}
            </Field>
            <Field label="address">
              {chosen.tls === false ? <Text color={color.warn}>▲ plain http </Text> : null}
              {`${chosen.host}:${chosen.port}`}
            </Field>
            <Field label="sdk env">{chosen.sdk_env ?? '—'}</Field>
            <Field label="keys">
              {plural(keys.filter((k) => k.upstream === chosen.name).length, 'key')} ({liveKeys(chosen)} active)
            </Field>
            <Box flexDirection="column" marginTop={1}>
              <SectionTitle>ROUTES</SectionTitle>
              {(chosen.path_allowlist.length ? packLines(chosen.path_allowlist, width - 4, detailH - DETAIL_FIXED) : ['none']).map((line, i) => (
                <Text key={i} dimColor wrap="truncate-end">
                  {line}
                </Text>
              ))}
            </Box>
          </>
        ) : (
          <Text dimColor>No upstream selected.</Text>
        )}
      </Panel>
    </Box>
  );
}

/** A marker for an upstream reached without TLS, kept even when the address is cut. */
const PLAIN = ' ▲http';

function presetText(u: Upstream): string {
  return u.preset ? fit(`${u.preset} v${u.preset_version ?? '?'}`, 28) : 'custom';
}
