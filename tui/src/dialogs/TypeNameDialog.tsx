import { Box, Text, useInput } from 'ink';
import { useEffect, useState } from 'react';
import type { Key } from '../api.js';
import { isChord, keyLabel } from '../keys.js';
import { editField, TextField } from '../ui/TextField.js';
import type { Edit } from '../ui/TextField.js';
import { Command } from '../ui/command.js';
import { Panel, Wrapped } from '../ui/parts.js';
import { color, severityGlyph } from '../ui/theme.js';
import { useArmed } from '../ui/useArmed.js';

const words = {
  revoke: {
    title: 'Revoke',
    what: 'This ends the key for good: apps using it are refused at once, and it cannot be enabled again.',
    instead: 'To stop it for now, disable it instead.',
    busy: 'Revoking…',
  },
  rotate: {
    title: 'Rotate',
    what: 'It gets a new secret and the old one is refused at once, with no grace: apps using it stop until they have the new one.',
    instead: 'The new secret is shown once, next.',
    busy: 'Rotating…',
  },
} as const;

interface Props {
  action: keyof typeof words;
  keyData: Key;
  /** The command that does the same. */
  command: string;
  /** What it does to the incident it is done for, if there is one. */
  incidentNote?: string;
  /** The widest the dialog may be. */
  maxWidth: number;
  busy: boolean;
  error?: string;
  /** The most lines a server error may take, which is as many as the screen has room for. */
  errorLines?: number;
  /** Enter is ignored for this long after the dialog opens. */
  guardMs?: number;
  onConfirm: () => void;
  onCancel: () => void;
  /** Told whether the typed name matches, so the footer offers Enter only when it would act. */
  onReady?: (ready: boolean) => void;
}

/**
 * Revoking cannot be undone, and rotating cuts off every app that holds the secret, so both
 * ask for the key's name to be typed, not just "y".
 */
export function TypeNameDialog({ action, keyData, command, incidentNote, maxWidth, busy, error, errorLines = 3, guardMs = 0, onConfirm, onCancel, onReady }: Props) {
  const [field, setField] = useState<Edit>({ value: '', cursor: 0 });
  const typed = field.value;
  const [tried, setTried] = useState(false);
  const armed = useArmed(guardMs);
  const w = words[action];
  const expected = keyLabel(keyData);
  const noun = keyData.name ? 'name' : 'id';
  const matches = typed === expected;
  const panelWidth = Math.min(maxWidth, Math.max(64, expected.length + 6));
  useEffect(() => {
    onReady?.(matches);
    return () => onReady?.(false);
  }, [matches, onReady]);

  useInput((input, key) => {
    if (busy) return;
    if (key.escape) return onCancel();
    if (key.return) {
      if (!armed() || isChord(key)) return;
      if (matches) onConfirm();
      else setTried(true);
      return;
    }
    setTried(false);
    setField((f) => editField(f, input, key, 63));
  });

  return (
    <Panel title={`${w.title} ${expected}?`} width={panelWidth} tone={color.danger}>
      <Box flexDirection="column">
        <Text>{w.what}</Text>
        {incidentNote ? <Text>{incidentNote}</Text> : null}
        <Text dimColor>{w.instead}</Text>
      </Box>
      <Command>{command}</Command>
      <Box marginTop={1} flexDirection="column">
        <Text>Type the key's {noun} exactly to confirm:</Text>
        <Text bold wrap="wrap">
          {expected}
        </Text>
      </Box>
      <TextField value={typed} cursor={field.cursor} focused width={panelWidth - 6} />
      {tried || error || busy || matches || typed !== '' ? (
        <Box marginTop={1} flexDirection="column">
          {tried ? (
            <Text color={color.danger}>
              {severityGlyph.danger} That is not the {noun}. Type it exactly.
            </Text>
          ) : error ? (
            <>
              <Wrapped text={error} marker={`${severityGlyph.danger} `} width={panelWidth - 4} maxLines={errorLines} tone={color.danger} />
              <Text dimColor>{busy ? w.busy : matches ? 'Enter tries again.' : `Type the ${noun} exactly.`}</Text>
            </>
          ) : busy ? (
            <Text dimColor>{w.busy}</Text>
          ) : (
            <Text dimColor>{matches ? 'Name matches.' : 'Not matching yet.'}</Text>
          )}
        </Box>
      ) : null}
    </Panel>
  );
}
