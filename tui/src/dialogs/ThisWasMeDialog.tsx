import { Box, Text, useInput } from 'ink';
import { useState } from 'react';
import type { PickKind } from '../api.js';
import { cli } from '../commands.js';
import { plural, wrapLines } from '../format.js';
import type { Pick } from '../incidents.js';
import { Command } from '../ui/command.js';
import { Panel, Wrapped } from '../ui/parts.js';
import { color, severityGlyph } from '../ui/theme.js';
import { useArmed } from '../ui/useArmed.js';

const labels: Record<PickKind, string> = { country: 'country', asn: 'ASN', client: 'client', network: 'network' };

/** Lines of a box's values before the rest are only counted. */
const VALUE_LINES = 2;

/** The values on at most VALUE_LINES lines, saying how many do not fit: ticking adds them all. */
function valueLines(values: string[], width: number): string[] {
  for (let shown = values.length; shown > 0; shown--) {
    const more = values.length - shown;
    const text = `${values.slice(0, shown).join(', ')}${more ? `, +${more} more` : ''}`;
    const lines = wrapLines(text, width, VALUE_LINES + 1);
    if (lines.length <= VALUE_LINES) return lines;
  }
  return [plural(values.length, 'value')];
}

/**
 * "This was me": the incident ends as a false positive, and the places it saw can be added
 * as known sources, a kind at a time. Nothing is ticked to begin with.
 */
export function ThisWasMeDialog({
  id,
  picks,
  ticked,
  width,
  busy,
  error,
  guardMs = 0,
  onTick,
  onConfirm,
  onCancel,
}: {
  id: number;
  /** Undefined until the incident's detail has loaded. */
  picks: Pick[] | undefined;
  /** The kinds ticked. They are held above, as what the incident saw can change under them. */
  ticked: ReadonlySet<PickKind>;
  width: number;
  busy: boolean;
  error?: string;
  guardMs?: number;
  onTick: (kind: PickKind) => void;
  onConfirm: (kinds: PickKind[], everyKey: boolean) => void;
  onCancel: () => void;
}) {
  const [cursor, setCursor] = useState(0);
  const [everyKey, setEveryKey] = useState(false);
  const armed = useArmed(guardMs);
  const boxes = picks ?? [];
  // The scope is the row under the boxes.
  const rows = boxes.length ? boxes.length + 1 : 0;
  const at = Math.min(cursor, Math.max(0, rows - 1));
  const kinds = boxes.filter((p) => !p.withheld && ticked.has(p.kind)).map((p) => p.kind);
  const panelWidth = Math.min(70, width);
  const valueW = panelWidth - 4 - 16;

  useInput((input, key) => {
    if (busy) return;
    if (key.escape) return onCancel();
    if (key.upArrow || input === 'k') return setCursor(Math.max(0, at - 1));
    if (key.downArrow || input === 'j') return setCursor(Math.min(Math.max(0, rows - 1), at + 1));
    if (!armed() || !picks) return;
    if (key.return) return onConfirm(kinds, everyKey && kinds.length > 0);
    const onScope = at === boxes.length;
    if (onScope && (input === ' ' || key.leftArrow || key.rightArrow)) return setEveryKey((v) => !v);
    const box = boxes[at];
    if (input === ' ' && box && !box.withheld) onTick(box.kind);
  });

  const mark = (here: boolean) => (
    <Text bold color={color.accent}>
      {here ? '>' : ' '}{' '}
    </Text>
  );
  return (
    <Panel title={`This was you? Incident #${id}`} width={panelWidth} tone={color.accent}>
      <Text wrap="wrap">It becomes a false positive. Tick what should not count as new:</Text>
      {!picks ? <Text dimColor>Loading what it saw…</Text> : null}
      {picks && boxes.length === 0 ? <Text dimColor>It saw nothing that can be added.</Text> : null}
      {boxes.map((p, n) => (
        <Box key={p.kind}>
          {mark(n === at)}
          <Box width={14} flexShrink={0}>
            <Text dimColor={p.withheld} bold={n === at}>
              {p.withheld ? ' – ' : ticked.has(p.kind) ? '[x]' : '[ ]'} {labels[p.kind]}
            </Text>
          </Box>
          <Box flexDirection="column">
            {p.withheld ? (
              <>
                <Text dimColor>unavailable: withheld while</Text>
                <Text dimColor>expose_client_addresses is off</Text>
              </>
            ) : (
              valueLines(p.values, valueW).map((line) => <Text key={line}>{line}</Text>)
            )}
          </Box>
        </Box>
      ))}
      {boxes.length ? (
        <Box marginTop={1}>
          {mark(at === boxes.length)}
          <Text bold={at === boxes.length}>
            For {everyKey ? 'every key' : 'this key only'}
            <Text dimColor>  Space or ← → changes</Text>
          </Text>
        </Box>
      ) : null}
      <Box marginTop={1}>
        <Text dimColor wrap="wrap">
          {kinds.length ? `Adds what is ticked as known sources, ${everyKey ? 'for every key' : 'for this key'}.` : 'Nothing ticked: it adds no known source.'}
        </Text>
      </Box>
      <Command marginTop={0}>{cli.thisWasMe(id, kinds, everyKey)}</Command>
      {error ? <Wrapped text={error} marker={`${severityGlyph.danger} `} width={panelWidth - 4} maxLines={3} tone={color.danger} /> : null}
      {busy ? (
        <Text dimColor>Resolving…</Text>
      ) : (
        <Text>
          <Text bold color={color.key}>Space</Text>
          <Text dimColor> tick   </Text>
          <Text bold color={color.key}>Enter</Text>
          <Text dimColor> resolve   </Text>
          <Text bold color={color.key}>Esc</Text>
          <Text dimColor> cancel</Text>
        </Text>
      )}
    </Panel>
  );
}
