import { Box, Text, useInput } from 'ink';
import type { Tab } from '../ui/chrome.js';
import { Panel, SectionTitle } from '../ui/parts.js';
import { color } from '../ui/theme.js';

// Grouped as lazygit's keybindings menu is: what works everywhere, then per panel.
const KEY_W = 24;
const groups: { title: string; panel?: Tab; rows: [string, string][] }[] = [
  {
    title: 'GLOBAL',
    rows: [
      ['1-4  h l  ← →', 'jump to a panel; step to the next or previous'],
      ['Tab  ⇧Tab', 'next panel, previous panel'],
      ['↑ ↓  j k  PgUp PgDn', 'move in the focused panel'],
      ['g  G  Home  End', 'first, last'],
      ['Enter', 'on Keys, Upstreams, Audit: focus the detail panel'],
      ['J  K', 'scroll the detail panel without leaving the list'],
      ['n  r', 'new key; refresh now'],
      ['q  Ctrl+C', 'quit (this help: Esc or ? closes)'],
    ],
  },
  { title: 'STATUS', panel: 'status', rows: [['Enter', 'open the selected key, or upstream, in its panel']] },
  {
    title: 'KEYS',
    panel: 'keys',
    rows: [
      ['/   Esc', 'filter by name, id, tag or upstream; clear it'],
      ['d  e', 'disable, enable the key (y confirms, n cancels)'],
      ['c', 'the addresses a key was used from'],
      ['x', 'revoke the key (type its name to confirm)'],
    ],
  },
  { title: 'UPSTREAMS', panel: 'upstreams', rows: [['Enter', 'read the whole upstream; they are added with kman upstream add']] },
  { title: 'AUDIT', panel: 'audit', rows: [['Enter', 'read the whole entry; the log is read-only']] },
];

const DETAIL = {
  title: 'DETAIL (focused)',
  rows: [
    ['↑ ↓  j k', 'scroll a line'],
    ['PgUp  PgDn', 'scroll a page'],
    ['g  G  Home  End', 'top, bottom'],
    ['Esc  Enter', 'give the keyboard back to the list'],
  ] as [string, string][],
};

/** `focus` is the focused panel: its keys come first, as in lazygit's menu. */
export function HelpDialog({ onClose, focus, detail }: { onClose: () => void; focus?: Tab; detail?: boolean }) {
  // The focused panel's keys first, then what works everywhere: other panels' keys
  // are on their own screens, which this dialog has no room for.
  // With the detail panel focused only its own keys work, so those come first.
  const mine = detail ? DETAIL : groups.find((g) => g.panel === focus);
  const ordered = [...(mine ? [mine] : []), groups[0]!];
  useInput((_i, key) => {
    if (key.escape || key.return || _i === '?') onClose();
  });
  return (
    <Panel title="Keybindings" width={78} tone={color.accent}>
      {ordered.map((g, i) => (
        <Box key={g.title} flexDirection="column" marginTop={i === 0 ? 0 : 1}>
          <SectionTitle>{g === mine && !detail ? `${g.title} (this panel)` : g.title}</SectionTitle>
          {g.rows.map(([k, what]) => (
            <Text key={k + what}>
              <Text bold color={color.key}>
                {k.padEnd(KEY_W)}
              </Text>
              {what}
            </Text>
          ))}
        </Box>
      ))}
      <Box marginTop={1} flexDirection="column">
        <SectionTitle>MARKS</SectionTitle>
        <Text dimColor>● active   ◐ disabled   ✖ revoked or danger   ▲ warning</Text>
        <Text dimColor>○ note   ◆ new entry   ◌ not connected</Text>
      </Box>
    </Panel>
  );
}
