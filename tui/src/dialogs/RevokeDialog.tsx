import { Box, Text, useInput } from 'ink';
import { useState } from 'react';
import type { Key } from '../api.js';
import { keyLabel } from '../keys.js';
import { editText, TextField } from '../ui/TextField.js';
import { Panel } from '../ui/parts.js';
import { color } from '../ui/theme.js';

interface Props {
  keyData: Key;
  /** The widest the dialog may be. */
  maxWidth: number;
  busy: boolean;
  error?: string;
  onConfirm: () => void;
  onCancel: () => void;
}

/** Revoking cannot be undone, so it asks for the key's name to be typed, not just "y". */
export function RevokeDialog({ keyData, maxWidth, busy, error, onConfirm, onCancel }: Props) {
  const [typed, setTyped] = useState('');
  const [tried, setTried] = useState(false);
  const expected = keyLabel(keyData);
  const matches = typed === expected;

  useInput((input, key) => {
    if (busy) return;
    if (key.escape) return onCancel();
    if (key.return) {
      if (matches) onConfirm();
      else setTried(true);
      return;
    }
    setTried(false);
    setTyped((v) => editText(v, input, key, 63));
  });

  return (
    <Panel title={`Revoke ${expected}?`} width={Math.min(maxWidth, Math.max(64, expected.length + 6))} tone={color.danger}>
      <Box flexDirection="column" marginTop={1}>
        <Text>This ends the key for good. Apps using it are refused from their next request, and a revoked key cannot be enabled again.</Text>
        <Text dimColor>To stop it for now, disable it instead.</Text>
      </Box>
      <Box marginTop={1} flexDirection="column">
        <Text>Type the key's name exactly to confirm:</Text>
        <Text bold wrap="wrap">
          {expected}
        </Text>
      </Box>
      <TextField value={typed} focused width={40} />
      <Box marginTop={1}>
        {error ? (
          <Text color={color.danger}>✖ {error}</Text>
        ) : tried ? (
          <Text color={color.danger}>✖ That is not the name. Type it exactly, or Esc to cancel.</Text>
        ) : busy ? (
          <Text dimColor>Revoking…</Text>
        ) : (
          <Text dimColor>{matches ? 'Press Enter to revoke · Esc to cancel' : 'Esc to cancel'}</Text>
        )}
      </Box>
    </Panel>
  );
}
