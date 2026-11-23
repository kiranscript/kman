import { Box, Text, useInput } from 'ink';
import type { Tab } from '../ui/chrome.js';
import { Panel, SectionTitle } from '../ui/parts.js';
import { color } from '../ui/theme.js';

// Grouped as lazygit's keybindings menu is: what works everywhere, then per panel.
const groups: { title: string; rows: [string, string][] }[] = [
  {
    title: 'GLOBAL',
    rows: [
      ['1-4  ← →  h l  Tab  ⇧Tab', 'jump to, or cycle, the panels'],
      ['↑ ↓  j k  PgUp PgDn', 'move in the focused panel'],
      ['g  G  Home  End', 'first, last'],
      ['J  K', 'scroll the detail panel'],
      ['Enter', 'on Status: open the selected key'],
      ['n  r', 'new key; refresh now'],
      ['q  Ctrl+C', 'quit (this help: Esc or ? closes)'],
    ],
  },
  {
    title: 'KEYS',
    rows: [
      ['/   Esc', 'filter by name, id, tag or upstream; clear it'],
      ['d  e', 'disable, enable the key (y confirms, n cancels)'],
      ['c', 'the addresses a key was used from'],
      ['x', 'revoke the key (type its name to confirm)'],
      ['Ctrl+S', 'in the new-key form: create now'],
      ['c  Enter', 'on the new key screen: copy it, then done'],
    ],
  },
];

/** `focus` is the focused panel: its keys come first, as in lazygit's menu. */
export function HelpDialog({ onClose, focus }: { onClose: () => void; focus?: Tab }) {
  const ordered = focus === 'keys' ? [groups[1]!, groups[0]!] : groups;
  useInput((_i, key) => {
    if (key.escape || key.return || _i === '?') onClose();
  });
  return (
    <Panel title="Keybindings" width={76} tone={color.accent}>
      {ordered.map((g, i) => (
        <Box key={g.title} flexDirection="column" marginTop={i === 0 ? 0 : 1}>
          <SectionTitle>{focus === 'keys' && g.title === 'KEYS' ? 'KEYS (this panel)' : g.title}</SectionTitle>
          {g.rows.map(([k, what]) => (
            <Text key={k}>
              <Text bold color={color.key}>
                {k.padEnd(22)}
              </Text>
              {what}
            </Text>
          ))}
        </Box>
      ))}
      <Box marginTop={1} flexDirection="column">
        <SectionTitle>MARKS</SectionTitle>
        <Text dimColor>● active  ◐ disabled  ✖ revoked or danger  ▲ warning  ○ note  ◆ new entry  ◌ no link</Text>
      </Box>
    </Panel>
  );
}
