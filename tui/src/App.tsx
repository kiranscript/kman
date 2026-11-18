import { Box, Text, useApp, useInput, useWindowSize } from 'ink';
import { useEffect, useMemo, useState } from 'react';
import type { AdminClient, CreatedKey, CreateKeyInput, Key } from './api.js';
import { CreateKeyDialog } from './dialogs/CreateKeyDialog.js';
import { HelpDialog } from './dialogs/HelpDialog.js';
import { RevokeDialog } from './dialogs/RevokeDialog.js';
import { SecretDialog } from './dialogs/SecretDialog.js';
import { keyLabel, visibleKeys } from './keys.js';
import { attention } from './risk.js';
import { layout } from './layout.js';
import { Footer, TABS } from './ui/chrome.js';
import type { HintItem, Notice, Tab } from './ui/chrome.js';
import { Hint, Panel } from './ui/parts.js';
import { editText } from './ui/TextField.js';
import { color } from './ui/theme.js';
import { useAdmin } from './useAdmin.js';
import { AuditMain, KeyMain, StatusMain, UpstreamMain } from './views/detail.js';
import type { Scroll } from './views/detail.js';
import { AuditPanel, KeysPanel, StatusPanel, UpstreamsPanel } from './views/panels.js';

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
// The notice row and the options bar under the panels.
const CHROME_ROWS = 2;

export function App({ client, url, intervalMs, size, copy, noticeMs = 6000 }: AppProps) {
  const { exit } = useApp();
  const win = useWindowSize();
  const { columns, rows } = size ?? win;
  const { data, link, now, fresh, refresh } = useAdmin(client, intervalMs);

  const [tab, setTab] = useState<Tab>('status');
  const [mode, setMode] = useState<Mode>({ kind: 'none' });
  const [filter, setFilter] = useState('');
  const [keySel, setKeySel] = useState<string | undefined>();
  const [attentionSel, setAttentionSel] = useState(0);
  const [upstreamSel, setUpstreamSel] = useState(0);
  const [auditSel, setAuditSel] = useState(0);
  const [busy, setBusy] = useState(false);
  const [dialogError, setDialogError] = useState<string | undefined>();
  const [notice, setNotice] = useState<Notice | undefined>();
  // How far the main panel is scrolled, and how far it can be. It starts from the top
  // again whenever something else is selected.
  const [scrollTop, setScrollTop] = useState(0);
  const [scrollMax, setScrollMax] = useState(0);

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
  const attnSelKey = needs[Math.min(attentionSel, Math.max(0, needs.length - 1))]?.key.id;
  const watching = `${tab}:${tab === 'keys' ? selectedId : tab === 'status' ? attnSelKey : tab === 'upstreams' ? upstreamSel : auditSel}`;
  useEffect(() => setScrollTop(0), [watching]);
  const scroll: Scroll = {
    offset: scrollTop,
    onMax: (m) => setScrollMax((c) => (c === m ? c : m)),
  };
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
    } else if (tab === 'status') {
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
      if (key.rightArrow || input === 'l') return goTab(TABS[(at + 1) % TABS.length]!.id);
      if (key.leftArrow || input === 'h') return goTab(TABS[(at + TABS.length - 1) % TABS.length]!.id);

      if (key.upArrow || input === 'k') return move(-1);
      if (key.downArrow || input === 'j') return move(1);
      if (key.pageUp) return move(-10);
      if (key.pageDown) return move(10);
      if (key.home || input === 'g') return move('first');
      if (key.end || input === 'G') return move('last');

      if (input === 'J') return setScrollTop((t) => Math.min(scrollMax, t + 3));
      if (input === 'K') return setScrollTop((t) => Math.max(0, t - 3));

      if (input === 'n') return setMode({ kind: 'create' });
      if (tab === 'status' && key.return) {
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

  const width = columns;
  const bodyRows = rows - CHROME_ROWS;
  const want =
    tab === 'status'
      ? 7 + needs.length
      : tab === 'keys'
        ? Math.max(1, visible.length) + (filter !== '' || mode.kind === 'filter' ? 1 : 0)
        : tab === 'upstreams'
          ? data.upstreams.length
          : data.audit.length;
  const lay = layout(columns, bodyRows, tab, want);

  let body;
  if (link.state === 'denied') {
    body = (
      <Centered width={width} height={bodyRows}>
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
      <Centered width={width} height={bodyRows}>
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
      <Centered width={width} height={bodyRows}>
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
      <Centered width={width} height={bodyRows}>
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
      <Centered width={width} height={bodyRows}>
        <SecretDialog created={mode.created} width={width} onCopy={copy} onClose={() => setMode({ kind: 'none' })} />
      </Centered>
    );
  } else if (mode.kind === 'help') {
    body = (
      <Centered width={width} height={bodyRows}>
        <HelpDialog focus={tab} onClose={() => setMode({ kind: 'none' })} />
      </Centered>
    );
  } else if (mode.kind === 'confirm') {
    body = (
      <Centered width={width} height={bodyRows}>
        <Panel title={`${mode.action === 'disable' ? 'Disable' : 'Enable'} this key?`} width={Math.min(70, width)} tone={color.warn}>
          <Box flexDirection="column" marginTop={1}>
            {/* Never cut: two names can differ only at the end. */}
            <Text bold wrap="wrap">
              {keyLabel(mode.key)}
            </Text>
            <Text dimColor wrap="wrap">
              id {mode.key.id} · {mode.key.upstream ?? 'no upstream'}
            </Text>
          </Box>
          <Box marginTop={1}>
            <Text wrap="wrap">
              {mode.action === 'disable'
                ? 'It is refused from its next request, and you can enable it again.'
                : 'It works again from its next request.'}
            </Text>
          </Box>
          <Box marginTop={1}>
            <Hint k="y">yes</Hint>
            <Text>{'   '}</Text>
            <Hint k="n">cancel</Hint>
          </Box>
        </Panel>
      </Centered>
    );
  } else {
    const h = lay.heights;
    const names = { keys: data.keys, upstreams: data.upstreams };
    const upSel = Math.min(upstreamSel, Math.max(0, data.upstreams.length - 1));
    const auSel = Math.min(auditSel, Math.max(0, data.audit.length - 1));
    const left = (
      <Box flexDirection="column" width={lay.leftW} height={lay.wide ? bodyRows : undefined} flexShrink={0}>
        <StatusPanel
          width={lay.leftW}
          height={h.status}
          focused={tab === 'status'}
          link={link}
          url={url}
          actor={data.actor}
          keys={data.keys}
          needs={needs}
          selected={attentionSel}
          price={data.price}
        />
        <KeysPanel
          width={lay.leftW}
          height={h.keys}
          focused={tab === 'keys'}
          keys={visible}
          total={data.keys.length}
          selectedId={selectedId}
          filter={filter}
          filtering={mode.kind === 'filter'}
          now={now}
        />
        <UpstreamsPanel
          width={lay.leftW}
          height={h.upstreams}
          focused={tab === 'upstreams'}
          upstreams={data.upstreams}
          keys={data.keys}
          selected={upstreamSel}
        />
        <AuditPanel
          width={lay.leftW}
          height={h.audit}
          focused={tab === 'audit'}
          audit={data.audit}
          names={names}
          fresh={fresh}
          selected={auditSel}
          now={now}
        />
      </Box>
    );
    const attn = needs[Math.min(attentionSel, Math.max(0, needs.length - 1))]?.key;
    const main =
      tab === 'status' ? (
        attn ? (
          <KeyMain key={attn.id} keyData={attn} width={lay.mainW} height={lay.mainH} now={now} compact={!lay.wide} scroll={scroll} />
        ) : (
          <StatusMain width={lay.mainW} height={lay.mainH} audit={data.audit} names={names} now={now} scroll={scroll} />
        )
      ) : tab === 'keys' ? (
        <KeyMain keyData={selectedKey} width={lay.mainW} height={lay.mainH} now={now} compact={!lay.wide} scroll={scroll} />
      ) : tab === 'upstreams' ? (
        <UpstreamMain upstream={data.upstreams[upSel]} keys={data.keys} width={lay.mainW} height={lay.mainH} scroll={scroll} />
      ) : (
        <AuditMain entry={data.audit[auSel]} names={names} width={lay.mainW} height={lay.mainH} scroll={scroll} />
      );
    body = (
      <Box width={width} height={bodyRows} flexDirection={lay.wide ? 'row' : 'column'}>
        {left}
        {main}
      </Box>
    );
  }

  return (
    <Box flexDirection="column" width={columns} height={rows}>
      <Box width={width} height={bodyRows}>
        {body}
      </Box>
      <Footer
        notice={notice}
        link={link}
        hints={
          loaded && link.state !== 'denied'
            ? hintsFor(tab, mode.kind, selectedKey, filter !== '', needs.length > 0, scrollMax > 0)
            : link.state === 'denied'
              ? [{ k: 'q', label: 'quit' }]
              : [{ k: 'r', label: 'retry' }, { k: 'q', label: 'quit' }]
        }
        width={width}
      />
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

function hintsFor(
  tab: Tab,
  mode: Mode['kind'],
  key: Key | undefined,
  filtered: boolean,
  anyAttention: boolean,
  scrollable: boolean,
): HintItem[] {
  if (mode === 'confirm') return [{ k: 'y', label: 'yes' }, { k: 'n', label: 'cancel' }];
  if (mode === 'filter') {
    return [
      { k: 'Enter', label: 'keep filter' },
      { k: 'Esc', label: 'clear' },
      { k: '↑↓', label: 'select' },
    ];
  }
  if (mode === 'create') {
    return [
      { k: 'Tab', label: 'next field' },
      { k: 'Ctrl+S', label: 'create', always: true },
      { k: 'Esc', label: 'cancel', always: true },
    ];
  }
  if (mode === 'revoke') return [{ k: 'Enter', label: 'revoke', always: true }, { k: 'Esc', label: 'cancel', always: true }];
  if (mode === 'secret') return [{ k: 'c', label: 'copy' }, { k: 'Enter', label: 'done', always: true }];
  if (mode !== 'none') return [{ k: 'Esc', label: 'close' }];
  const common: HintItem[] = [
    ...(scrollable ? [{ k: 'J/K', label: 'scroll', always: true }] : []),
    { k: '1-4', label: 'panels' },
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
      ...(filtered ? [{ k: 'Esc', label: 'clear filter', always: true }] : [{ k: '/', label: 'filter', always: true }]),
      ...common,
    ];
  }
  if (tab === 'status') {
    return [
      ...(anyAttention ? [{ k: '↑↓', label: 'select', always: true }, { k: 'Enter', label: 'open key' }] : []),
      { k: 'n', label: 'new key' },
      ...common,
    ];
  }
  return [{ k: '↑↓', label: 'select' }, { k: 'n', label: 'new key' }, ...common];
}
