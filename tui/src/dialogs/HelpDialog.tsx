import { Box, Text, useInput } from 'ink';
import type { Tab } from '../ui/chrome.js';
import { Panel, SectionTitle } from '../ui/parts.js';
import { color, okGlyph, severityGlyph, statusStyle } from '../ui/theme.js';

// Grouped as lazygit's keybindings menu is: what works everywhere, then per panel.
const KEY_W = 24;
const groups: { title: string; panel?: Tab; rows: [string, string][] }[] = [
  {
    title: 'GLOBAL',
    rows: [
      ['1-5  h l  ← →', 'jump to a panel; step to the next or previous'],
      ['Tab  ⇧Tab', 'next panel, previous panel'],
      ['↑ ↓  j k  PgUp PgDn', 'move in the focused panel'],
      ['g  G  Home  End', 'first, last'],
      ['Enter', 'focus the detail, when it has more than it shows'],
      ['J  K', 'scroll the detail panel without leaving the list'],
      ['n  r', 'new key; refresh now'],
      ['q  Ctrl+C', 'quit (this help: Esc or ? closes)'],
    ],
  },
  { title: 'STATUS', panel: 'status', rows: [['Enter', 'open an incident, else the key or upstream']] },
  {
    title: 'KEYS',
    panel: 'keys',
    rows: [
      ['/   Esc', 'filter by name, id, tag or upstream; clear it'],
      ['Enter', 'open its incident, else focus a longer detail'],
      ['d  e', 'disable, enable the key (y confirms, n cancels)'],
      ['v', "the key's requests"],
      ['c', 'the IP addresses a key was used from'],
      ['x', 'revoke the key (type its name to confirm)'],
    ],
  },
  {
    title: 'REQUESTS',
    panel: 'requests',
    rows: [
      ['↑ ↓  j k  PgUp PgDn', 'move; past the end loads older ones'],
      ['g  G  Home  End', 'newest; oldest loaded'],
      ['f', 'all, flagged, refused'],
      ['Enter', "open the request's incident, else its key"],
      ['Esc', 'back to all keys'],
    ],
  },
];

/** What the flags of the requests table claim, and what they cannot. */
const FLAG_NOTES = [
  '! a canary key, or like what an open incident saw (maybe your own use).',
  '▲ refused, or unknown key. Never !: what no rule raised (learning, a known',
  'source), resolved incidents, and detections past the 100 listed.',
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

const INCIDENT = {
  title: 'INCIDENT',
  rows: [
    ['↑ ↓  j k  PgUp PgDn', 'scroll'],
    ['a', 'acknowledge: it stays open'],
    ['m', 'this was me: a false positive'],
    ['R', 'resolve, as confirmed or false positive'],
    ['d  e', 'disable (it stays open) or enable the key'],
    ['o  x', 'rotate or revoke the key: it is confirmed'],
    ['z', 'canary: arm it again'],
    ['Esc', 'go back'],
  ] as [string, string][],
};

// What still works while the detail has the keyboard: not the list's own keys.
const DETAIL_GLOBAL = {
  title: 'GLOBAL',
  rows: [
    ['1-5  h l  ← →  Tab', 'go to another panel (the list gets the keyboard)'],
    ['r', 'refresh now'],
    ['q  Ctrl+C', 'quit (this help: Esc or ? closes)'],
  ] as [string, string][],
};

/** `focus` is the focused panel: its keys come first, as in lazygit's menu. */
export function HelpDialog({ onClose, focus, detail, incident }: { onClose: () => void; focus?: Tab; detail?: boolean; incident?: boolean }) {
  // The focused panel's keys first, then what works everywhere: other panels' keys
  // are on their own screens, which this dialog has no room for.
  // With the detail panel focused only its own keys work, so those come first.
  // An incident, like a focused detail, has the keyboard to itself.
  const alone = incident ? INCIDENT : detail ? DETAIL : undefined;
  const mine = alone ? undefined : groups.find((g) => g.panel === focus);
  // On Status Enter opens the selected item, so the list-with-a-detail line does not apply there.
  // The requests table moves and opens its own way, which its group says: only the rest is repeated.
  const unsaid = focus === 'requests' ? ['↑ ↓  j k  PgUp PgDn', 'g  G  Home  End', 'Enter', 'J  K'] : focus === 'status' || focus === 'keys' ? ['Enter'] : [];
  const global = { ...groups[0]!, rows: groups[0]!.rows.filter(([k]) => !unsaid.includes(k)) };
  const ordered = alone ? [alone, DETAIL_GLOBAL] : [...(mine ? [mine] : []), global];
  useInput((_i, key) => {
    if (key.escape || key.return || _i === '?') onClose();
  });
  return (
    <Panel title="Keybindings" width={78} tone={color.accent}>
      {ordered.map((g, i) => (
        <Box key={g.title} flexDirection="column" marginTop={i === 0 ? 0 : 1}>
          <SectionTitle>{g === mine ? `${g.title} (this panel)` : g.title}</SectionTitle>
          {g.rows.map(([k, what]) => (
            // The meaning wraps under itself, not under the key.
            <Box key={k + what}>
              <Box width={KEY_W} flexShrink={0}>
                <Text bold color={color.key}>
                  {k}
                </Text>
              </Box>
              <Box flexGrow={1} flexShrink={1}>
                <Text wrap="wrap">{what}</Text>
              </Box>
            </Box>
          ))}
        </Box>
      ))}
      {mine?.panel === 'requests' ? (
        <Box marginTop={1} flexDirection="column">
          {FLAG_NOTES.map((l) => (
            <Text key={l} dimColor>
              {l}
            </Text>
          ))}
        </Box>
      ) : null}
      <Box marginTop={1} flexDirection="column">
        <SectionTitle>MARKS</SectionTitle>
        <Text dimColor>
          {statusStyle.active.glyph} active   {statusStyle.disabled.glyph} disabled   {statusStyle.revoked.glyph} revoked   {okGlyph} ok
        </Text>
        <Text dimColor>
          {severityGlyph.danger} danger   {severityGlyph.warn} warning   {severityGlyph.info} note or message   ◆ new entry   ◌ not connected
        </Text>
      </Box>
    </Panel>
  );
}
