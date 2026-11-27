import { Box, Text, useApp, useInput, useWindowSize } from 'ink';
import { useEffect, useMemo, useRef, useState } from 'react';
import { ApiError } from './api.js';
import type { AdminClient, CreatedKey, CreateKeyInput, IncidentDetail, Key, PickKind, Resolution, RotatedKey } from './api.js';
import { ClientsDialog } from './dialogs/ClientsDialog.js';
import { CreateKeyDialog } from './dialogs/CreateKeyDialog.js';
import { HelpDialog } from './dialogs/HelpDialog.js';
import { ResolveDialog } from './dialogs/ResolveDialog.js';
import { SecretDialog } from './dialogs/SecretDialog.js';
import type { ShownSecret } from './dialogs/SecretDialog.js';
import { ThisWasMeDialog } from './dialogs/ThisWasMeDialog.js';
import { TypeNameDialog } from './dialogs/TypeNameDialog.js';
import { cli, resolutionWord } from './commands.js';
import { fit, plural } from './format.js';
import { actionFor, actions, endedText, incidentFor, isCanary, picks } from './incidents.js';
import type { Action } from './incidents.js';
import { isChord, keyLabel, visibleKeys } from './keys.js';
import { attention, otherIncidents, unknowns, upstreamAttention } from './risk.js';
import { layout, hasFilterRow, listArea, summaryRows } from './layout.js';
import { MAX_ROWS, nextFilter } from './requests.js';
import type { RequestFilter } from './requests.js';
import type { ListPanel } from './layout.js';
import { Footer, noticeRows, TABS } from './ui/chrome.js';
import type { HintItem, Notice, Tab } from './ui/chrome.js';
import { Hint, Panel } from './ui/parts.js';
import { editText } from './ui/TextField.js';
import { color } from './ui/theme.js';
import { Command } from './ui/command.js';
import { useArmed } from './ui/useArmed.js';
import { AUDIT_LIMIT, useAdmin, useIncident, useFlags, useIncidentDetails, useKeyRequests, usePlaces, useRequests } from './useAdmin.js';
import { AuditMain, IncidentMain, KeyMain, StartMain, StatusMain, UnknownMain, UpstreamMain } from './views/detail.js';
import type { Scroll } from './views/detail.js';
import { IncidentView } from './views/incident.js';
import { attentionItems, AuditPanel, KeysPanel, RequestsPanel, StatusPanel, UpstreamsPanel, addressWidth } from './views/panels.js';
import { RequestsMain } from './views/requests.js';

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
  /** How long action keys are ignored after an incident, or a dialog from one, appears. */
  actionGuardMs?: number;
  /** Puts text on the clipboard; defaults to asking the terminal (OSC 52). */
  copy?: (text: string) => void;
  /** Fixes the size, instead of reading the terminal's. */
  size?: { columns: number; rows: number };
}

type Mode =
  | { kind: 'none' }
  | { kind: 'filter' }
  /** `incident` is set when it was asked for from that incident. */
  | { kind: 'confirm'; action: 'disable' | 'enable'; key: Key; incident?: number }
  | { kind: 'create' }
  | { kind: 'revoke'; key: Key; incident?: number }
  | { kind: 'rotate'; key: Key; incident: number }
  | { kind: 'resolve'; incident: number }
  | { kind: 'thisWasMe'; incident: number }
  /** Arming a fired canary again. */
  | { kind: 'reset'; incident: number; canary: string; name: string }
  | { kind: 'secret'; shown: ShownSecret }
  /** The addresses a key was used from; the admin API gives them out only if the install allows it. */
  | { kind: 'clients'; key: Key }
  | { kind: 'help' };

// The classic terminal size. Every view and dialog fits it.
export const MIN_COLUMNS = 80;
export const MIN_ROWS = 24;

/** The columns the Requests strip takes of the row it shares with Audit. */
const REQUESTS_STRIP = 28;

/** Under this, the requests table takes the screen's width, with the panels above it. */
const TABLE_WIDTH = 110;
/** With an address column too, the operation and the model need this much. */
const ADDRESS_TABLE_WIDTH = 180;

/**
 * Whether a dialog is for answering this incident, so is moot once it is resolved. Enable
 * is one: whoever resolved it may have found the leak real, so the key stays as it is.
 * Arming a canary again is not, as resolving does not arm it.
 */
const endsWith = (m: Mode, id: number) =>
  (m.kind === 'rotate' || m.kind === 'resolve' || m.kind === 'thisWasMe' || m.kind === 'revoke' || m.kind === 'confirm') && m.incident === id;

const CHANGED = 'What it saw has changed: check the boxes again.';

const maybeRotated = (label: string) => `If ${label} was rotated, run ${cli.rotate(label)} for a secret you can see.`;

/**
 * What to say when the incident that ended records a rotation of the key, whose new secret
 * is not on this screen; `how` says what ended the incident. Another rotation is advised
 * only when this screen's own answer was lost and the rotation is this operator's (`me`):
 * after anyone else's, one more would end the secret they were given. kman records the
 * rotation in the same step as it resolves the incident, so none recorded is none made.
 */
function rotationSaid(
  ended: IncidentDetail,
  key: { id: string; label: string },
  how: string,
  { me, lost }: { me: string | undefined; lost: boolean },
): string | undefined {
  const done = ended.actions?.find((x) => x.action === 'key.rotate' && x.target_id === key.id);
  if (!done) return undefined;
  if (lost && (!me || done.actor === me)) {
    return `${key.label} was rotated by ${done.actor}, but no new secret arrived here: run ${cli.rotate(key.label)} for one you can see. ${how}`;
  }
  return `${how} ${key.label} was rotated by ${done.actor}, and the new secret did not come here: another rotation would end it.`;
}

export function App({ client, url, configDir, tokenFromEnv, intervalMs, size, copy, noticeMs = 6000, secretGuardMs, actionGuardMs = 400 }: AppProps) {
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
  const [working, setWorking] = useState<Work | undefined>();
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
  // Whether the name typed to revoke or rotate matches, so the footer offers Enter only then.
  const [nameReady, setNameReady] = useState(false);
  // The incident open in the main panel, and the kinds ticked in "this was me".
  const [viewing, setViewing] = useState<number | undefined>();
  const [ticked, setTicked] = useState<ReadonlySet<PickKind>>(new Set());
  // The requests table: whose, which of them, the selected one (by id, so it stays put as
  // others arrive) and the newest one seen, past which a row is new.
  const [requestKeyId, setRequestKeyId] = useState<string | undefined>();
  const [requestFilter, setRequestFilter] = useState<RequestFilter>('all');
  const [place, setPlace] = useState<{ id?: number; seen?: number }>({});
  const tableRows = useRef(8);

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
  const watch = useMemo(
    () => ({ incidents: data.incidents, incidentsError: data.incidentsError, findings: data.findings, findingsError: data.findingsError }),
    [data.incidents, data.incidentsError, data.findings, data.findingsError],
  );
  const needs = useMemo(() => attention(data.keys, now, watch), [data.keys, now, watch]);
  const upNeeds = useMemo(() => upstreamAttention(data.upstreams), [data.upstreams]);
  const canaries = useMemo(() => otherIncidents(data.keys, data.incidents), [data.keys, data.incidents]);
  const items = useMemo(
    () => attentionItems(needs, upNeeds, data.upstreams.length, canaries, unknowns(watch)),
    [needs, upNeeds, data.upstreams.length, canaries, watch],
  );
  const attnItem = items[Math.min(attentionSel, Math.max(0, items.length - 1))];
  // Where the key in the main panel was used from: loaded for that one key, and again on every poll.
  const shownKeyId = tab === 'status' ? attnItem?.key?.id : tab === 'keys' ? selectedKey?.id : undefined;
  // `now` is when the last poll answered.
  const places = usePlaces(client, shownKeyId, now);
  // Not before the first poll has answered, which is when the token is known to be taken.
  const answeredAt = link.state === 'ok' ? link.at : link.state === 'down' ? (link.lastOk ?? 0) : 0;
  const exposed = data.settings?.expose_client_addresses;
  const requests = useRequests(client, requestKeyId, requestFilter === 'refused' ? 'refused' : undefined, answeredAt, { selected: place.id, exposed });
  const hasRequests = !requests.unsupported;
  const tabs = useMemo(() => TABS.filter((t) => t.id !== 'requests' || hasRequests), [hasRequests]);
  const incidentDetails = useIncidentDetails(client, hasRequests ? data.incidents : undefined, now, exposed);
  const flag = useFlags(data.canaries, data.incidents, incidentDetails);
  const flagged = useMemo(() => requests.rows.map((e) => ({ e, flag: flag(e) })), [requests.rows, flag]);
  const tableShown = useMemo(() => (requestFilter === 'flagged' ? flagged.filter((r) => r.flag) : flagged), [flagged, requestFilter]);
  /** The selected row's place: its own, or, when it is gone, the next older one's. */
  const indexOf = (id: number | undefined) => {
    if (id === undefined) return 0;
    const at = tableShown.findIndex((r) => r.e.id <= id);
    return at < 0 ? Math.max(0, tableShown.length - 1) : at;
  };
  const requestAt = indexOf(place.id);
  const freshRows = place.seen === undefined ? 0 : tableShown.filter((r) => r.e.id > place.seen!).length;
  // The selection starts at the newest row, and from then on stays with that request.
  // The flagged rows are not all known until the incidents' detail is in: pinned before,
  // the selection would sit under rows that were there all along, counted as new.
  const settled = requestFilter !== 'flagged' || !incidentDetails.incomplete;
  useEffect(() => {
    const first = tableShown[0]?.e.id;
    if (tab === 'requests' && settled && place.id === undefined && first !== undefined) setPlace({ id: first, seen: first });
  }, [tab, settled, place.id, tableShown]);
  // A selected request that had to be let go is not passed off as another: the selection
  // starts again at the newest, and says why.
  const [untold, setUntold] = useState(false);
  useEffect(() => {
    if (!requests.lost) return;
    setPlace({});
    setUntold(true);
  }, [requests.lost]);
  // It is said once the table is what is on screen, and never over a failure still to be
  // read or a change under way.
  useEffect(() => {
    if (!untold || tab !== 'requests' || viewing !== undefined || mode.kind !== 'none' || working !== undefined || notice?.tone === 'error') return;
    setUntold(false);
    setNotice({ tone: 'info', text: 'Too many new requests to keep your place: showing the newest.' });
  }, [untold, tab, viewing, mode.kind, working, notice]);
  // A kman found to have no events, after the panel was asked for, has no panel to be on.
  useEffect(() => {
    if (tab === 'requests' && !hasRequests) setTab('keys');
  }, [tab, hasRequests]);
  const keyRequests = useKeyRequests(client, hasRequests ? shownKeyId : undefined, now);
  const lastRequests = useMemo(() => {
    if (!hasRequests || !shownKeyId) return undefined;
    const held = keyRequests.id === shownKeyId ? keyRequests : undefined;
    return { rows: held?.events?.map((e) => ({ e, flag: flag(e) })), error: held?.error };
  }, [hasRequests, shownKeyId, keyRequests, flag]);
  const incident = useIncident(client, viewing, now);
  const opened = incident.detail;
  // Before the detail arrives, the list says what it can.
  const listed = data.incidents?.find((i) => i.id === viewing);
  const incidentKey = data.keys.find((k) => k.id === (opened ?? listed)?.key_id);
  const boxes = useMemo(() => (opened ? picks(opened) : undefined), [opened]);
  const boxesAre = JSON.stringify(boxes);
  const armed = useArmed(actionGuardMs, `${viewing}:${mode.kind}:${opened ? 'loaded' : 'loading'}`);
  // The incident last seen unresolved, and those this screen is itself resolving: another
  // one turning up resolved was someone else's doing.
  const seenOpen = useRef<number | undefined>(undefined);
  const resolving = useRef(new Set<number>());
  // A rotation that brought no new secret and may have happened: if its incident turns up
  // resolved, what it records of that is said.
  const lost = useRef<{ id: number; key: { id: string; label: string } } | undefined>(undefined);

  useEffect(() => {
    if (!opened) return;
    if (opened.state !== 'resolved') {
      seenOpen.current = opened.id;
      return;
    }
    if (seenOpen.current !== opened.id || resolving.current.has(opened.id)) return;
    seenOpen.current = undefined;
    setMode((m) => (endsWith(m, opened.id) ? { kind: 'none' } : m));
    const how = `Incident #${opened.id} ${endedText(opened)}.`;
    const of = lost.current?.id === opened.id ? lost.current.key : undefined;
    setNotice({ tone: 'error', text: (of && rotationSaid(opened, of, how, { me: data.actor, lost: true })) || how });
  }, [opened]);

  // A box ticks every value of its kind, so what was ticked is ticked again if they change.
  useEffect(() => {
    if (mode.kind !== 'thisWasMe' || ticked.size === 0) return;
    setTicked(new Set());
    setDialogError(CHANGED);
  }, [boxesAre]);

  const watching =
    viewing !== undefined
      ? `incident:${viewing}`
      : `${tab}:${tab === 'keys' ? selectedId : tab === 'status' ? attnItem?.id : tab === 'upstreams' ? upstreamSel : auditSel}`;
  useEffect(() => setScrollTop(0), [watching]);
  const scroll: Scroll = {
    offset: scrollTop,
    onMax: (m) => setScrollMax((c) => (c === m ? c : m)),
    focused: detail,
    onView: (r) => setViewRows((c) => (c === r ? c : r)),
  };
  /** Whether the focused list has more than one row, so up and down have somewhere to go. */
  const rowCount = { status: items.length, keys: visible.length, upstreams: data.upstreams.length, audit: data.audit.length, requests: tableShown.length }[tab];
  const movable = rowCount > 1 || (tab === 'requests' && requests.more && requests.rows.length < MAX_ROWS);
  /** Whether the focused list has rows to move over. */
  const selectable = rowCount > 0;
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
    // Whose and how many are flagged: the rows themselves are the main panel's.
    requests: 2,
  };
  const panels = tabs.map((t) => t.id);
  const address = addressWidth(url);
  const beside = layout(columns, bodyRows, tab, wants, { panels, address });
  const lay =
    tab === 'requests' && !(beside.wide && beside.mainW >= (exposed ? ADDRESS_TABLE_WIDTH : TABLE_WIDTH))
      ? layout(columns, bodyRows, tab, wants, { panels, address, stacked: true })
      : beside;
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
    return tab === 'requests' ? tableRows.current : Math.max(1, listArea(lay.heights[tab], listPanel).rows);
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
    } else if (tab === 'requests') {
      // Moving past the oldest row asks for the ones before it.
      const end = tableShown.length - 1;
      if (to === 'last' ? requestAt >= end : typeof to === 'number' && requestAt + to > end) requests.loadOlder();
      setPlace((p) => {
        const id = tableShown[step(indexOf(p.id), tableShown.length)]?.e.id;
        if (id === undefined) return p;
        // g goes to the newest, so everything has been seen; going up sees what is passed.
        // Before the selection was pinned, what was on screen from the top had been seen.
        return { id, seen: Math.max(id, p.seen ?? tableShown[0]?.e.id ?? id) };
      });
    }
  }

  /** Shows the requests of one key, or of all of them, from the newest. */
  function showRequests(keyId: string | undefined, filter: RequestFilter = requestFilter) {
    setRequestKeyId(keyId);
    setRequestFilter(filter);
    setPlace({});
    goTab('requests');
  }

  /** Enter on a request: its incident, else its key. */
  function openRequest() {
    const row = tableShown[requestAt];
    if (!row) return;
    if (row.flag?.incident !== undefined) return open(row.flag.incident);
    const k = data.keys.find((x) => x.id === row.e.key_id);
    if (k) {
      setFilter('');
      setKeySel(k.id);
      return goTab('keys');
    }
    setNotice({
      tone: 'info',
      text: row.flag?.canary
        ? `A canary with no open incident: kman canary show ${row.e.key_id}`
        : row.e.key_id
          ? 'Its key is not in the list.'
          : 'It presented no key kman knows: there is nothing to open.',
    });
  }

  function goTab(t: Tab) {
    // Back at the table from the newest row, it starts at the newest again.
    if (t === 'requests' && tab !== t && place.id === place.seen) setPlace({});
    setTab(t);
    setDetail(false);
    setViewing(undefined);
  }

  /** Opens an incident in the main panel. It only reads: nothing is acknowledged by looking. */
  function open(id: number) {
    setDetail(false);
    setScrollTop(0);
    setViewing(id);
  }

  /** Runs one admin API call; resolves to the error message, or undefined on success. */
  async function call(kind: Work, what: string, fn: () => Promise<unknown>): Promise<string | undefined> {
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

  /**
   * One change made for an incident. When it fails because the incident has meanwhile been
   * resolved, that is said, with who did it, and the dialog that asked is closed; any other
   * failure is returned for the caller to show.
   */
  async function onIncident(id: number, doing: string, resolves: boolean, change: () => Promise<unknown>): Promise<{ error?: string; ended?: IncidentDetail }> {
    if (resolves) resolving.current.add(id);
    let after: IncidentDetail | undefined;
    const error = await call('incident', doing, async () => {
      try {
        await change();
      } catch (err) {
        after = await client.incident(id).catch(() => undefined);
        throw err;
      }
    });
    void refresh();
    if (!error) return {};
    resolving.current.delete(id);
    if (after?.state !== 'resolved') return { error };
    seenOpen.current = undefined;
    setMode((m) => (endsWith(m, id) ? { kind: 'none' } : m));
    setNotice({ tone: 'error', text: `Incident #${id} ${endedText(after)}. While ${doing}: ${error}` });
    return { error, ended: after };
  }

  async function acknowledge(id: number) {
    setNotice({ tone: 'info', text: `Acknowledging incident #${id}…` });
    const { error, ended } = await onIncident(id, `acknowledging incident #${id}`, false, () => client.acknowledgeIncident(id));
    if (ended) return;
    if (error) return setNotice({ tone: 'error', text: `Could not acknowledge incident #${id}: ${error}` });
    setNotice({ tone: 'ok', text: `Acknowledged incident #${id}: it stays open. (kman incident ack ${id})` });
  }

  /** Whether a dialog's change cannot be sent now, saying why in the dialog. */
  function held(): boolean {
    if (link.state === 'down') {
      setDialogError('kman is not answering, so nothing was sent. Try again when it does.');
      return true;
    }
    return busyNow();
  }

  async function resolve(id: number, as: Resolution) {
    if (held()) return;
    setDialogError(undefined);
    let by: string | undefined;
    const { error, ended } = await onIncident(id, `resolving incident #${id}`, true, async () => {
      by = (await client.resolveIncident(id, as)).resolved_by;
    });
    if (ended) return;
    if (error) return setDialogError(error);
    setMode({ kind: 'none' });
    // Resolving it the same way again changes nothing, so it may be someone else's.
    setNotice(
      by && data.actor && by !== data.actor
        ? { tone: 'error', text: `Incident #${id} was already resolved by ${by} as ${resolutionWord(as)}.` }
        : { tone: 'ok', text: `Resolved incident #${id} as ${resolutionWord(as)}.` },
    );
  }

  async function thisWasMe(id: number, kinds: PickKind[], everyKey: boolean) {
    if (held()) return;
    setDialogError(undefined);
    let said = '';
    let by: string | undefined;
    const { error, ended } = await onIncident(id, `resolving incident #${id}`, true, async () => {
      // Looked at again first: it may have been resolved, or have seen more, since it was drawn.
      const latest = await client.incident(id);
      if (latest.state === 'resolved') throw new Error(`incident ${id} is already resolved`);
      if (kinds.length && JSON.stringify(picks(latest)) !== boxesAre) {
        // Drawn at once: the next key must find the new boxes, not the old ones beside a warning.
        incident.show(latest);
        throw new Error(CHANGED);
      }
      const got = await client.thisWasMe(
        id,
        kinds.length ? { add_known_sources: true, kinds, ...(everyKey ? { install_wide: true } : {}) } : { add_known_sources: false },
      );
      by = got.incident.resolved_by;
      said = [
        kinds.length ? ` Added ${plural(got.added.length, 'known source')}${everyKey ? ' for every key' : ''}.` : '',
        got.already_known ? ` ${plural(got.already_known, 'place was', 'places were')} already known.` : '',
        got.withheld_networks ? ` ${plural(got.withheld_networks, 'network was', 'networks were')} not added: addresses are off.` : '',
      ].join('');
    });
    if (ended) return;
    if (error) {
      if (error === CHANGED) setTicked(new Set());
      return setDialogError(error);
    }
    setMode({ kind: 'none' });
    // Marked a false positive since it was looked at: kman takes it again, and still adds
    // what was ticked.
    setNotice(
      by && data.actor && by !== data.actor
        ? { tone: 'error', text: `Incident #${id} was already resolved by ${by} as false positive.${said}` }
        : { tone: 'ok', text: `#${id} resolved: false positive.${said}` },
    );
  }

  async function resetCanary(id: number, canary: string, name: string) {
    setMode({ kind: 'none' });
    if (offline() || busyNow()) return;
    setNotice({ tone: 'info', text: `Arming ${name} again…` });
    const error = await call('incident', `arming ${name} again`, () => client.resetCanary(canary));
    setNotice(
      error
        ? { tone: 'error', text: `Could not reset ${name}: ${error}` }
        : { tone: 'ok', text: `${name} is armed: its next use alerts. Incident #${id} is as it was.` },
    );
    void refresh();
  }

  async function confirmToggle(action: 'disable' | 'enable', key: Key, id?: number) {
    if (acting.current) return;
    setMode({ kind: 'none' });
    if (id !== undefined && offline()) return;
    setNotice({ tone: 'info', text: `${action === 'disable' ? 'Disabling' : 'Enabling'} ${keyLabel(key)}…` });
    const doing = `${action === 'disable' ? 'disabling' : 'enabling'} ${keyLabel(key)}`;
    const change = async () => {
      if (action === 'disable') return client.disableKey(key.id, id);
      // kman records no incident on an enable, so it would not refuse one for an incident
      // resolved since it was asked: look first.
      if (id !== undefined && (await client.incident(id)).state === 'resolved') throw new Error(`incident ${id} is already resolved`);
      return client.enableKey(key.id);
    };
    const { error: err, ended } =
      id === undefined ? { error: await call('toggle', doing, change), ended: undefined } : await onIncident(id, doing, false, change);
    if (ended) return;
    if (err) setNotice({ tone: 'error', text: `Could not ${action} ${keyLabel(key)}: ${err}` });
    else
      setNotice({
        tone: 'ok',
        text:
          action === 'disable'
            ? // One row: from an incident, what becomes of it matters more.
              `Disabled ${keyLabel(key)}. ${id === undefined ? 'It is refused from its next request.' : `Incident #${id} stays open.`}`
            : `Enabled ${keyLabel(key)}.`,
      });
    // A change made for an incident has already asked for fresh data.
    if (id === undefined) void refresh();
  }

  async function revoke(key: Key, id?: number) {
    if (id === undefined ? acting.current : held()) return;
    setDialogError(undefined);
    const { error, ended } =
      id === undefined
        ? { error: await call('revoke', `revoking ${keyLabel(key)}`, () => client.revokeKey(key.id)), ended: undefined }
        : await onIncident(id, `revoking ${keyLabel(key)}`, true, () => client.revokeKey(key.id, id));
    if (ended) return;
    if (error) return setDialogError(error);
    setMode({ kind: 'none' });
    setNotice({ tone: 'ok', text: `Revoked ${keyLabel(key)}. ${id === undefined ? 'It cannot be used again.' : `Incident #${id} resolved as confirmed.`}` });
    if (id === undefined) void refresh();
  }

  async function rotate(key: Key, id: number) {
    if (held()) return;
    setDialogError(undefined);
    let rotated: RotatedKey | undefined;
    let unanswered = false;
    const { error, ended } = await onIncident(id, `rotating ${keyLabel(key)}`, true, async () => {
      try {
        rotated = await client.rotateKey(key.id, id);
      } catch (err) {
        // Only a refusal says that nothing changed. No answer, a success that could not be
        // read, and a failure such as a proxy's 502 may all follow a rotation.
        unanswered = !(err instanceof ApiError && err.status !== undefined && err.status >= 400 && err.status < 500);
        throw err;
      }
    });
    const of = { id: key.id, label: keyLabel(key) };
    // An answer that was lost may have been of a rotation that happened: its secret is gone with it.
    if (unanswered) lost.current = { id, key: of };
    // This try, or an earlier one for this incident that brought no secret.
    const warning = lost.current?.id === id ? maybeRotated(of.label) : undefined;
    if (ended) {
      const how = `Incident #${id} ${endedText(ended)}.`;
      const done = rotationSaid(ended, of, how, { me: data.actor, lost: warning !== undefined });
      const text = done ? `${done} (${error})` : `${how} Could not rotate: ${error}.`;
      return setNotice({ tone: 'error', text });
    }
    if (error || !rotated) return setDialogError(`${error ?? 'kman did not return the new secret'}${warning ? `. ${warning}` : ''}`);
    setNotice(undefined);
    setMode({ kind: 'secret', shown: { was: 'rotated', note: `Incident #${id} is resolved as confirmed.`, ...rotated } });
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
    setMode({ kind: 'secret', shown: { was: 'created', ...created } });
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

  /** Why an incident does not offer an action. */
  function whyNot(action: Action, d: IncidentDetail): string {
    if (d.state === 'resolved') return `Incident #${d.id} is resolved: ${isCanary(d) ? 'only reset is left' : 'there is nothing left to answer'}.`;
    if (action === 'acknowledge') return `Incident #${d.id} is already acknowledged.`;
    if (isCanary(d)) return 'A canary offers only acknowledge, resolve and reset.';
    if (action === 'reset') return 'Only a canary is reset.';
    if (!incidentKey) return 'Its key is not in the list.';
    if (incidentKey.status === 'revoked') return `${keyLabel(incidentKey)} is revoked for good.`;
    return `${keyLabel(incidentKey)} is ${action === 'enable' ? 'not disabled' : 'already disabled: e enables it'}.`;
  }

  /** An action key pressed on the incident. */
  function ask(action: Action) {
    if (!opened || !armed() || offline() || busyNow()) return;
    if (!actions(opened, incidentKey).includes(action)) return setNotice({ tone: 'info', text: whyNot(action, opened) });
    const id = opened.id;
    setDialogError(undefined);
    setNotice(undefined);
    if (action === 'acknowledge') return void acknowledge(id);
    if (action === 'resolve') return setMode({ kind: 'resolve', incident: id });
    if (action === 'thisWasMe') {
      setTicked(new Set());
      return setMode({ kind: 'thisWasMe', incident: id });
    }
    if (action === 'reset') return setMode({ kind: 'reset', incident: id, canary: opened.key_id, name: opened.key_name || opened.key_id });
    const k = incidentKey!;
    if (action === 'disable' || action === 'enable') return setMode({ kind: 'confirm', action, key: k, incident: id });
    setMode(action === 'rotate' ? { kind: 'rotate', key: k, incident: id } : { kind: 'revoke', key: k, incident: id });
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
      if (
        mode.kind === 'create' ||
        mode.kind === 'revoke' ||
        mode.kind === 'rotate' ||
        mode.kind === 'resolve' ||
        mode.kind === 'thisWasMe' ||
        mode.kind === 'secret' ||
        mode.kind === 'help' ||
        mode.kind === 'clients'
      )
        return;
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
      if (mode.kind === 'confirm' || mode.kind === 'reset') {
        if (isChord(key)) return;
        // From an incident it is y/N: Enter says no, and neither answers a dialog just shown.
        const guarded = mode.kind === 'reset' || mode.incident !== undefined;
        if (input === 'n' || input === 'N' || key.escape || (guarded && key.return && armed())) setMode({ kind: 'none' });
        else if ((input === 'y' || input === 'Y') && (!guarded || armed())) {
          if (mode.kind === 'reset') void resetCanary(mode.incident, mode.canary, mode.name);
          else void confirmToggle(mode.action, mode.key, mode.incident);
        }
        return;
      }

      // An incident, like a focused detail, has the keyboard: the movement keys scroll it.
      const reading = detail || viewing !== undefined;

      // Ink can deliver repeated printable keys in one chunk and arrows in one React batch.
      const up = key.upArrow || /^k+$/.test(input);
      if (up || key.downArrow || /^j+$/.test(input)) {
        const count = key.upArrow || key.downArrow ? 1 : input.length;
        const delta = up ? -count : count;
        if (reading) return setScrollTop((t) => Math.max(0, Math.min(scrollMax, t + delta)));
        return move(delta);
      }
      if (input === 'q') return exit();
      if (input === '?') {
        setNotice(undefined);
        return setMode({ kind: 'help' });
      }
      // With the detail panel focused the movement keys scroll it, a line or a page at a
      // time; panels, help, refresh and quit keep working.
      if (reading) {
        const by = (n: number) => setScrollTop((t) => Math.max(0, Math.min(scrollMax, t + n)));
        if (viewing !== undefined) {
          if (key.escape) return setViewing(undefined);
        } else if (key.escape || key.return) return setDetail(false);
        if (key.pageUp) return by(-Math.max(1, viewRows - 1));
        if (key.pageDown) return by(Math.max(1, viewRows - 1));
        if (key.home || input === 'g') return setScrollTop(0);
        if (key.end || input === 'G') return setScrollTop(scrollMax);
      }
      if (input === 'r') {
        setNotice({ tone: 'info', text: 'Refreshing…' });
        void refresh().then((why) =>
          setNotice(
            !why
              ? { tone: 'ok', text: 'Refreshed.' }
              : why.partial
                ? // The connection is fine, so the next poll does not answer this.
                  { tone: 'error', text: `Refreshed, but ${why.message}` }
                : { tone: 'error', untilReconnect: true, text: `Could not refresh: ${why.message}` },
          ),
        );
        return;
      }
      const n = Number(input);
      if (n >= 1 && n <= tabs.length) return goTab(tabs[n - 1]!.id);
      const at = tabs.findIndex((t) => t.id === tab);
      if (key.tab) return goTab(tabs[(at + (key.shift ? tabs.length - 1 : 1)) % tabs.length]!.id);
      if (key.rightArrow || input === 'l') return goTab(tabs[(at + 1) % tabs.length]!.id);
      if (key.leftArrow || input === 'h') return goTab(tabs[(at + tabs.length - 1) % tabs.length]!.id);

      // A page is what the list shows, less one row, so one row of the old page is still in view.
      if (key.pageUp) return move(-Math.max(1, pageRows() - 1));
      if (key.pageDown) return move(Math.max(1, pageRows() - 1));
      if (key.home || input === 'g') return move('first');
      if (key.end || input === 'G') return move('last');

      if (input === 'J') return setScrollTop((t) => Math.min(scrollMax, t + 3));
      if (input === 'K') return setScrollTop((t) => Math.max(0, t - 3));

      if (viewing !== undefined) {
        const action = isChord(key) ? undefined : actionFor(input);
        if (action) ask(action);
        return;
      }
      // The detail panel has no actions of its own: what the list does waits for Esc.
      if (detail) return;
      if (input === 'n') return offline() || busyNow() ? undefined : setMode({ kind: 'create' });
      if (tab === 'requests') {
        if (key.return) return openRequest();
        if (input === 'f') {
          setPlace({});
          return setRequestFilter(nextFilter);
        }
        if (key.escape && requestKeyId !== undefined) {
          setPlace({});
          return setRequestKeyId(undefined);
        }
        return;
      }
      if (input === 'v' && hasRequests && (tab === 'keys' || tab === 'status')) {
        const id = tab === 'keys' ? selectedId : (attnItem?.key?.id ?? attnItem?.incident?.key_id);
        // All of the key's requests, whatever the table was last narrowed to.
        if (id !== undefined) return showRequests(id, 'all');
      }
      // Enter on a row marked as an incident opens it.
      const marked = tab === 'status' ? (attnItem?.incident ?? incidentFor(data.incidents, attnItem?.key?.id)) : tab === 'keys' ? incidentFor(data.incidents, selectedId) : undefined;
      if (key.return && marked) return open(marked.id);
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
  const reading = detail || viewing !== undefined;
  const requestKey = data.keys.find((k) => k.id === requestKeyId);
  // A canary, or a key that has gone, is named by its requests or its id.
  const requestKeyName =
    requestKeyId === undefined
      ? undefined
      : requestKey
        ? keyLabel(requestKey)
        : requests.rows[0]?.key_name || data.incidents?.find((i) => i.key_id === requestKeyId)?.key_name || requestKeyId;
  const flagsMissing = data.canariesError
    ? 'Canaries did not load: their requests may not be flagged.'
    : data.incidentsError
      ? 'Incidents did not load: flags may be old or missing.'
      : incidentDetails.incomplete
        ? 'An incident is still loading or did not load: flags may be missing.'
        : undefined;
  // On a short screen one row holds both of the last two strips.
  const requestsW = lay.shared ? REQUESTS_STRIP : lay.leftW;
  const lastPanels = (
    <Box flexDirection={lay.shared ? 'row' : 'column'} flexShrink={0}>
      <AuditPanel
        width={lay.leftW - (lay.shared ? REQUESTS_STRIP : 0)}
        height={h.audit}
        focused={tab === 'audit' && !reading && !popup}
        audit={data.audit}
        names={names}
        fresh={fresh}
        selected={auditSel}
        now={now}
      />
      {hasRequests ? (
        <RequestsPanel
          width={requestsW}
          height={h.requests}
          focused={tab === 'requests' && !reading && !popup}
          keyName={lay.shared ? undefined : requestKeyName}
          brief={lay.shared}
          danger={flagged.filter((r) => r.flag?.severity === 'danger').length}
          warn={flagged.filter((r) => r.flag?.severity === 'warn').length}
          loaded={requests.loaded ? flagged.length : undefined}
        />
      ) : null}
    </Box>
  );
  const left = (
    <Box flexDirection="column" width={lay.leftW} height={lay.wide ? bodyRows : undefined} flexShrink={0}>
      <StatusPanel
        width={lay.leftW}
        height={h.status}
        focused={tab === 'status' && !reading && !popup}
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
        focused={tab === 'keys' && !reading && !popup}
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
        focused={tab === 'upstreams' && !reading && !popup}
        upstreams={data.upstreams}
        keys={data.keys}
        selected={upstreamSel}
      />
      {lastPanels}
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
        <TypeNameDialog
          action="revoke"
          keyData={mode.key}
          command={cli.revoke(keyLabel(mode.key), mode.incident)}
          incidentNote={mode.incident === undefined ? undefined : `Incident #${mode.incident} is resolved as confirmed.`}
          maxWidth={dw}
          busy={working !== undefined}
          // The dialog is at most 14 rows without its error (its hint line included), and
          // two more when it is done for an incident.
          errorLines={Math.max(3, bodyRows - (mode.incident === undefined ? 14 : 16))}
          error={dialogError}
          guardMs={mode.incident === undefined ? 0 : actionGuardMs}
          onConfirm={() => void revoke(mode.key, mode.incident)}
          onReady={setNameReady}
          onCancel={() => {
            setMode({ kind: 'none' });
            setNotice({ tone: 'info', text: `Revoke cancelled for ${keyLabel(mode.key)}.` });
          }}
        />
      </Centered>
    );
  } else if (mode.kind === 'rotate') {
    body = (
      <Centered width={dw} height={bodyRows}>
        <TypeNameDialog
          action="rotate"
          keyData={mode.key}
          command={cli.rotate(keyLabel(mode.key), mode.incident)}
          incidentNote={`Incident #${mode.incident} is resolved as confirmed.`}
          maxWidth={dw}
          busy={working !== undefined}
          // Its words take a row more than revoke's.
          errorLines={Math.max(3, bodyRows - 17)}
          error={dialogError}
          guardMs={actionGuardMs}
          onConfirm={() => void rotate(mode.key, mode.incident)}
          onReady={setNameReady}
          onCancel={() => {
            setMode({ kind: 'none' });
            setNotice({ tone: 'info', text: `Rotation cancelled for ${keyLabel(mode.key)}.` });
          }}
        />
      </Centered>
    );
  } else if (mode.kind === 'resolve') {
    body = (
      <Centered width={dw} height={bodyRows}>
        <ResolveDialog
          id={mode.incident}
          width={dw}
          busy={working !== undefined}
          error={dialogError}
          guardMs={actionGuardMs}
          onConfirm={(as) => void resolve(mode.incident, as)}
          onCancel={() => setMode({ kind: 'none' })}
        />
      </Centered>
    );
  } else if (mode.kind === 'thisWasMe') {
    body = (
      <Centered width={dw} height={bodyRows}>
        <ThisWasMeDialog
          id={mode.incident}
          picks={boxes}
          ticked={ticked}
          width={dw}
          busy={working !== undefined}
          error={dialogError}
          guardMs={actionGuardMs}
          onTick={(kind) => {
            setDialogError(undefined);
            setTicked((was) => {
              const next = new Set(was);
              if (!next.delete(kind)) next.add(kind);
              return next;
            });
          }}
          onConfirm={(kinds, everyKey) => void thisWasMe(mode.incident, kinds, everyKey)}
          onCancel={() => setMode({ kind: 'none' })}
        />
      </Centered>
    );
  } else if (mode.kind === 'reset') {
    body = (
      <Centered width={dw} height={bodyRows}>
        <Panel title="Arm this canary again?" width={Math.min(70, dw)} tone={color.warn}>
          <Text bold wrap="wrap">
            {mode.name}
          </Text>
          <Text dimColor wrap="wrap">
            id {mode.canary}
          </Text>
          <Box marginTop={1}>
            <Text wrap="wrap">Its next use alerts again. What it has recorded stays, and incident #{mode.incident} is left as it is.</Text>
          </Box>
          <Command>{cli.resetCanary(mode.canary)}</Command>
          <YesNo />
        </Panel>
      </Centered>
    );
  } else if (mode.kind === 'secret') {
    body = (
      <Centered width={dw} height={bodyRows}>
        <SecretDialog shown={mode.shown} width={dw} onCopy={copy} armMs={secretGuardMs} onClose={() => setMode({ kind: 'none' })} />
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
        <HelpDialog focus={tab} detail={detail} incident={viewing !== undefined} requests={hasRequests} onClose={() => setMode({ kind: 'none' })} />
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
                ? `It is refused from its next request, and you can enable it again.${mode.incident === undefined ? '' : ` Incident #${mode.incident} stays open.`}`
                : 'It works again from its next request.'}
            </Text>
          </Box>
          <Command>{mode.action === 'disable' ? cli.disable(keyLabel(mode.key), mode.incident) : cli.enable(keyLabel(mode.key))}</Command>
          {mode.incident === undefined ? null : <YesNo />}
        </Panel>
      </Centered>
    );
  } else if (viewing !== undefined) {
    // Narrow, it takes the body: under the panels there would be no room to read it.
    const view = (
      <IncidentView
        key={viewing}
        id={viewing}
        state={incident}
        listed={listed}
        keyData={incidentKey}
        width={lay.wide ? lay.mainW : width}
        height={lay.wide ? lay.mainH : bodyRows}
        now={now}
        scroll={scroll}
        offline={link.state === 'down'}
      />
    );
    body = (
      <Box width={width} height={bodyRows}>
        {lay.wide ? left : null}
        {view}
      </Box>
    );
  } else {
    const attn = attnItem?.key;
    const attnUp = attnItem?.upstream;
    const main =
      tab === 'requests' ? (
        <RequestsMain
          width={lay.mainW}
          height={lay.mainH}
          rows={tableShown}
          selected={requestAt}
          filter={requestFilter}
          keyName={requestKeyName}
          state={requests}
          stale={link.state !== 'ok' || requests.error !== undefined}
          incomplete={flagsMissing}
          fresh={freshRows}
          now={now}
          onView={(r) => (tableRows.current = r)}
        />
      ) : tab === 'status' ? (
        attn ? (
          <KeyMain key={attn.id} keyData={attn} width={lay.mainW} height={lay.mainH} now={now} compact={!lay.wide} scroll={scroll} watch={watch} places={places} requests={lastRequests} />
        ) : attnItem?.incident ? (
          <IncidentMain key={attnItem.id} incident={attnItem.incident} width={lay.mainW} height={lay.mainH} scroll={scroll} now={now} />
        ) : attnItem?.unknown ? (
          <UnknownMain key={attnItem.id} unknown={attnItem.unknown} width={lay.mainW} height={lay.mainH} scroll={scroll} />
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
          requests={lastRequests}
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
                  key: tab === 'status' ? attnItem?.key : selectedKey,
                  canary: Boolean(attnItem?.incident),
                  filtered: filter !== '',
                  selectable,
                  // A part that did not load has nothing to open.
                  openable: Boolean(attnItem?.key || attnItem?.upstream || attnItem?.noUpstream || attnItem?.incident),
                  marked: Boolean(tab === 'status' ? (attnItem?.incident ?? incidentFor(data.incidents, attnItem?.key?.id)) : incidentFor(data.incidents, selectedId)),
                  movable,
                  nameReady,
                  incident: viewing !== undefined,
                  answered: link.state === 'down' && Boolean(link.answered),
                  offline: link.state === 'down',
                  working: working !== undefined,
                  canCreate: data.upstreams.length > 0,
                  hasKeys: data.keys.length > 0,
                  scrollable: scrollMax > 0,
                  detail,
                  panels: tabs.length,
                  requests: hasRequests,
                  oneKey: requestKeyId !== undefined,
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

/** What is being sent. */
type Work = 'toggle' | 'revoke' | 'create' | 'incident';

/** The answer a y/N dialog waits for: no is what Enter says. */
function YesNo() {
  return (
    <Box marginTop={1}>
      <Text>
        <Text bold color={color.key}>y</Text>
        <Text dimColor> yes   </Text>
        <Text bold color={color.key}>N</Text>
        <Text dimColor> no (also Enter, Esc)</Text>
      </Text>
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
  /** The name typed to revoke or rotate matches. */
  nameReady: boolean;
  /** An incident is open in the main panel. */
  incident: boolean;
  /** The selected row is marked as an incident, which Enter opens. */
  marked: boolean;
  /** kman is answering, with an error. */
  answered: boolean;
  /** How many panels there are, and whether the requests one is among them. */
  panels: number;
  requests: boolean;
  /** The requests table shows one key. */
  oneKey: boolean;
  /** The selected Status row is a canary's incident, whose key is not listed. */
  canary: boolean;
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
  /** Enter on the selected Status row goes somewhere. */
  openable: boolean;
}

/**
 * Only keys that would do something are offered. When the line is too short the least
 * missed go first (`rank`): scrolling and the panel keys, then selecting, and last the
 * actions on the selected row, which stay unless nothing else can.
 */
function hintsFor({ tab, mode, key, filtered, selectable, movable, nameReady, offline, working, canCreate, hasKeys, scrollable, detail, openable, incident, marked, answered, panels, requests, oneKey, canary }: HintState): HintItem[] {
  const jump = `1-${panels}`;
  const canChange = !offline && !working;
  if (mode === 'confirm' || mode === 'reset') return [{ k: 'y', label: 'yes' }, { k: 'n', label: 'cancel' }];
  if (mode === 'resolve' || mode === 'thisWasMe') return [{ k: 'Esc', label: 'cancel', always: true }];
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
  if (mode === 'revoke' || mode === 'rotate') return [...(nameReady ? [{ k: 'Enter', label: mode, always: true }] : []), { k: 'Esc', label: 'cancel', always: true }];
  if (mode === 'secret') return [{ k: 'c', label: 'copy' }, { k: 'Enter', label: 'done', always: true }];
  if (mode === 'clients') return [{ k: 'Esc', label: 'close', always: true }];
  if (mode !== 'none') return [{ k: 'Esc', label: 'close' }];
  if (incident) {
    return [
      // Said first: it is why the keys in the view do nothing.
      ...(offline ? [{ k: 'Actions off:', label: answered ? 'kman answers with an error' : 'kman is unreachable', always: true }] : []),
      ...(scrollable ? [{ k: '↑↓', label: 'scroll', rank: 3.3 }] : []),
      { k: 'Esc', label: 'back', always: true },
      { k: 'r', label: offline ? 'retry' : 'refresh', always: offline, rank: 2 },
      { k: jump, label: 'panels', rank: 1 },
      { k: '?', label: 'help', always: true },
      { k: 'q', label: 'quit', always: true },
    ];
  }
  if (detail) {
    return [
      { k: '↑↓', label: 'scroll', always: true },
      { k: 'PgUp/PgDn', label: 'page', rank: 1 },
      { k: 'g/G', label: 'top/bottom', rank: 0 },
      { k: 'Esc', label: 'back to list', always: true },
      { k: jump, label: 'panels', rank: 0 },
      { k: '?', label: 'help', always: true },
      { k: 'q', label: 'quit', always: true },
    ];
  }
  const common: HintItem[] = [
    { k: 'r', label: offline ? 'retry' : 'refresh', always: offline, rank: 2 },
    // The detail's border already says how to scroll, so this is the first of these to go.
    ...(scrollable ? [{ k: 'J/K', label: 'scroll', rank: 2.9 }] : []),
    { k: jump, label: 'panels', rank: 3.2 },
    { k: '?', label: 'help', always: true },
    { k: 'q', label: 'quit', always: true },
  ];
  const create = canChange && canCreate ? [{ k: 'n', label: 'new key', rank: tab === 'keys' && key ? 3 : 4 }] : [];
  // Only offered when the detail has more than it shows: otherwise there is nothing to go to.
  const open = marked && tab === 'keys' ? [{ k: 'Enter', label: 'incident', rank: 5.5 }] : selectable && scrollable ? [{ k: 'Enter', label: 'details', rank: 3.6 }] : [];
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
      ...(key && requests ? [{ k: 'v', label: 'requests', rank: 2.95 }] : []),
      ...(key && !offline ? [{ k: 'c', label: 'IPs', rank: 3 }] : []),
      ...(filtered ? [{ k: 'Esc', label: 'clear filter', always: true }] : hasKeys ? [{ k: '/', label: 'filter', rank: 6 }] : []),
      ...common,
    ];
  }
  if (tab === 'status') {
    return [
      ...select,
      ...(openable ? [{ k: 'Enter', label: marked ? 'incident' : 'open', rank: 5 }] : []),
      ...(requests && (key || canary) ? [{ k: 'v', label: 'requests', rank: 2.95 }] : []),
      ...create,
      ...common,
    ];
  }
  if (tab === 'requests') {
    return [
      ...select,
      ...(selectable ? [{ k: 'Enter', label: 'open', rank: 5 }] : []),
      { k: 'f', label: 'filter', rank: 4.5 },
      { k: 'g', label: 'newest', rank: 4 },
      ...(oneKey ? [{ k: 'Esc', label: 'all keys', always: true }] : []),
      // J and K have nothing to scroll here.
      ...common.filter((h) => h.k !== 'J/K'),
    ];
  }
  return [...select, ...open, ...create, ...common];
}
