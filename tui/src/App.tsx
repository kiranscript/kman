import { Box, Text, useApp, useInput, useWindowSize } from 'ink';
import { useEffect, useMemo, useRef, useState } from 'react';
import type { AdminClient, CreatedKey, CreateKeyInput, Key } from './api.js';
import { ClientsDialog } from './dialogs/ClientsDialog.js';
import { CreateKeyDialog } from './dialogs/CreateKeyDialog.js';
import { HelpDialog } from './dialogs/HelpDialog.js';
import { RevokeDialog } from './dialogs/RevokeDialog.js';
import { SecretDialog } from './dialogs/SecretDialog.js';
import { fit } from './format.js';
import { keyLabel, visibleKeys } from './keys.js';
import { attention } from './risk.js';
import { Footer, Header, noticeRows, TABS, Tabs } from './ui/chrome.js';
import type { HintItem, Notice, Tab } from './ui/chrome.js';
import { Panel } from './ui/parts.js';
import { editText } from './ui/TextField.js';
import { color } from './ui/theme.js';
import { useAdmin } from './useAdmin.js';
import { AuditView, auditRows } from './views/AuditView.js';
import { KeysView, keyListRows } from './views/KeysView.js';
import { OverviewView, attentionRows } from './views/OverviewView.js';
import { UpstreamsView, upstreamRows } from './views/UpstreamsView.js';

export interface AppProps {
  client: AdminClient;
  /** The admin address, for the header. Never the token. */
  url: string;
  intervalMs?: number;
  /** How long a success notice stays, in milliseconds. */
  noticeMs?: number;
  /** How long Enter is ignored on the one-time secret screen, in milliseconds. */
  secretGuardMs?: number;
  /** Puts text on the clipboard; defaults to asking the terminal (OSC 52). */
  copy?: (text: string) => void;
  /** Fixes the size, instead of reading the terminal's. */
  size?: { columns: number; rows: number };
}

type Mode =
  | { kind: 'none' }
  | { kind: 'filter' }
  | { kind: 'confirm'; action: 'disable' | 'enable'; key: Key }
  | { kind: 'create' }
  | { kind: 'revoke'; key: Key }
  | { kind: 'secret'; created: CreatedKey }
  /** The addresses a key was used from; the admin API gives them out only if the install allows it. */
  | { kind: 'clients'; key: Key }
  | { kind: 'help' };

// The classic terminal size. Every view and dialog fits it.
export const MIN_COLUMNS = 80;
export const MIN_ROWS = 24;
// Header, tabs and their rule, notice and hints.
const CHROME_ROWS = 5;

export function App({ client, url, intervalMs, size, copy, noticeMs = 6000, secretGuardMs }: AppProps) {
  const { exit } = useApp();
  const win = useWindowSize();
  const { columns, rows } = size ?? win;
  const { data, link, now, fresh, refresh } = useAdmin(client, intervalMs);

  const [tab, setTab] = useState<Tab>('overview');
  const [mode, setMode] = useState<Mode>({ kind: 'none' });
  const [filter, setFilter] = useState('');
  const [keySel, setKeySel] = useState<string | undefined>();
  const [attentionSel, setAttentionSel] = useState(0);
  const [upstreamSel, setUpstreamSel] = useState(0);
  const [auditSel, setAuditSel] = useState(0);
  // What is being sent right now, so only the dialog that sent it shows "working".
  const [working, setWorking] = useState<'toggle' | 'revoke' | 'create' | undefined>();
  const doing = useRef('');
  // State is only read on the next render, so two quick key presses would both see
  // "not busy". This is read at once.
  const acting = useRef(false);
  const [dialogError, setDialogError] = useState<string | undefined>();
  const [notice, setNotice] = useState<Notice | undefined>();

  // A success fades. A failure stays until the next key press: someone who looks away
  // must not come back believing a key was disabled when it was not.
  useEffect(() => {
    // A notice about a change under way stays while it is: it is replaced by its result.
    if (!notice || notice.tone === 'error' || working !== undefined) return;
    const t = setTimeout(() => setNotice(undefined), noticeMs);
    return () => clearTimeout(t);
  }, [notice, noticeMs, working]);

  const visible = useMemo(() => visibleKeys(data.keys, filter), [data.keys, filter]);
  const selectedId = visible.some((k) => k.id === keySel) ? keySel : visible[0]?.id;
  const selectedKey = visible.find((k) => k.id === selectedId);
  const needs = useMemo(() => attention(data.keys, now), [data.keys, now]);
  const width = columns - 2;
  // A long failure takes more rows, which the body gives up.
  const shownNotice = link.state === 'denied' ? undefined : notice;
  const height = rows - CHROME_ROWS - (noticeRows(shownNotice, width) - 1);
  const tooSmall = columns < MIN_COLUMNS || rows < MIN_ROWS;
  const loaded = link.state === 'ok' || data.keys.length > 0 || data.upstreams.length > 0 || data.audit.length > 0;
  const usable = loaded && link.state !== 'denied';

  // The list re-sorts as keys change state, so the selection is remembered by id and
  // follows its key. It starts as the first row; this pins that choice on the first
  // load. It only ever fills an empty selection: an effect that overwrote a chosen
  // key would race with the key press that chose it.
  useEffect(() => {
    if (selectedId) setKeySel((cur) => cur ?? selectedId);
  }, [selectedId]);

  /** Rows of the current list on screen at once, so a page moves by what was shown. */
  function pageRows(): number {
    if (tab === 'keys') return keyListRows(visible, now, width, height);
    if (tab === 'overview') return attentionRows(needs.length, width, height);
    if (tab === 'upstreams') return upstreamRows(data.upstreams.length, height);
    return auditRows(height);
  }

  function move(to: number | 'first' | 'last') {
    const step = (cur: number, count: number) =>
      count === 0 ? 0 : Math.max(0, Math.min(count - 1, to === 'first' ? 0 : to === 'last' ? count - 1 : cur + to));
    if (tab === 'keys') {
      const cur = Math.max(0, visible.findIndex((k) => k.id === selectedId));
      setKeySel(visible[step(cur, visible.length)]?.id);
    } else if (tab === 'overview') {
      setAttentionSel((c) => step(c, needs.length));
    } else if (tab === 'upstreams') {
      setUpstreamSel((c) => step(c, data.upstreams.length));
    } else if (tab === 'audit') {
      setAuditSel((c) => step(c, data.audit.length));
    }
  }

  function goTab(t: Tab) {
    setTab(t);
  }

  /** Runs one admin API call; resolves to the error message, or undefined on success. */
  async function call(kind: 'toggle' | 'revoke' | 'create', what: string, fn: () => Promise<unknown>): Promise<string | undefined> {
    acting.current = true;
    doing.current = what;
    setWorking(kind);
    try {
      await fn();
      return undefined;
    } catch (err) {
      return err instanceof Error ? err.message : String(err);
    } finally {
      acting.current = false;
      setWorking(undefined);
    }
  }

  async function confirmToggle(action: 'disable' | 'enable', key: Key) {
    if (acting.current) return;
    setMode({ kind: 'none' });
    setNotice({ tone: 'info', text: `${action === 'disable' ? 'Disabling' : 'Enabling'} ${keyLabel(key)}…` });
    const err = await call('toggle', `${action === 'disable' ? 'disabling' : 'enabling'} ${keyLabel(key)}`, () =>
      action === 'disable' ? client.disableKey(key.id) : client.enableKey(key.id),
    );
    if (err) setNotice({ tone: 'error', text: `Could not ${action} ${keyLabel(key)}: ${err}` });
    else
      setNotice({
        tone: 'ok',
        text:
          action === 'disable'
            ? `Disabled ${keyLabel(key)}. It is refused from its next request.`
            : `Enabled ${keyLabel(key)}.`,
      });
    void refresh();
  }

  async function revoke(key: Key) {
    if (acting.current) return;
    setDialogError(undefined);
    const err = await call('revoke', `revoking ${keyLabel(key)}`, () => client.revokeKey(key.id));
    if (err) return setDialogError(err);
    setMode({ kind: 'none' });
    setNotice({ tone: 'ok', text: `Revoked ${keyLabel(key)}. It cannot be used again.` });
    void refresh();
  }

  async function create(input: CreateKeyInput) {
    if (acting.current) return;
    setDialogError(undefined);
    let created: CreatedKey | undefined;
    const err = await call('create', 'creating a key', async () => {
      created = await client.createKey(input);
    });
    if (err || !created) return setDialogError(err ?? 'kman did not return the key');
    setKeySel(created.key.id);
    setFilter('');
    setMode({ kind: 'secret', created });
    void refresh();
  }

  /** One change at a time: a second would race the first, and its dialog would claim work that is not its own. */
  function busyNow(): boolean {
    if (!acting.current) return false;
    setNotice({ tone: 'info', text: `Wait: still ${doing.current}…` });
    return true;
  }

  /** Changes need kman to answer, so while it does not, say so instead of opening a dialog that must fail. */
  function offline(): boolean {
    if (link.state !== 'down') return false;
    setNotice({ tone: 'error', text: 'kman is not answering, so nothing can be changed now. r tries again.' });
    return true;
  }

  function request(action: 'disable' | 'enable' | 'revoke') {
    const k = selectedKey;
    if (!k) return;
    if (offline() || busyNow()) return;
    if (k.status === 'revoked') return setNotice({ tone: 'info', text: `${keyLabel(k)} is revoked for good.` });
    if (action === 'disable' && k.status === 'disabled') return setNotice({ tone: 'info', text: `${keyLabel(k)} is already disabled.` });
    if (action === 'enable' && k.status === 'active') return setNotice({ tone: 'info', text: `${keyLabel(k)} is already enabled.` });
    setDialogError(undefined);
    setMode(action === 'revoke' ? { kind: 'revoke', key: k } : { kind: 'confirm', action, key: k });
  }

  useInput(
    (input, key) => {
      if (notice?.tone === 'error') setNotice(undefined);
      // This handler is always on, so the terminal stays in raw mode and a key pressed
      // where nothing listens is dropped instead of echoed and replayed later.
      // Behind the "too small" screen, or before there is anything to show, nothing
      // can be seen, so nothing may be done.
      if (tooSmall || !usable) {
        if (input === 'q') exit();
        else if (input === 'r' && !tooSmall) void refresh();
        return;
      }
      if ((mode.kind === 'help' || mode.kind === 'clients') && input === 'q') return exit();
      // The dialogs read their own keys.
      if (mode.kind === 'create' || mode.kind === 'revoke' || mode.kind === 'secret' || mode.kind === 'help' || mode.kind === 'clients') return;
      if (mode.kind === 'filter') {
        // Leaving the filter keeps whichever key it left selected.
        if (key.escape) {
          setKeySel(selectedId);
          setFilter('');
          return setMode({ kind: 'none' });
        }
        if (key.return) {
          setKeySel(selectedId);
          return setMode({ kind: 'none' });
        }
        if (key.upArrow) return move(-1);
        if (key.downArrow) return move(1);
        return setFilter((f) => editText(f, input, key, 40));
      }
      if (mode.kind === 'confirm') {
        if (input === 'y' || input === 'Y') void confirmToggle(mode.action, mode.key);
        else if (input === 'n' || input === 'N' || key.escape) setMode({ kind: 'none' });
        return;
      }

      if (input === 'q') return exit();
      if (input === '?') return setMode({ kind: 'help' });
      if (input === 'r') {
        setNotice({ tone: 'info', text: 'Refreshing…' });
        void refresh().then((why) =>
          setNotice(why ? { tone: 'error', text: `Could not refresh: ${why}` } : { tone: 'ok', text: 'Refreshed.' }),
        );
        return;
      }
      const n = Number(input);
      if (n >= 1 && n <= TABS.length) return goTab(TABS[n - 1]!.id);
      const at = TABS.findIndex((t) => t.id === tab);
      if (key.tab) return goTab(TABS[(at + (key.shift ? TABS.length - 1 : 1)) % TABS.length]!.id);
      if (key.rightArrow) return goTab(TABS[(at + 1) % TABS.length]!.id);
      if (key.leftArrow) return goTab(TABS[(at + TABS.length - 1) % TABS.length]!.id);

      if (key.upArrow || input === 'k') return move(-1);
      if (key.downArrow || input === 'j') return move(1);
      // A page is what the list shows, less one row, so one row of the old page is still in view.
      if (key.pageUp) return move(-Math.max(1, pageRows() - 1));
      if (key.pageDown) return move(Math.max(1, pageRows() - 1));
      if (key.home || input === 'g') return move('first');
      if (key.end || input === 'G') return move('last');

      if (input === 'n') return offline() || busyNow() ? undefined : setMode({ kind: 'create' });
      if (tab === 'overview' && key.return) {
        const target = needs[Math.min(attentionSel, needs.length - 1)];
        if (!target) return;
        setFilter('');
        setKeySel(target.key.id);
        return goTab('keys');
      }
      if (tab === 'keys') {
        if (input === '/') return setMode({ kind: 'filter' });
        if (key.escape && filter) {
          setKeySel(selectedId);
          return setFilter('');
        }
        if (input === 'd') return request('disable');
        if (input === 'e') return request('enable');
        if (input === 'x') return request('revoke');
        // Reading addresses needs the server, but is not a change, so a running change does not stop it.
        if (input === 'c') return selectedKey && !offline() ? setMode({ kind: 'clients', key: selectedKey }) : undefined;
      }
    },
  );

  if (tooSmall) {
    return (
      <Box width={columns} height={rows} alignItems="center" justifyContent="center" flexDirection="column">
        <Text color={color.warn}>Terminal too small</Text>
        <Text color={color.warn}>
          kman needs {MIN_COLUMNS}×{MIN_ROWS}; this is {columns}×{rows}.
        </Text>
        <Text dimColor>q quits</Text>
      </Box>
    );
  }

  const selectable =
    tab === 'overview' ? needs.length > 0 : tab === 'keys' ? visible.length > 0 : tab === 'upstreams' ? data.upstreams.length > 0 : data.audit.length > 0;


  let body;
  if (link.state === 'denied') {
    body = (
      <Centered width={width} height={height}>
        <Panel title="The admin API refused this token" width={66} tone={color.danger}>
          <Box flexDirection="column" marginTop={1}>
            <Text>{link.error}</Text>
            <Text dimColor>The token may be revoked or rotated.</Text>
            <Text dimColor>Save a current one with kman login, or set KMAN_ADMIN_TOKEN,</Text>
            <Text dimColor>then start this again.</Text>
          </Box>
        </Panel>
      </Centered>
    );
  } else if (!loaded) {
    body = (
      <Centered width={width} height={height}>
        {link.state === 'down' ? (
          <Panel title="Cannot reach kman" width={66} tone={color.warn}>
            <Box flexDirection="column" marginTop={1}>
              <Text>{link.error}</Text>
              <Text dimColor>Is it running? Start it with: kman serve</Text>
              <Text dimColor>Retrying every few seconds. r retries now, q quits.</Text>
            </Box>
          </Panel>
        ) : (
          <Text dimColor>Connecting to {url}…</Text>
        )}
      </Centered>
    );
  } else if (mode.kind === 'create') {
    body = (
      <Centered width={width} height={height}>
        <CreateKeyDialog
          upstreams={data.upstreams}
          defaultDays={data.settings ? Math.round(data.settings.default_key_ttl_seconds / 86_400) : undefined}
          busy={working === 'create'}
          // The form is 12 rows without its error; whatever the body has left is for the error.
          errorLines={Math.max(3, height - 12)}
          error={dialogError}
          onSubmit={(i) => void create(i)}
          onCancel={() => setMode({ kind: 'none' })}
        />
      </Centered>
    );
  } else if (mode.kind === 'revoke') {
    body = (
      <Centered width={width} height={height}>
        <RevokeDialog
          keyData={mode.key}
          maxWidth={width}
          busy={working === 'revoke'}
          // The dialog is at most 14 rows without its error (its hint line included).
          errorLines={Math.max(3, height - 14)}
          error={dialogError}
          onConfirm={() => void revoke(mode.key)}
          onCancel={() => setMode({ kind: 'none' })}
        />
      </Centered>
    );
  } else if (mode.kind === 'secret') {
    body = (
      <Centered width={width} height={height}>
        <SecretDialog created={mode.created} width={width} onCopy={copy} armMs={secretGuardMs} onClose={() => setMode({ kind: 'none' })} />
      </Centered>
    );
  } else if (mode.kind === 'clients') {
    body = (
      <Centered width={width} height={height}>
        <ClientsDialog client={client} keyData={mode.key} width={width} height={height} now={now} onClose={() => setMode({ kind: 'none' })} />
      </Centered>
    );
  } else if (mode.kind === 'help') {
    body = (
      <Centered width={width} height={height}>
        <HelpDialog onClose={() => setMode({ kind: 'none' })} />
      </Centered>
    );
  } else if (tab === 'overview') {
    body = (
      <OverviewView
        keys={data.keys}
        upstreams={data.upstreams}
        audit={data.audit}
        price={data.price}
        needs={needs}
        selected={attentionSel}
        width={width}
        height={height}
        now={now}
      />
    );
  } else if (tab === 'keys') {
    body = (
      <KeysView
        keys={visible}
        total={data.keys.length}
        selectedId={selectedId}
        filter={filter}
        filtering={mode.kind === 'filter'}
        hasUpstreams={data.upstreams.length > 0}
        width={width}
        height={height}
        now={now}
      />
    );
  } else if (tab === 'upstreams') {
    body = <UpstreamsView upstreams={data.upstreams} keys={data.keys} selected={upstreamSel} width={width} height={height} />;
  } else {
    body = <AuditView audit={data.audit} names={{ keys: data.keys, upstreams: data.upstreams }} fresh={fresh} selected={auditSel} width={width} height={height} now={now} />;
  }

  return (
    <Box flexDirection="column" width={columns} height={rows} paddingX={1}>
      <Header link={link} url={url} actor={data.actor} width={width} />
      <Tabs
        active={tab}
        width={width}
        warnings={needs.filter((n) => n.top !== 'info').length}
        infos={needs.filter((n) => n.top === 'info').length}
        counts={loaded ? { keys: data.keys.length, upstreams: data.upstreams.length } : {}}
      />
      <Box height={height} width={width}>
        {body}
      </Box>
      {mode.kind === 'confirm' ? (
        <Box flexDirection="column">
          <Text color={color.warn} wrap="truncate-end">
            {confirmQuestion(mode.action, mode.key, width)}
          </Text>
          <Text>
            <Text bold color={color.accent}>
              y
            </Text>
            <Text dimColor> yes   </Text>
            <Text bold color={color.accent}>
              n
            </Text>
            <Text dimColor> cancel   </Text>
            <Text dimColor>
              {mode.action === 'disable' ? 'Requests with it are refused; e re-enables.' : 'It works again from its next request.'}
            </Text>
          </Text>
        </Box>
      ) : (
        <Footer
          notice={shownNotice}
          hints={
            link.state === 'denied'
              ? [{ k: 'q', label: 'quit' }]
              : loaded
                ? hintsFor({
                    tab,
                    mode: mode.kind,
                    key: selectedKey,
                    filtered: filter !== '',
                    selectable,
                    offline: link.state === 'down',
                    working: working !== undefined,
                    canCreate: data.upstreams.length > 0,
                    hasKeys: data.keys.length > 0,
                  })
                : [{ k: 'r', label: 'retry' }, { k: 'q', label: 'quit' }]
          }
          width={width}
        />
      )}
    </Box>
  );
}

function Centered({ width, height, children }: { width: number; height: number; children: React.ReactNode }) {
  return (
    <Box width={width} height={height} alignItems="center" justifyContent="center">
      {children}
    </Box>
  );
}

/** "Disable checkout (k_ab12)?": the id tells apart two keys whose names start alike. */
function confirmQuestion(action: 'disable' | 'enable', key: Key, width: number): string {
  const verb = action === 'disable' ? 'Disable' : 'Enable';
  const label = keyLabel(key);
  const tail = label === key.id ? '?' : ` (${key.id})?`;
  return `${verb} ${fit(label, Math.max(12, width - verb.length - 1 - tail.length))}${tail}`;
}

interface HintState {
  tab: Tab;
  mode: Mode['kind'];
  key: Key | undefined;
  filtered: boolean;
  /** Whether the view has rows to move over. */
  selectable: boolean;
  offline: boolean;
  /** A change is being sent: another cannot start. */
  working: boolean;
  /** An upstream exists, so a key can be made. */
  canCreate: boolean;
  hasKeys: boolean;
}

/** Only keys that would do something are offered. */
function hintsFor({ tab, mode, key, filtered, selectable, offline, working, canCreate, hasKeys }: HintState): HintItem[] {
  const canChange = !offline && !working;
  if (mode === 'filter') {
    return [
      { k: 'Enter', label: 'keep filter' },
      { k: 'Esc', label: 'clear' },
      { k: '↑↓', label: 'select' },
    ];
  }
  // Each dialog shows its own keys.
  if (mode === 'secret' || mode === 'revoke' || mode === 'create' || mode === 'clients') return [];
  if (mode !== 'none') return [{ k: 'Esc', label: 'close' }];
  const common: HintItem[] = [
    { k: 'r', label: offline ? 'retry' : 'refresh', always: true },
    { k: '1-4', label: 'views' },
    { k: '?', label: 'help', always: true },
    { k: 'q', label: 'quit', always: true },
  ];
  if (tab === 'keys') {
    const act =
      key?.status === 'active'
        ? [{ k: 'd', label: 'disable' }]
        : key?.status === 'disabled'
          ? [{ k: 'e', label: 'enable' }]
          : [];
    return [
      ...(selectable ? [{ k: '↑↓', label: 'select', always: true }] : []),
      ...(canChange ? [...(canCreate ? [{ k: 'n', label: 'new key' }] : []), ...act, ...(key && key.status !== 'revoked' ? [{ k: 'x', label: 'revoke' }] : [])] : []),
      ...(key && !offline ? [{ k: 'c', label: 'IPs' }] : []),
      ...(filtered ? [{ k: 'Esc', label: 'clear filter' }] : hasKeys ? [{ k: '/', label: 'filter' }] : []),
      ...common,
    ];
  }
  if (tab === 'overview') {
    return [
      ...(selectable ? [{ k: '↑↓', label: 'select', always: true }, { k: 'Enter', label: 'open key' }] : []),
      ...(canChange && canCreate ? [{ k: 'n', label: 'new key' }] : []),
      ...common,
    ];
  }
  return [...(selectable ? [{ k: '↑↓', label: 'select' }] : []), ...(canChange && canCreate ? [{ k: 'n', label: 'new key' }] : []), ...common];
}
