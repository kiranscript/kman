import { Box, Text, useInput } from 'ink';
import { useEffect, useState } from 'react';
import type { CreatedKey } from '../api.js';
import { keyLabel } from '../keys.js';
import { color } from '../ui/theme.js';

/** Asks the terminal to put `text` on the clipboard (OSC 52). Not every terminal allows it. */
export function osc52(text: string, write: (s: string) => void = (s) => void process.stdout.write(s)): void {
  write(`\u001b]52;c;${Buffer.from(text, 'utf8').toString('base64')}\u0007`);
}

interface Props {
  created: CreatedKey;
  width: number;
  onClose: () => void;
  onCopy?: (text: string) => void;
  /**
   * Enter is ignored for this long after the secret appears: the Enter that created the
   * key, or one more behind it, must not dismiss a secret nobody has seen yet.
   */
  armMs?: number;
}

/**
 * The one time a virtual key's secret is shown. It lives only in this dialog's
 * props. It has no side borders and its lines are never boxed in, so selecting
 * the secret or the export line with a mouse takes nothing but the text.
 * Only Enter closes it: Esc is a reflex, and the secret cannot be shown again.
 */
export function SecretDialog({ created, width, onClose, onCopy = osc52, armMs = 750 }: Props) {
  const [note, setNote] = useState<{ text: string; warn: boolean } | undefined>();
  const [armed, setArmed] = useState(armMs <= 0);
  useEffect(() => {
    if (armMs <= 0) return;
    const t = setTimeout(() => setArmed(true), armMs);
    return () => clearTimeout(t);
  }, [armMs]);
  const exportLine = created.sdk_env ? `export ${created.sdk_env}=${created.secret}` : undefined;
  // Ink breaks a long line with a hard newline, which would corrupt a copied secret. So the
  // export line is shown only when it fits; otherwise the secret above is the thing to copy.
  const exportFits = exportLine !== undefined && [...exportLine].length <= width - 4;

  useInput((input, key) => {
    if (key.return) return armed ? onClose() : undefined;
    if (key.escape) return setNote({ text: 'Esc does not close this: the secret cannot be shown again. Press Enter once you have copied it.', warn: true });
    if (input === 'c') {
      onCopy(created.secret);
      setNote({ text: "Sent to your terminal's clipboard. If nothing pastes, select the line above instead.", warn: false });
    }
  });

  return (
    <Box
      flexDirection="column"
      width={width}
      borderStyle="single"
      borderLeft={false}
      borderRight={false}
      borderColor={color.ok}
      paddingX={1}
      paddingY={1}
    >
      <Text bold color={color.ok}>
        Key created: {keyLabel(created.key)}
      </Text>
      <Text>Copy it now. kman keeps only its hash, so it cannot show it again.</Text>
      <Box marginTop={1}>
        <Text bold color={color.ok}>
          {created.secret}
        </Text>
      </Box>
      <Box marginTop={1} flexDirection="column">
        {exportFits ? (
          <>
            <Text dimColor>For an app, in its shell:</Text>
            <Text>{exportLine}</Text>
          </>
        ) : created.sdk_env ? (
          <Text dimColor>Set {created.sdk_env} to the secret above.</Text>
        ) : (
          <Text dimColor>Use the secret above as this upstream's key.</Text>
        )}
        {/* The proxy address is not in the admin API, so name its default, and always say where requests go. */}
        <Text dimColor>
          Base URL: kman's proxy address{created.sdk_base_path ? ` + ${created.sdk_base_path}` : ''} (http://127.0.0.1:8080 by default)
        </Text>
      </Box>
      <Box marginTop={1} flexDirection="column">
        <Text color={note?.warn ? color.warn : undefined} dimColor={!note}>
          {note?.text ?? 'The secret is cleared from this screen when you press Enter.'}
        </Text>
        <Text>
          <Text bold color={color.accent}>
            c
          </Text>
          <Text dimColor> copy   </Text>
          <Text bold={armed} dimColor={!armed} color={armed ? color.accent : undefined}>
            Enter
          </Text>
          <Text dimColor> done</Text>
        </Text>
      </Box>
    </Box>
  );
}
