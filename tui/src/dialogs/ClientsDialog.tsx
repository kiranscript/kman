import { Box, Text, useInput } from 'ink';
import { useEffect, useState } from 'react';
import type { AdminClient, Key, KeyClient, KeyClients } from '../api.js';
import { ApiError } from '../api.js';
import { ago, fitMiddle, money, padEnd, padStart } from '../format.js';
import { keyLabel } from '../keys.js';
import { Panel, Wrapped, scrollHint } from '../ui/parts.js';
import { color } from '../ui/theme.js';

type State =
  | { kind: 'loading' }
  | { kind: 'ok'; got: KeyClients }
  /** The admin API refused, or could not be reached. `off` is the install not giving addresses out. */
  | { kind: 'error'; message: string; off: boolean };

/** Rows of the dialog that are not addresses: border, title, gap, subtitle, header, note and hints. */
const CHROME = 9;
const PANEL_MAX = 78;

interface Props {
  client: AdminClient;
  keyData: Key;
  /** The room the dialog has. */
  width: number;
  height: number;
  now: number;
  onClose: () => void;
}

/** "DE AS3320": where an address is, from the GeoIP databases, when there are any. */
function whereText(c: KeyClient): string {
  return [c.country, c.asn ? `AS${c.asn}` : ''].filter(Boolean).join(' ');
}

/**
 * The client addresses a key was used from, grouped by address, newest first. The admin API gives
 * them out only when the install turned expose_client_addresses on (an address is personal data),
 * so this loads when it opens and says how to turn it on when it is refused.
 */
export function ClientsDialog({ client, keyData, width, height, now, onClose }: Props) {
  const [state, setState] = useState<State>({ kind: 'loading' });
  const [scroll, setScroll] = useState(0);
  const rows = Math.max(3, height - CHROME);
  const count = state.kind === 'ok' ? state.got.clients.length : 0;
  const maxScroll = Math.max(0, count - rows);

  useEffect(() => {
    let live = true;
    client.keyClients(keyData.id).then(
      (got) => live && setState({ kind: 'ok', got }),
      (err: unknown) =>
        live &&
        setState({
          kind: 'error',
          message: err instanceof Error ? err.message : String(err),
          off: err instanceof ApiError && err.code === 'conflict',
        }),
    );
    return () => {
      live = false;
    };
  }, [client, keyData.id]);

  useInput((input, key) => {
    if (key.escape || key.return || input === 'c') return onClose();
    const to = (n: number) => setScroll((s) => Math.min(maxScroll, Math.max(0, n)));
    if (key.upArrow || input === 'k') to(scroll - 1);
    else if (key.downArrow || input === 'j') to(scroll + 1);
    else if (key.pageUp) to(scroll - Math.max(1, rows - 1));
    else if (key.pageDown) to(scroll + Math.max(1, rows - 1));
    else if (key.home || input === 'g') to(0);
    else if (key.end || input === 'G') to(maxScroll);
  });

  const panelW = Math.min(width, PANEL_MAX);
  const inner = panelW - 4;
  const title = `Addresses that used ${fitMiddle(keyLabel(keyData), Math.max(12, inner - 22))}`;

  let body;
  if (state.kind === 'loading') {
    body = <Text dimColor>Loading…</Text>;
  } else if (state.kind === 'error') {
    body = state.off ? (
      <>
        <Text color={color.warn}>▲ Not shown: the admin API does not give out client addresses.</Text>
        <Wrapped text={state.message} width={inner} maxLines={4} dim />
        <Text dimColor>Turn it on, then press c again.</Text>
      </>
    ) : (
      <Wrapped text={state.message} marker="✖ Could not load them: " width={inner} maxLines={4} tone={color.danger} />
    );
  } else if (state.got.clients.length === 0) {
    body = <Text dimColor>None in the events kman keeps for this key.</Text>;
  } else {
    body = <Table clients={state.got.clients} total={state.got.total} scroll={scroll} rows={rows} inner={inner} now={now} />;
  }

  return (
    <Panel title={title} width={panelW} tone={color.accent}>
      <Box flexDirection="column">
        {state.kind === 'ok' && state.got.clients.length > 0 ? <Text dimColor>By address, from the events kman keeps</Text> : null}
        {body}
      </Box>
      {/* Esc is in the footer; this is the one thing it cannot know. */}
      {maxScroll > 0 ? <Text dimColor>↑↓ scroll</Text> : null}
    </Panel>
  );
}

function Table({
  clients,
  total,
  scroll,
  rows,
  inner,
  now,
}: {
  clients: KeyClient[];
  total: number;
  scroll: number;
  rows: number;
  inner: number;
  now: number;
}) {
  const where = Math.max(5, ...clients.map((c) => [...whereText(c)].length));
  const costOf = (c: KeyClient) => `${money(c.cost_micro)}${c.unmetered_requests ? '+' : ''}`;
  const costW = Math.max(4, ...clients.map((c) => [...costOf(c)].length));
  const longest = Math.max(7, ...clients.map((c) => [...c.address].length));
  const fixed = 2 + 8 + 2 + 7 + 2 + 9 + 2 + where;
  // Cost is the first column to give way, and only when that would cut an address.
  const showCost = inner - (fixed + 2 + costW) >= Math.min(longest, 24);
  const addrW = Math.max(12, Math.min(longest, inner - fixed - (showCost ? 2 + costW : 0)));
  const shown = clients.slice(scroll, scroll + rows);
  const unmetered = clients.some((c) => c.unmetered_requests);
  const line = (a: string, req: string, blk: string, last: string, w: string, cost: string) =>
    `${padEnd(a, addrW)}  ${padStart(req, 8)}  ${padStart(blk, 7)}  ${padEnd(last, 9)}  ${padEnd(w, where)}${showCost ? `  ${padStart(cost, costW)}` : ''}`;
  const more = scrollHint(scroll, shown.length, clients.length);
  return (
    <>
      <Text dimColor bold wrap="truncate-end">
        {line('ADDRESS', 'REQUESTS', 'BLOCKED', 'LAST SEEN', 'WHERE', 'COST')}
      </Text>
      {shown.map((c) => (
        <Text key={c.address} wrap="truncate-end">
          {padEnd(fitMiddle(c.address, addrW), addrW)}
          {'  '}
          {padStart(String(c.requests), 8)}
          {'  '}
          <Text color={c.blocked > 0 ? color.warn : undefined} dimColor={c.blocked === 0}>
            {padStart(String(c.blocked), 7)}
          </Text>
          {'  '}
          <Text dimColor>{padEnd(ago(c.last_seen, now), 9)}</Text>
          {'  '}
          {padEnd(whereText(c), where)}
          {showCost ? `  ${padStart(costOf(c), costW)}` : ''}
        </Text>
      ))}
      <Text dimColor wrap="truncate-end">
        {[more, total > clients.length ? `${total - clients.length} more not listed` : '', unmetered && showCost ? '+ some costs unknown' : '']
          .filter(Boolean)
          .join(' · ') || ' '}
      </Text>
    </>
  );
}
