import { Box, Text, useInput } from 'ink';
import { useState } from 'react';
import type { Key } from '../api.js';
import { keyLabel } from '../keys.js';
import { editField, TextField } from '../ui/TextField.js';
import type { Edit } from '../ui/TextField.js';
import { Panel, Wrapped } from '../ui/parts.js';
import { color } from '../ui/theme.js';

interface Props {
  keyData: Key;
  /** The widest the dialog may be. */
  maxWidth: number;
  busy: boolean;
  error?: string;
  /** The most lines a server error may take, which is as many as the screen has room for. */
  errorLines?: number;
  onConfirm: () => void;
  onCancel: () => void;
}

/** Revoking cannot be undone, so it asks for the key's name to be typed, not just "y". */
export function RevokeDialog({ keyData, maxWidth, busy, error, errorLines = 3, onConfirm, onCancel }: Props) {
  const [field, setField] = useState<Edit>({ value: '', cursor: 0 });
  const typed = field.value;
  const [tried, setTried] = useState(false);
  const expected = keyLabel(keyData);
  const matches = typed === expected;
  const panelWidth = Math.min(maxWidth, Math.max(64, expected.length + 6));

  useInput((input, key) => {
    if (busy) return;
    if (key.escape) return onCancel();
    if (key.return) {
      if (matches) onConfirm();
      else setTried(true);
      return;
    }
    setTried(false);
    setField((f) => editField(f, input, key, 63));
  });

  return (
    <Panel title={`Revoke ${expected}?`} width={panelWidth} tone={color.danger}>
      <Box flexDirection="column">
        <Text>This ends the key for good: apps using it are refused at once, and it cannot be enabled again.</Text>
        <Text dimColor>To stop it for now, disable it instead.</Text>
      </Box>
      <Box marginTop={1} flexDirection="column">
        <Text>Type the key's {keyData.name ? 'name' : 'id'} exactly to confirm:</Text>
        <Text bold wrap="wrap">
          {expected}
        </Text>
      </Box>
      <TextField value={typed} cursor={field.cursor} focused width={panelWidth - 6} />
      <Box marginTop={1} flexDirection="column">
        {tried ? (
          <Text color={color.danger}>✖ That is not the {keyData.name ? 'name' : 'id'}. Type it exactly.</Text>
        ) : error ? (
          <>
            <Wrapped text={error} marker="✖ " width={panelWidth - 4} maxLines={errorLines} tone={color.danger} />
            <Text dimColor>{busy ? 'Revoking…' : matches ? 'Enter tries again.' : `Type the ${keyData.name ? 'name' : 'id'} exactly.`}</Text>
          </>
        ) : busy ? (
          <Text dimColor>Revoking…</Text>
        ) : (
          <Text dimColor>{matches ? 'Name matches.' : 'Not matching yet.'}</Text>
        )}
      </Box>
    </Panel>
  );
}
