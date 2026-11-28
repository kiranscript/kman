import { Box, Text, useInput } from 'ink';
import { useEffect, useState } from 'react';
import type { CreatedKey, RotatedKey } from '../api.js';
import { keyLabel } from '../keys.js';
import { color } from '../ui/theme.js';

/** Asks the terminal to put `text` on the clipboard (OSC 52). Not every terminal allows it. */
export function osc52(text: string, write: (s: string) => void = (s) => void process.stdout.write(s)): void {
  write(`\u001b]52;c;${Buffer.from(text, 'utf8').toString('base64')}\u0007`);
}

/** A secret that is shown once: of a key just created, or just rotated. */
export type ShownSecret = ({ was: 'created' } & CreatedKey) | ({ was: 'rotated'; note?: string } & RotatedKey);

interface Props {
  shown: ShownSecret;
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
export function SecretDialog({ shown, width, onClose, onCopy = osc52, armMs = 750 }: Props) {
  const [note, setNote] = useState<{ text: string; warn: boolean } | undefined>();
  const [armed, setArmed] = useState(armMs <= 0);
  useEffect(() => {
    if (armMs <= 0) return;
    const t = setTimeout(() => setArmed(true), armMs);
    return () => clearTimeout(t);
  }, [armMs]);
  const exportLine = shown.sdk_env ? `export ${shown.sdk_env}=${shown.secret}` : undefined;
  // Ink breaks a long line with a hard newline, which would corrupt a copied secret. So the
  // export line is shown only when it fits; otherwise the secret above is the thing to copy.
  const exportFits = exportLine !== undefined && [...exportLine].length <= width - 4;

  useInput((input, key) => {
    if (key.return) return armed ? onClose() : undefined;
    if (key.escape) return setNote({ text: 'Esc does not close this: the secret cannot be shown again. Press Enter once you have copied it.', warn: true });
    if (input === 'c') {
      onCopy(shown.secret);
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
        Key {shown.was}: {keyLabel(shown.key)}
      </Text>
      <Text>Copy it now. kman keeps only its hash, so it cannot show it again.</Text>
      {shown.was === 'rotated' ? <Text wrap="wrap">{`The old secret is refused from now on.${shown.note ? ` ${shown.note}` : ''}`}</Text> : null}
      <Box marginTop={1}>
        <Text bold color={color.ok}>
          {shown.secret}
        </Text>
      </Box>
      <Box marginTop={1} flexDirection="column">
        {exportFits ? (
          <>
            <Text dimColor>For an app, in its shell:</Text>
            <Text>{exportLine}</Text>
          </>
        ) : shown.sdk_env ? (
          <Text dimColor>Set {shown.sdk_env} to the secret above.</Text>
        ) : (
          <Text dimColor>Use the secret above as this upstream's key.</Text>
        )}
        {/* A rotation changes only the secret. Older servers omit the bound address. */}
        <Text dimColor>
          {shown.was === 'rotated'
            ? 'The base URL apps use does not change.'
            : shown.proxy_url
              ? `Base URL on the kman host: ${shown.proxy_url}${shown.sdk_base_path ?? ''}`
              : `Base URL: kman's proxy address${shown.sdk_base_path ? ` + ${shown.sdk_base_path}` : ''} (http://127.0.0.1:8080 by default)`}
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
