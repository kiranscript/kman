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
import { attention, otherIncidents, upstreamAttention } from './risk.js';
import { layout, hasFilterRow, listArea, summaryRows } from './layout.js';
import type { ListPanel } from './layout.js';
import { Footer, noticeRows, TABS } from './ui/chrome.js';
import type { HintItem, Notice, Tab } from './ui/chrome.js';
import { Hint, Panel } from './ui/parts.js';
import { editText } from './ui/TextField.js';
import { color } from './ui/theme.js';
import { AUDIT_LIMIT, useAdmin, usePlaces } from './useAdmin.js';
import { AuditMain, IncidentMain, KeyMain, StartMain, StatusMain, UpstreamMain } from './views/detail.js';
import type { Scroll } from './views/detail.js';
import { attentionItems, AuditPanel, KeysPanel, StatusPanel, UpstreamsPanel } from './views/panels.js';

export interface AppProps {
  client: AdminClient;
  /** The admin address, for the header. Never the token. */
  url: string;
  configDir?: string;
  tokenFromEnv?: boolean;
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

export function App({ client, url, configDir, tokenFromEnv, intervalMs, size, copy, noticeMs = 6000, secretGuardMs }: AppProps) {
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
  // What is being sent right now, so only the dialog that sent it shows "working".
  const [working, setWorking] = useState<'toggle' | 'revoke' | 'create' | undefined>();
  const doing = useRef('');
  // State is only read on the next render, so two quick key presses would both see
  // "not busy". This is read at once.
  const acting = useRef(false);
  const [dialogError, setDialogError] = useState<string | undefined>();
  const [notice, setNotice] = useState<Notice | undefined>();
  // How far the main panel is scrolled, and how far it can be. It starts from the top
  // again whenever something else is selected.
  const [scrollTop, setScrollTop] = useState(0);
  const [scrollMax, setScrollMax] = useState(0);
  // Enter on a list hands the keyboard to the detail panel, as lazygit does for its main
  // view: the arrow keys scroll it, and Esc gives the keyboard back to the list.
  const [detail, setDetail] = useState(false);
  const [viewRows, setViewRows] = useState(8);
  // Whether the name typed in the revoke dialog matches, so the footer offers Enter only then.
  const [revokeReady, setRevokeReady] = useState(false);

  // A success fades. A failure stays until the next key press: someone who looks away
  // must not come back believing a key was disabled when it was not.
  useEffect(() => {
    // A notice about a change under way stays while it is: it is replaced by its result.
    if (!notice || notice.tone === 'error' || working !== undefined) return;
    const t = setTimeout(() => setNotice(undefined), noticeMs);
    return () => clearTimeout(t);
  }, [notice, noticeMs, working]);

  // A successful poll resolves connection notices. Failed mutations still need acknowledgement.
  useEffect(() => {
    if (link.state === 'ok') setNotice((current) => current?.untilReconnect ? undefined : current);
  }, [link]);

  const visible = useMemo(() => visibleKeys(data.keys, filter), [data.keys, filter]);
  const selectedId = visible.some((k) => k.id === keySel) ? keySel : visible[0]?.id;
  const selectedKey = visible.find((k) => k.id === selectedId);
  const watch = useMemo(() => ({ incidents: data.incidents, findings: data.findings }), [data.incidents, data.findings]);
  const needs = useMemo(() => attention(data.keys, now, watch), [data.keys, now, watch]);
  const upNeeds = useMemo(() => upstreamAttention(data.upstreams), [data.upstreams]);
  const canaries = useMemo(() => otherIncidents(data.keys, data.incidents), [data.keys, data.incidents]);
  const items = useMemo(() => attentionItems(needs, upNeeds, data.upstreams.length, canaries), [needs, upNeeds, data.upstreams.length, canaries]);
  const attnItem = items[Math.min(attentionSel, Math.max(0, items.length - 1))];
  // Where the key in the main panel was used from: loaded for that one key, and again on every poll.
  const shownKeyId = tab === 'status' ? attnItem?.key?.id : tab === 'keys' ? selectedKey?.id : undefined;
  const places = usePlaces(client, shownKeyId, link.state === 'ok' ? link.at : 0);
  const watching = `${tab}:${tab === 'keys' ? selectedId : tab === 'status' ? attnItem?.id : tab === 'upstreams' ? upstreamSel : auditSel}`;
  useEffect(() => setScrollTop(0), [watching]);
  const scroll: Scroll = {
    offset: scrollTop,
    onMax: (m) => setScrollMax((c) => (c === m ? c : m)),
    focused: detail,
    onView: (r) => setViewRows((c) => (c === r ? c : r)),
  };
  /** Whether the focused list has more than one row, so up and down have somewhere to go. */
  const movable =
    tab === 'status' ? items.length > 1 : tab === 'keys' ? visible.length > 1 : tab === 'upstreams' ? data.upstreams.length > 1 : data.audit.length > 1;
  /** Whether the focused list has rows to move over. */
  const selectable =
    tab === 'status' ? items.length > 0 : tab === 'keys' ? visible.length > 0 : tab === 'upstreams' ? data.upstreams.length > 0 : data.audit.length > 0;
  // A long failure takes more rows, which the body gives up.
  const shownNotice = link.state === 'denied' ? undefined : notice;
  const bodyRows = rows - 1 - noticeRows(shownNotice, columns);
  const tooSmall = columns < MIN_COLUMNS || rows < MIN_ROWS;
  const noKeys = data.keys.length === 0;
  const filterRow = tab === 'keys' && hasFilterRow(filter, mode.kind === 'filter');
  /** What of the focused panel decides how many rows its list gets. */
  const listPanel: ListPanel =
    tab === 'status' ? { tab, items: items.length, noKeys } : tab === 'keys' ? { tab, filterRow } : { tab };
  // All of Status's list and its header, were there no limit.
  const statusList = listArea(Infinity, { tab: 'status', items: items.length, noKeys });
  // What each list could fill; Status is its summary, and under it the list when focused.
  const wants = {
    status: summaryRows(noKeys) + (tab === 'status' ? statusList.header + statusList.rows : 1),
    // While filtering the panel keeps the height of the whole list, so it does not shrink under the cursor.
    keys: Math.max(1, filterRow ? data.keys.length : visible.length) + (filterRow ? 1 : 0),
    upstreams: Math.max(1, data.upstreams.length),
    audit: Math.max(1, data.audit.length),
  };
  const lay = layout(columns, bodyRows, tab, wants);
  const loaded = link.state === 'ok' || data.keys.length > 0 || data.upstreams.length > 0 || data.audit.length > 0;
  const usable = loaded && link.state !== 'denied';

  // The list re-sorts as keys change state, so the selection is remembered by id and
  // follows its key. It starts as the first row; this pins that choice on the first
  // load. It only ever fills an empty selection: an effect that overwrote a chosen
  // key would race with the key press that chose it.
  useEffect(() => {
    if (selectedId) setKeySel((cur) => cur ?? selectedId);
  }, [selectedId]);

  /** Rows of the focused list on screen at once, in the layout that is drawn, so a page moves by what was shown. */
  function pageRows(): number {
    return Math.max(1, listArea(lay.heights[tab], listPanel).rows);
  }

  function move(to: number | 'first' | 'last') {
    const step = (cur: number, count: number) =>
      count === 0 ? 0 : Math.max(0, Math.min(count - 1, to === 'first' ? 0 : to === 'last' ? count - 1 : cur + to));
    if (tab === 'keys') {
      setKeySel((previous) => {
        const cur = Math.max(0, visible.findIndex((k) => k.id === previous));
        return visible[step(cur, visible.length)]?.id;
      });
    } else if (tab === 'status') {
      setAttentionSel((c) => step(c, items.length));
    } else if (tab === 'upstreams') {
      setUpstreamSel((c) => step(c, data.upstreams.length));
    } else if (tab === 'audit') {
      setAuditSel((c) => step(c, data.audit.length));
    }
  }

  function goTab(t: Tab) {
    setTab(t);
    setDetail(false);
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
    setNotice({ tone: 'error', untilReconnect: true, text: 'kman is not answering, so nothing can be changed now. r tries again.' });
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

      // Ink can deliver repeated printable keys in one chunk and arrows in one React batch.
      const up = key.upArrow || /^k+$/.test(input);
      if (up || key.downArrow || /^j+$/.test(input)) {
        const count = key.upArrow || key.downArrow ? 1 : input.length;
        const delta = up ? -count : count;
        if (detail) return setScrollTop((t) => Math.max(0, Math.min(scrollMax, t + delta)));
        return move(delta);
      }
      if (input === 'q') return exit();
      if (input === '?') {
        setNotice(undefined);
        return setMode({ kind: 'help' });
      }
      // With the detail panel focused the movement keys scroll it, a line or a page at a
      // time; panels, help, refresh and quit keep working.
      if (detail) {
        const by = (n: number) => setScrollTop((t) => Math.max(0, Math.min(scrollMax, t + n)));
        if (key.escape || key.return) return setDetail(false);
        if (key.pageUp) return by(-Math.max(1, viewRows - 1));
        if (key.pageDown) return by(Math.max(1, viewRows - 1));
        if (key.home || input === 'g') return setScrollTop(0);
        if (key.end || input === 'G') return setScrollTop(scrollMax);
      }
      if (input === 'r') {
        setNotice({ tone: 'info', text: 'Refreshing…' });
        void refresh().then((why) =>
          setNotice(why ? { tone: 'error', untilReconnect: true, text: `Could not refresh: ${why}` } : { tone: 'ok', text: 'Refreshed.' }),
        );
        return;
      }
      const n = Number(input);
      if (n >= 1 && n <= TABS.length) return goTab(TABS[n - 1]!.id);
      const at = TABS.findIndex((t) => t.id === tab);
      if (key.tab) return goTab(TABS[(at + (key.shift ? TABS.length - 1 : 1)) % TABS.length]!.id);
      if (key.rightArrow || input === 'l') return goTab(TABS[(at + 1) % TABS.length]!.id);
      if (key.leftArrow || input === 'h') return goTab(TABS[(at + TABS.length - 1) % TABS.length]!.id);

      // A page is what the list shows, less one row, so one row of the old page is still in view.
      if (key.pageUp) return move(-Math.max(1, pageRows() - 1));
      if (key.pageDown) return move(Math.max(1, pageRows() - 1));
      if (key.home || input === 'g') return move('first');
      if (key.end || input === 'G') return move('last');

      if (input === 'J') return setScrollTop((t) => Math.min(scrollMax, t + 3));
      if (input === 'K') return setScrollTop((t) => Math.max(0, t - 3));

      // The detail panel has no actions of its own: what the list does waits for Esc.
      if (detail) return;
      if (input === 'n') return offline() || busyNow() ? undefined : setMode({ kind: 'create' });
      if (tab === 'status' && key.return) {
        if (attnItem?.noUpstream) return goTab('upstreams');
        if (attnItem?.upstream) {
          const id = attnItem.upstream.id;
          setUpstreamSel(Math.max(0, data.upstreams.findIndex((u) => u.id === id)));
          return goTab('upstreams');
        }
        if (!attnItem?.key) return;
        setFilter('');
        setKeySel(attnItem.key.id);
        return goTab('keys');
      }
      // Enter only hands the keyboard over when there is something to scroll to.
      if (key.return && tab !== 'status' && selectable) {
        if (scrollMax > 0) return setDetail(true);
        return setNotice({ tone: 'info', text: 'The whole detail is already showing.' });
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

  const width = columns;

  const popup = usable && mode.kind !== 'none' && mode.kind !== 'filter';
  const h = lay.heights;
  const names = { keys: data.keys, upstreams: data.upstreams };
  const upSel = Math.min(upstreamSel, Math.max(0, data.upstreams.length - 1));
  const auSel = Math.min(auditSel, Math.max(0, data.audit.length - 1));
  const left = (
    <Box flexDirection="column" width={lay.leftW} height={lay.wide ? bodyRows : undefined} flexShrink={0}>
      <StatusPanel
        width={lay.leftW}
        height={h.status}
        focused={tab === 'status' && !detail && !popup}
        link={link}
        url={url}
        actor={data.actor}
        keys={data.keys}
        items={items}
        selected={attentionSel}
        price={data.price}
        upstreamCount={data.upstreams.length}
        watch={watch}
      />
      <KeysPanel
        width={lay.leftW}
        height={h.keys}
        focused={tab === 'keys' && !detail && !popup}
        keys={visible}
        all={data.keys}
        total={data.keys.length}
        selectedId={selectedId}
        filter={filter}
        filtering={mode.kind === 'filter'}
        hasUpstreams={data.upstreams.length > 0}
        now={now}
        watch={watch}
      />
      <UpstreamsPanel
        width={lay.leftW}
        height={h.upstreams}
        focused={tab === 'upstreams' && !detail && !popup}
        upstreams={data.upstreams}
        keys={data.keys}
        selected={upstreamSel}
      />
      <AuditPanel
        width={lay.leftW}
        height={h.audit}
        focused={tab === 'audit' && !detail && !popup}
        audit={data.audit}
        names={names}
        fresh={fresh}
        selected={auditSel}
        now={now}
      />
    </Box>
  );

  // A popup stays beside the panels when the main panel is wide enough to hold it,
  // so the list a change applies to is still in view; otherwise it takes the screen.
  const overlay = lay.wide && lay.mainW >= 78;
  const dw = overlay ? lay.mainW : width;

  let body;
  if (link.state === 'denied') {
    body = (
      <Centered width={width} height={bodyRows}>
        <Panel title="The admin API refused this token" width={66} tone={color.danger}>
          <Box flexDirection="column">
            <Text>{link.error}</Text>
            <Text>Admin address: {url}</Text>
            {configDir && <Text>Config directory: {configDir}</Text>}
            {tokenFromEnv && <Text>KMAN_ADMIN_TOKEN supplied this token. Unset it to use a saved login, or replace it, then restart.</Text>}
            <Text dimColor>Save a current token using the address above:</Text>
            <Text>kman login --admin-url &lt;address&gt;{configDir && ' --config-dir <directory>'}</Text>
            {!tokenFromEnv && <Text dimColor>Then restart the TUI.</Text>}
          </Box>
        </Panel>
      </Centered>
    );
  } else if (!loaded) {
    body = (
      <Centered width={width} height={bodyRows}>
        {link.state === 'down' ? (
          <Panel title={link.answered ? 'kman answered with an error' : 'Cannot reach kman'} width={66} tone={link.answered ? color.danger : color.warn}>
            <Box flexDirection="column">
              <Text>{link.error}</Text>
              {/* An answer means it is running: do not tell anyone to start it. */}
              <Text dimColor>{link.answered ? 'kman is running but could not serve this request.' : 'Is it running? Start it with: kman serve'}</Text>
              <Text dimColor>Retrying every few seconds.</Text>
            </Box>
          </Panel>
        ) : (
          <Text dimColor>Connecting to {url}…</Text>
        )}
      </Centered>
    );
  } else if (mode.kind === 'create') {
    body = (
      <Centered width={dw} height={bodyRows}>
        <CreateKeyDialog
          upstreams={data.upstreams}
          defaultDays={data.settings ? Math.round(data.settings.default_key_ttl_seconds / 86_400) : undefined}
          busy={working === 'create'}
          // The form is 12 rows without its error; whatever the body has left is for the error.
          errorLines={Math.max(3, bodyRows - 12)}
          error={dialogError}
          onSubmit={(i) => void create(i)}
          onCancel={() => setMode({ kind: 'none' })}
        />
      </Centered>
    );
  } else if (mode.kind === 'revoke') {
    body = (
      <Centered width={dw} height={bodyRows}>
        <RevokeDialog
          keyData={mode.key}
          maxWidth={dw}
          busy={working === 'revoke'}
          // The dialog is at most 14 rows without its error (its hint line included).
          errorLines={Math.max(3, bodyRows - 14)}
          error={dialogError}
          onConfirm={() => void revoke(mode.key)}
          onReady={setRevokeReady}
          onCancel={() => {
            setMode({ kind: 'none' });
            setNotice({ tone: 'info', text: `Revoke cancelled for ${keyLabel(mode.key)}.` });
          }}
        />
      </Centered>
    );
  } else if (mode.kind === 'secret') {
    body = (
      <Centered width={dw} height={bodyRows}>
        <SecretDialog created={mode.created} width={dw} onCopy={copy} armMs={secretGuardMs} onClose={() => setMode({ kind: 'none' })} />
      </Centered>
    );
  } else if (mode.kind === 'clients') {
    body = (
      <Centered width={dw} height={bodyRows}>
        <ClientsDialog client={client} keyData={mode.key} width={dw} height={bodyRows} now={now} onClose={() => setMode({ kind: 'none' })} />
      </Centered>
    );
  } else if (mode.kind === 'help') {
    body = (
      <Centered width={dw} height={bodyRows}>
        <HelpDialog focus={tab} detail={detail} onClose={() => setMode({ kind: 'none' })} />
      </Centered>
    );
  } else if (mode.kind === 'confirm') {
    body = (
      <Centered width={dw} height={bodyRows}>
        <Panel title={`${mode.action === 'disable' ? 'Disable' : 'Enable'} this key?`} width={Math.min(70, dw)} tone={color.warn}>
          <Box flexDirection="column">
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
        </Panel>
      </Centered>
    );
  } else {
    const attn = attnItem?.key;
    const attnUp = attnItem?.upstream;
    const main =
      tab === 'status' ? (
        attn ? (
          <KeyMain key={attn.id} keyData={attn} width={lay.mainW} height={lay.mainH} now={now} compact={!lay.wide} scroll={scroll} watch={watch} places={places} />
        ) : attnItem?.incident ? (
          <IncidentMain key={attnItem.id} incident={attnItem.incident} width={lay.mainW} height={lay.mainH} scroll={scroll} now={now} />
        ) : attnItem?.noUpstream ? (
          <UpstreamMain upstream={undefined} keys={data.keys} width={lay.mainW} height={lay.mainH} scroll={scroll} none={NO_UPSTREAM} />
        ) : attnUp ? (
          <UpstreamMain key={attnUp.id} upstream={attnUp} keys={data.keys} width={lay.mainW} height={lay.mainH} scroll={scroll} />
        ) : data.keys.length === 0 ? (
          <StartMain width={lay.mainW} height={lay.mainH} scroll={scroll} canCreate={data.upstreams.length > 0} />
        ) : (
          <StatusMain width={lay.mainW} height={lay.mainH} audit={data.audit} names={names} now={now} scroll={scroll} />
        )
      ) : tab === 'keys' && data.keys.length === 0 ? (
        <StartMain width={lay.mainW} height={lay.mainH} scroll={scroll} canCreate={data.upstreams.length > 0} />
      ) : tab === 'keys' ? (
        <KeyMain
          keyData={selectedKey}
          width={lay.mainW}
          height={lay.mainH}
          now={now}
          compact={!lay.wide}
          scroll={scroll}
          watch={watch}
          places={places}
          none={data.keys.length === 0 ? 'No keys yet.' : `No key matches "${filter}".`}
        />
      ) : tab === 'upstreams' ? (
        <UpstreamMain
          upstream={data.upstreams[upSel]}
          keys={data.keys}
          width={lay.mainW}
          height={lay.mainH}
          scroll={scroll}
          none={NO_UPSTREAM}
        />
      ) : (
        <AuditMain
          entry={data.audit[auSel]}
          none="The audit log is empty: nothing has changed yet."
          names={names}
          limited={data.audit.length >= AUDIT_LIMIT} width={lay.mainW} height={lay.mainH} scroll={scroll} />
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
        {/* Keep the dialog mounted when a resize shows or hides the panels. */}
        {overlay && popup ? left : null}
        {body}
      </Box>
      <Footer
        notice={shownNotice}
        link={link}
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
                  movable,
                  revokeReady,
                  offline: link.state === 'down',
                  working: working !== undefined,
                  canCreate: data.upstreams.length > 0,
                  hasKeys: data.keys.length > 0,
                  scrollable: scrollMax > 0,
                  detail,
                })
              : [{ k: 'r', label: 'retry' }, { k: 'q', label: 'quit' }]
        }
        width={width}
      />
    </Box>
  );
}

const NO_UPSTREAM = 'No upstreams yet. An upstream is an API kman sits in front of. Add one with:';

function Centered({ width, height, children }: { width: number; height: number; children: React.ReactNode }) {
  return (
    <Box width={width} height={height} alignItems="center" justifyContent="center">
      {children}
    </Box>
  );
}

interface HintState {
  tab: Tab;
  mode: Mode['kind'];
  key: Key | undefined;
  filtered: boolean;
  /** Whether the panel has rows to move over. */
  selectable: boolean;
  /** Whether it has more than one, so the arrows have somewhere to go. */
  movable: boolean;
  /** The name typed in the revoke dialog matches. */
  revokeReady: boolean;
  offline: boolean;
  /** A change is being sent: another cannot start. */
  working: boolean;
  /** An upstream exists, so a key can be made. */
  canCreate: boolean;
  hasKeys: boolean;
  /** The detail panel has more than it shows. */
  scrollable: boolean;
  /** The detail panel has the keyboard. */
  detail: boolean;
}

/**
 * Only keys that would do something are offered. When the line is too short the least
 * missed go first (`rank`): scrolling and the panel keys, then selecting, and last the
 * actions on the selected row, which stay unless nothing else can.
 */
function hintsFor({ tab, mode, key, filtered, selectable, movable, revokeReady, offline, working, canCreate, hasKeys, scrollable, detail }: HintState): HintItem[] {
  const canChange = !offline && !working;
  if (mode === 'confirm') return [{ k: 'y', label: 'yes' }, { k: 'n', label: 'cancel' }];
  if (mode === 'filter') {
    return [
      { k: 'Enter', label: 'keep filter', always: true },
      { k: 'Esc', label: 'clear', always: true },
      ...(movable ? [{ k: '↑↓', label: 'select' }] : []),
    ];
  }
  // With no upstream the form only explains, and Esc is all that works in it.
  if (mode === 'create' && !canCreate) return [{ k: 'Esc', label: 'close', always: true }];
  if (mode === 'create') {
    return [
      { k: 'Tab/Enter', label: 'next field' },
      { k: 'Ctrl+S', label: 'create', always: true },
      { k: 'Esc', label: 'cancel', always: true },
    ];
  }
  if (mode === 'revoke') return [...(revokeReady ? [{ k: 'Enter', label: 'revoke', always: true }] : []), { k: 'Esc', label: 'cancel', always: true }];
  if (mode === 'secret') return [{ k: 'c', label: 'copy' }, { k: 'Enter', label: 'done', always: true }];
  if (mode === 'clients') return [{ k: 'Esc', label: 'close', always: true }];
  if (mode !== 'none') return [{ k: 'Esc', label: 'close' }];
  if (detail) {
    return [
      { k: '↑↓', label: 'scroll', always: true },
      { k: 'PgUp/PgDn', label: 'page', rank: 1 },
      { k: 'g/G', label: 'top/bottom', rank: 0 },
      { k: 'Esc', label: 'back to list', always: true },
      { k: '1-4', label: 'panels', rank: 0 },
      { k: '?', label: 'help', always: true },
      { k: 'q', label: 'quit', always: true },
    ];
  }
  const common: HintItem[] = [
    { k: 'r', label: offline ? 'retry' : 'refresh', always: offline, rank: 2 },
    // The detail's border already says how to scroll, so this is the first of these to go.
    ...(scrollable ? [{ k: 'J/K', label: 'scroll', rank: 2.9 }] : []),
    { k: '1-4', label: 'panels', rank: 3.2 },
    { k: '?', label: 'help', always: true },
    { k: 'q', label: 'quit', always: true },
  ];
  const create = canChange && canCreate ? [{ k: 'n', label: 'new key', rank: tab === 'keys' && key ? 3 : 4 }] : [];
  // Only offered when the detail has more than it shows: otherwise there is nothing to go to.
  const open = selectable && scrollable ? [{ k: 'Enter', label: 'details', rank: 3.6 }] : [];
  // Moving over the rows and reaching the other panels outrank a secondary action like c IPs.
  const select = movable ? [{ k: '↑↓', label: 'select', rank: 3.3 }] : [];
  if (tab === 'keys') {
    const act =
      key?.status === 'active'
        ? [{ k: 'd', label: 'disable', rank: 5 }]
        : key?.status === 'disabled'
          ? [{ k: 'e', label: 'enable', rank: 5 }]
          : [];
    return [
      ...select,
      ...open,
      // Keep the selected key’s actions visible before the global create shortcut.
      ...(canChange ? [...create, ...act, ...(key && key.status !== 'revoked' ? [{ k: 'x', label: 'revoke', rank: 4.5 }] : [])] : []),
      ...(key && !offline ? [{ k: 'c', label: 'IPs', rank: 3 }] : []),
      ...(filtered ? [{ k: 'Esc', label: 'clear filter', always: true }] : hasKeys ? [{ k: '/', label: 'filter', rank: 6 }] : []),
      ...common,
    ];
  }
  if (tab === 'status') {
    return [...select, ...(selectable ? [{ k: 'Enter', label: 'open', rank: 5 }] : []), ...create, ...common];
  }
  return [...select, ...open, ...create, ...common];
}
