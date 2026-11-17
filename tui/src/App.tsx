import { Box, Text, useApp, useInput, useWindowSize } from 'ink';
import { useEffect, useMemo, useState } from 'react';
import type { AdminClient, CreatedKey, CreateKeyInput, Key } from './api.js';
import { CreateKeyDialog } from './dialogs/CreateKeyDialog.js';
import { HelpDialog } from './dialogs/HelpDialog.js';
import { RevokeDialog } from './dialogs/RevokeDialog.js';
import { SecretDialog } from './dialogs/SecretDialog.js';
import { keyLabel, visibleKeys } from './keys.js';
import { attention } from './risk.js';
import { Footer, Header, TABS, Tabs } from './ui/chrome.js';
import type { HintItem, Notice, Tab } from './ui/chrome.js';
import { Panel } from './ui/parts.js';
import { editText } from './ui/TextField.js';
import { color } from './ui/theme.js';
import { useAdmin } from './useAdmin.js';
import { AuditView } from './views/AuditView.js';
import { KeysView } from './views/KeysView.js';
import { OverviewView } from './views/OverviewView.js';
import { UpstreamsView } from './views/UpstreamsView.js';

export interface AppProps {
  client: AdminClient;
  /** The admin address, for the header. Never the token. */
  url: string;
  intervalMs?: number;
  /** How long a success notice stays, in milliseconds. */
  noticeMs?: number;
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
  | { kind: 'help' };

// The classic terminal size. Every view and dialog fits it.
export const MIN_COLUMNS = 80;
export const MIN_ROWS = 24;
// Header, tabs and their rule, notice and hints.
const CHROME_ROWS = 5;

export function App({ client, url, intervalMs, size, copy, noticeMs = 6000 }: AppProps) {
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
  const [busy, setBusy] = useState(false);
  const [dialogError, setDialogError] = useState<string | undefined>();
  const [notice, setNotice] = useState<Notice | undefined>();

  // A success fades. A failure stays until the next key press: someone who looks away
  // must not come back believing a key was disabled when it was not.
  useEffect(() => {
    if (!notice || notice.tone === 'error') return;
    const t = setTimeout(() => setNotice(undefined), noticeMs);
    return () => clearTimeout(t);
  }, [notice, noticeMs]);

  const visible = useMemo(() => visibleKeys(data.keys, filter), [data.keys, filter]);
  const selectedId = visible.some((k) => k.id === keySel) ? keySel : visible[0]?.id;
  const selectedKey = visible.find((k) => k.id === selectedId);
  const needs = useMemo(() => attention(data.keys, now), [data.keys, now]);
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
  async function call(fn: () => Promise<unknown>): Promise<string | undefined> {
    setBusy(true);
    try {
      await fn();
      return undefined;
    } catch (err) {
      return err instanceof Error ? err.message : String(err);
    } finally {
      setBusy(false);
    }
  }

  async function confirmToggle(action: 'disable' | 'enable', key: Key) {
    setMode({ kind: 'none' });
    const err = await call(() => (action === 'disable' ? client.disableKey(key.id) : client.enableKey(key.id)));
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
    setDialogError(undefined);
    const err = await call(() => client.revokeKey(key.id));
    if (err) return setDialogError(err);
    setMode({ kind: 'none' });
    setNotice({ tone: 'ok', text: `Revoked ${keyLabel(key)}. It cannot be used again.` });
    void refresh();
  }

  async function create(input: CreateKeyInput) {
    setDialogError(undefined);
    let created: CreatedKey | undefined;
    const err = await call(async () => {
      created = await client.createKey(input);
    });
    if (err || !created) return setDialogError(err ?? 'kman did not return the key');
    setKeySel(created.key.id);
    setFilter('');
    setMode({ kind: 'secret', created });
    void refresh();
  }

  function request(action: 'disable' | 'enable' | 'revoke') {
    const k = selectedKey;
    if (!k) return;
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
      // The dialogs read their own keys.
      if (mode.kind === 'create' || mode.kind === 'revoke' || mode.kind === 'secret' || mode.kind === 'help') return;
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
      if (key.pageUp) return move(-10);
      if (key.pageDown) return move(10);
      if (key.home || input === 'g') return move('first');
      if (key.end || input === 'G') return move('last');

      if (input === 'n') return setMode({ kind: 'create' });
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
      }
    },
  );

  if (tooSmall) {
    return (
      <Box width={columns} height={rows} alignItems="center" justifyContent="center">
        <Text color={color.warn}>
          Terminal too small: kman needs {MIN_COLUMNS}×{MIN_ROWS}, this is {columns}×{rows}. (q quits)
        </Text>
      </Box>
    );
  }

  const width = columns - 2;
  const height = rows - CHROME_ROWS;

  let body;
  if (link.state === 'denied') {
    body = (
      <Centered width={width} height={height}>
        <Panel title="The admin API refused this token" width={66} tone={color.danger}>
          <Box flexDirection="column" marginTop={1}>
            <Text>{link.error}</Text>
            <Text dimColor>The token may be revoked or rotated. Save a current one with: kman login</Text>
            <Text dimColor>Or set KMAN_ADMIN_TOKEN, then start this again.</Text>
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
          busy={busy}
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
          busy={busy}
          error={dialogError}
          onConfirm={() => void revoke(mode.key)}
          onCancel={() => setMode({ kind: 'none' })}
        />
      </Centered>
    );
  } else if (mode.kind === 'secret') {
    body = (
      <Centered width={width} height={height}>
        <SecretDialog created={mode.created} width={width} onCopy={copy} onClose={() => setMode({ kind: 'none' })} />
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
            {mode.action === 'disable'
              ? `Disable ${keyLabel(mode.key)}? It is refused from its next request, and you can enable it again.`
              : `Enable ${keyLabel(mode.key)}? It works again from its next request.`}
          </Text>
          <Text>
            <Text bold color={color.accent}>
              y
            </Text>
            <Text dimColor> yes   </Text>
            <Text bold color={color.accent}>
              n
            </Text>
            <Text dimColor> cancel</Text>
          </Text>
        </Box>
      ) : (
        <Footer
          notice={notice}
          hints={
            loaded && link.state !== 'denied'
              ? hintsFor(tab, mode.kind, selectedKey, filter !== '', needs.length > 0)
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

function hintsFor(tab: Tab, mode: Mode['kind'], key: Key | undefined, filtered: boolean, anyAttention: boolean): HintItem[] {
  if (mode === 'filter') {
    return [
      { k: 'Enter', label: 'keep filter' },
      { k: 'Esc', label: 'clear' },
      { k: '↑↓', label: 'select' },
    ];
  }
  // Each dialog shows its own keys.
  if (mode === 'secret' || mode === 'revoke' || mode === 'create') return [];
  if (mode !== 'none') return [{ k: 'Esc', label: 'close' }];
  const common: HintItem[] = [
    { k: '1-4', label: 'views' },
    { k: 'r', label: 'refresh' },
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
      { k: '↑↓', label: 'select', always: true },
      { k: 'n', label: 'new key' },
      ...act,
      ...(key && key.status !== 'revoked' ? [{ k: 'x', label: 'revoke' }] : []),
      ...(filtered ? [{ k: 'Esc', label: 'clear filter' }] : [{ k: '/', label: 'filter' }]),
      ...common,
    ];
  }
  if (tab === 'overview') {
    return [
      ...(anyAttention ? [{ k: '↑↓', label: 'select', always: true }, { k: 'Enter', label: 'open key' }] : []),
      { k: 'n', label: 'new key' },
      ...common,
    ];
  }
  return [{ k: '↑↓', label: 'select' }, { k: 'n', label: 'new key' }, ...common];
}
