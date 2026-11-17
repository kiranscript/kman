import { Box, Text, useInput } from 'ink';
import { Panel, SectionTitle } from '../ui/parts.js';
import { color } from '../ui/theme.js';

const rows: [string, string][] = [
  ['1-4  ← →  Tab', 'switch view'],
  ['↑ ↓  j k  PgUp PgDn', 'move'],
  ['g  G  Home  End', 'first, last'],
  ['/   Esc', 'filter keys, clear the filter'],
  ['Enter', 'on Overview: open the selected key'],
  ['n', 'new key'],
  ['d  e', 'disable, enable the key'],
  ['x', 'revoke the key (typed confirm)'],
  ['Ctrl+S', 'in the new-key form: create now'],
  ['r', 'refresh now'],
  ['q  Ctrl+C', 'quit'],
];

export function HelpDialog({ onClose }: { onClose: () => void }) {
  useInput((_i, key) => {
    if (key.escape || key.return || _i === '?' || _i === 'q') onClose();
  });
  return (
    <Panel title="Keyboard" width={76} tone={color.accent}>
      <Box flexDirection="column" marginTop={1}>
        {rows.map(([k, what]) => (
          <Text key={k}>
            <Text bold color={color.accent}>
              {k.padEnd(22)}
            </Text>
            {what}
          </Text>
        ))}
      </Box>
      <Box marginTop={1} flexDirection="column">
        <SectionTitle>MARKS</SectionTitle>
        <Text dimColor>● active  ◐ disabled  ✖ revoked or danger  ▲ warning  ○ note</Text>
        <Text dimColor>Every change is one admin API call, audited under your token.</Text>
      </Box>
    </Panel>
  );
}
