import { Box, Text, useInput } from 'ink';
import { useState } from 'react';
import type { Resolution } from '../api.js';
import { cli, resolutionWord } from '../commands.js';
import { isChord } from '../keys.js';
import { Command } from '../ui/command.js';
import { Panel, Wrapped } from '../ui/parts.js';
import { color, severityGlyph } from '../ui/theme.js';
import { useArmed } from '../ui/useArmed.js';

const choices: { key: string; as: Resolution; what: string }[] = [
  { key: 'c', as: 'confirmed', what: 'a real leak or abuse: what it saw stays out of the baseline' },
  { key: 'f', as: 'false_positive', what: 'it was not: what it held back counts as usual again' },
];

/** Resolving says whether it was real, so neither is the default, and then asks y/N. */
export function ResolveDialog({
  id,
  width,
  busy,
  error,
  guardMs = 0,
  onConfirm,
  onCancel,
}: {
  id: number;
  width: number;
  busy: boolean;
  error?: string;
  guardMs?: number;
  onConfirm: (as: Resolution) => void;
  onCancel: () => void;
}) {
  const [as, setAs] = useState<Resolution | undefined>();
  // The question that follows the choice is a screen of its own.
  const armed = useArmed(guardMs, as ?? '');
  useInput((input, key) => {
    if (busy) return;
    if (key.escape) return onCancel();
    if (isChord(key)) return;
    if (!as) {
      const choice = choices.find((c) => c.key === input);
      if (choice && armed()) setAs(choice.as);
      return;
    }
    // y/N: anything that means no, and Enter, leave it unresolved.
    if (input === 'n' || input === 'N' || key.return) return armed() ? onCancel() : undefined;
    if ((input === 'y' || input === 'Y') && armed()) onConfirm(as);
  });

  return (
    <Panel title={`Resolve incident #${id}`} width={Math.min(70, width)} tone={color.warn}>
      {as ? (
        <Box flexDirection="column">
          <Text>
            Resolve it as <Text bold>{resolutionWord(as)}</Text>? <Text bold>y</Text>
            <Text dimColor>/</Text>
            <Text bold>N</Text>
          </Text>
          <Command>{cli.resolve(id, as)}</Command>
        </Box>
      ) : (
        <Box flexDirection="column">
          <Text>Was it real?</Text>
          {choices.map((c) => (
            <Box key={c.key}>
              <Box width={20} flexShrink={0}>
                <Text>
                  <Text bold color={color.key}>
                    {c.key}
                  </Text>{' '}
                  {resolutionWord(c.as)}
                </Text>
              </Box>
              <Box flexShrink={1}>
                <Text dimColor wrap="wrap">
                  {c.what}
                </Text>
              </Box>
            </Box>
          ))}
          <Command>{`kman incident resolve ${id} --as <confirmed|false-positive>`}</Command>
        </Box>
      )}
      {error ? (
        <Box marginTop={1} flexDirection="column">
          <Wrapped text={error} marker={`${severityGlyph.danger} `} width={Math.min(70, width) - 4} maxLines={4} tone={color.danger} />
        </Box>
      ) : null}
      {busy ? <Text dimColor>Resolving…</Text> : null}
    </Panel>
  );
}
