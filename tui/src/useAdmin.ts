// Polls the admin API and keeps the last good answer, so a dropped connection
// shows stale data marked as stale rather than an empty screen.
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { ApiError, isMissingRoute } from './api.js';
import type {
  AdminClient,
  AuditEntry,
  EventPage,
  EventQuery,
  ExposureFinding,
  Incident,
  IncidentDetail,
  Key,
  KeyPlaces,
  PriceDataStatus,
  RequestEvent,
  Settings,
  Upstream,
} from './api.js';
import { isCanary } from './incidents.js';
import { flagOf, MAX_ROWS, mergeNewest, mergeOlder, withoutAddresses } from './requests.js';
import type { Flag } from './requests.js';
import type { Feed } from './requests.js';

export interface AdminData {
  keys: Key[];
  upstreams: Upstream[];
  /** Newest first. */
  audit: AuditEntry[];
  price?: PriceDataStatus;
  settings?: Settings;
  actor?: string;
  /** Incidents not yet resolved. Undefined on a kman without them, or before they ever loaded. */
  incidents?: Incident[];
  /** Why the last fetch of the incidents failed: `incidents` is then old, or undefined. */
  incidentsError?: string;
  /** Exposure findings. Undefined as `incidents` is. */
  findings?: ExposureFinding[];
  findingsError?: string;
  /** Every canary's key id. Undefined as `incidents` is. */
  canaries?: ReadonlySet<string>;
  canariesError?: string;
}

/**
 * A fetch the rest of the screen can do without. A missing route is an older kman, which
 * leaves that part out; any other failure is kept, so it is never mistaken for "none".
 */
function optional<T>(fetch: Promise<T>): Promise<{ value?: T; error?: string }> {
  return fetch.then(
    (value) => ({ value }),
    (err: unknown) => {
      if (err instanceof ApiError && err.code === 'unauthorized') throw err;
      return isMissingRoute(err) ? {} : { error: err instanceof Error ? err.message : String(err) };
    },
  );
}

export type Link =
  | { state: 'connecting' }
  | { state: 'ok'; at: number }
  /** The API did not answer; `lastOk` is when it last did. */
  | { state: 'down'; error: string; lastOk?: number; answered?: boolean; status?: number }
  /** The API refused the token: retrying cannot help. */
  | { state: 'denied'; error: string };

export const AUDIT_LIMIT = 100;

/** Why a fetch did not give fresh data. With `partial` the rest of it did arrive. */
export interface Failure {
  message: string;
  partial?: boolean;
}

export function useAdmin(client: AdminClient, intervalMs = 3000) {
  const [data, setData] = useState<AdminData>({ keys: [], upstreams: [], audit: [] });
  const [link, setLink] = useState<Link>({ state: 'connecting' });
  const [now, setNow] = useState(() => Date.now());
  const [fresh, setFresh] = useState<ReadonlySet<number>>(new Set());
  const running = useRef<Promise<Failure | undefined> | undefined>(undefined);
  const queued = useRef<Promise<Failure | undefined> | undefined>(undefined);
  const denied = useRef<string | undefined>(undefined);
  const lastOk = useRef<number | undefined>(undefined);
  const maxAudit = useRef<number | undefined>(undefined);
  const actor = useRef<string | undefined>(undefined);

  const load = useCallback(async (): Promise<Failure | undefined> => {
    if (denied.current) return { message: denied.current };
    try {
      const [keys, upstreams, audit, price, settings, incidents, findings, canaries] = await Promise.all([
        client.listKeys(),
        client.listUpstreams(),
        client.latestAudit(AUDIT_LIMIT),
        client.priceStatus().catch(() => undefined),
        client.settings().catch(() => undefined),
        optional(client.openIncidents()),
        optional(client.exposure()),
        optional(client.canaries()),
      ]);
      actor.current ??= await client.whoami().catch(() => undefined);
      const newest = [...audit].reverse();
      // Entries that arrived since the last poll are marked until the next one.
      const prev = maxAudit.current;
      setFresh(prev === undefined ? new Set() : new Set(newest.filter((e) => e.id > prev).map((e) => e.id)));
      maxAudit.current = audit.reduce((m, e) => Math.max(m, e.id), prev ?? 0);
      // A failed fetch keeps the last good answer beside its error.
      setData((last) => ({
        keys,
        upstreams,
        audit: newest,
        price,
        // The last good answer stays: one that failed says nothing of a setting having changed.
        settings: settings ?? last.settings,
        actor: actor.current,
        incidents: incidents.error ? last.incidents : incidents.value,
        incidentsError: incidents.error,
        findings: findings.error ? last.findings : findings.value,
        findingsError: findings.error,
        canaries: canaries.error ? last.canaries : canaries.value && new Set(canaries.value.map((c) => c.id)),
        canariesError: canaries.error,
      }));
      // Always later than the last, even within a millisecond: what is fetched beside each
      // poll goes by this changing.
      lastOk.current = Math.max((lastOk.current ?? 0) + 1, Date.now());
      setNow(lastOk.current);
      setLink({ state: 'ok', at: lastOk.current });
      if (incidents.error) return { message: `incidents did not load: ${incidents.error}`, partial: true };
      return findings.error ? { message: `exposure did not load: ${findings.error}`, partial: true } : undefined;
    } catch (err) {
      if (err instanceof ApiError && err.code === 'unauthorized') {
        denied.current = err.message;
        setLink({ state: 'denied', error: err.message });
        return { message: err.message };
      }
      const message = err instanceof Error ? err.message : String(err);
      // An answer that is an error (a 500) is not the same as no answer: the server is there.
      const answered = err instanceof ApiError && err.code !== 'unreachable';
      setLink({ state: 'down', error: message, lastOk: lastOk.current, answered, status: err instanceof ApiError ? err.status : undefined });
      return { message };
    }
  }, [client]);

  /**
   * Fetches everything and resolves to why it failed, or undefined when the data is
   * fresh. A fetch already under way may have been asked for before the caller's
   * change, so it is not reused: one more starts when it ends (and callers who ask
   * meanwhile share that one).
   */
  const refresh = useCallback((): Promise<Failure | undefined> => {
    const current = running.current;
    if (!current) {
      const p = load().finally(() => {
        if (running.current === p) running.current = undefined;
      });
      running.current = p;
      return p;
    }
    queued.current ??= current.then(() => {
      queued.current = undefined;
      return refresh();
    });
    return queued.current;
  }, [load]);

  useEffect(() => {
    void refresh();
    // A poll never queues behind another fetch: the next tick comes soon enough.
    const t = setInterval(() => {
      if (!running.current) void refresh();
    }, intervalMs);
    return () => clearInterval(t);
  }, [refresh, intervalMs]);

  return { data, link, now, fresh, refresh };
}

export interface PlacesState {
  /** The key they are for. */
  id?: string;
  places?: KeyPlaces;
  /** Why the last fetch failed: `places` is then old, or undefined. */
  error?: string;
  /** The server has no such route (an older kman). */
  unsupported?: boolean;
}

/**
 * Where the key `id` was used from, fetched when it changes and again on every poll
 * (`at` is when the last one answered). The last answer stays while the next is fetched,
 * and an answer for a key that is no longer shown is dropped.
 */
export function usePlaces(client: AdminClient, id: string | undefined, at: number): PlacesState {
  const [state, setState] = useState<PlacesState>({});
  useEffect(() => {
    if (!id) return;
    let live = true;
    client.keyPlaces(id).then(
      (places) => live && setState({ id, places }),
      (err: unknown) => {
        if (!live) return;
        if (isMissingRoute(err)) return setState({ id, unsupported: true });
        const error = err instanceof Error ? err.message : String(err);
        setState((s) => ({ id, places: s.id === id ? s.places : undefined, error }));
      },
    );
    return () => {
      live = false;
    };
  }, [client, id, at]);
  return state.id === id ? state : { id };
}

export interface IncidentState {
  /** The incident this is about. */
  id?: number;
  detail?: IncidentDetail;
  /** Why the last fetch failed: `detail` is then old, or undefined. */
  error?: string;
}

/**
 * One incident in full, fetched when it is opened and again on every poll (`at` is when the
 * last one answered). Fetching it changes nothing: opening an incident never acknowledges it.
 */
export function useIncident(client: AdminClient, id: number | undefined, at: number): IncidentState & { show: (detail: IncidentDetail) => void } {
  const [state, setState] = useState<IncidentState>({});
  useEffect(() => {
    if (id === undefined) return setState({});
    let live = true;
    client.incident(id).then(
      (detail) => live && setState({ id, detail }),
      (err: unknown) => {
        if (!live) return;
        const error = err instanceof Error ? err.message : String(err);
        setState((s) => ({ id, detail: s.id === id ? s.detail : undefined, error }));
      },
    );
    return () => {
      live = false;
    };
  }, [client, id, at]);
  /** Takes a detail fetched elsewhere, so what it holds is on screen without waiting for a poll. */
  const show = useCallback((detail: IncidentDetail) => setState({ id: detail.id, detail }), []);
  return { ...(state.id === id ? state : { id }), show };
}

/** The events asked for at a time. */
export const EVENT_PAGE = 200;
/** What a poll asks for once rows are held: it reads further back only if more than this arrived. */
export const POLL_PAGE = 25;
/**
 * The most pages one poll reads to meet the rows already held before it starts over:
 * enough to cross as many new requests as can ever be held, and a few to spare.
 */
const CATCH_UP_PAGES = (2 * MAX_ROWS) / EVENT_PAGE + 5;

export interface Requests extends Feed {
  /** Whether anything has been answered for this query. */
  loaded: boolean;
  /** When the newest rows were last fetched. */
  at?: number;
  /** Why the last fetch failed: the rows are then old. */
  error?: string;
  /** The server has no such route (an older kman). */
  unsupported?: boolean;
  loadingOlder: boolean;
  /** How many times a selected request has had to be let go, whichever the table: it only grows. */
  lost?: number;
}

const noRequests: Requests = { rows: [], more: false, loaded: false, loadingOlder: false };

/**
 * The stored requests of one key, or all of them, newest first: the newest fetched again on
 * every poll and joined to what is held, and older ones a page at a time on demand. The
 * live event stream is not used, as its events have no id to keep a place by.
 *
 * `at` is when the last poll answered, 0 before one has. `selected` is the request not to
 * let go of, and `exposed` whether the install gives addresses out, when that is known.
 */
export function useRequests(
  client: AdminClient,
  key: string | undefined,
  kind: RequestEvent['kind'] | undefined,
  at: number,
  { selected, exposed }: { selected?: number; exposed?: boolean } = {},
) {
  const query = `${key ?? ''}|${kind ?? ''}`;
  const [state, setState] = useState<Requests & { query: string }>({ ...noRequests, query });
  const held = useRef(state);
  held.current = state;
  const keep = useRef(selected);
  keep.current = selected;
  // Each change of query starts a generation, and an answer from an earlier one is
  // dropped: after going away and back, a slow one would undo what has loaded since.
  const generation = useRef(0);
  const asked = useRef(query);
  const polling = useRef(false);
  const older = useRef(false);
  if (asked.current !== query) {
    asked.current = query;
    generation.current++;
    polling.current = false;
    older.current = false;
  }
  // Without a key the route is always there on a kman that has it, so only that answer
  // says an older kman lacks it: an unknown key is not found too.
  const unsupported = useRef(false);
  // The install began giving addresses out: the rows held lack them, so are read again.
  const wasExposed = useRef(exposed);
  const again = useRef(false);

  const update = useCallback((q: string, change: (s: Requests) => Partial<Requests>) => {
    setState((s) => {
      const was = s.query === q ? s : { ...noRequests, query: q, lost: s.lost };
      return { ...was, ...change(was), query: q };
    });
  }, []);

  // Ahead of the poll: what it asks for from now on is what has the addresses.
  useEffect(() => {
    if (exposed === undefined) return;
    if (wasExposed.current === false && exposed) {
      again.current = true;
      generation.current++;
      polling.current = false;
      older.current = false;
    }
    wasExposed.current = exposed;
  }, [exposed]);

  useEffect(() => {
    if (!at || polling.current || unsupported.current) return;
    polling.current = true;
    const mine = generation.current;
    const stale = () => mine !== generation.current;
    const anew = again.current;
    const rows = held.current.query === query ? held.current.rows : [];
    // How far back to read: to what is held, or, reading it all again, to the oldest held.
    const reach = (anew ? rows[rows.length - 1] : rows[0])?.id;
    const first = anew || !rows.length ? EVENT_PAGE : POLL_PAGE;
    const base: EventQuery = { key, kind };
    void (async () => {
      try {
        const pages: EventPage[] = [await client.events({ ...base, limit: first })];
        // More may have arrived than a page holds.
        for (let last = pages[0]!; !stale() && reach !== undefined && last.more && last.next_before! > reach && pages.length < CATCH_UP_PAGES; ) {
          last = await client.events({ ...base, limit: EVENT_PAGE, before: last.next_before });
          pages.push(last);
        }
        if (stale()) return;
        if (anew) again.current = false;
        const selected = keep.current;
        update(query, (s) => {
          const next = mergeNewest(anew ? { rows: [], more: false } : s, pages, selected);
          // More arrived than could be read back across: the selected request was let go.
          const gone = selected !== undefined && s.rows.some((r) => r.id === selected) && !next.rows.some((r) => r.id === selected);
          return { ...next, lost: (s.lost ?? 0) + (gone ? 1 : 0), loaded: true, at: Date.now(), error: undefined };
        });
      } catch (err) {
        if (stale()) return;
        if (!key && isMissingRoute(err)) {
          unsupported.current = true;
          return update(query, () => ({ unsupported: true }));
        }
        update(query, () => ({ error: err instanceof Error ? err.message : String(err) }));
      } finally {
        if (!stale()) polling.current = false;
      }
    })();
  }, [client, query, key, kind, at, exposed, update]);

  // The install stopped giving addresses out: those held go, whatever the next page holds.
  useEffect(() => {
    if (exposed === false) setState((s) => (s.rows.some((r) => r.client_address) ? { ...s, rows: withoutAddresses(s.rows) } : s));
  }, [exposed, state.rows]);

  const loadOlder = useCallback(() => {
    const s = held.current;
    const oldest = s.rows.at(-1)?.id;
    const room = MAX_ROWS - s.rows.length;
    if (s.query !== query || !s.more || oldest === undefined || room <= 0 || older.current) return;
    older.current = true;
    const mine = generation.current;
    update(query, () => ({ loadingOlder: true }));
    client.events({ key, kind, limit: Math.min(EVENT_PAGE, room), before: oldest }).then(
      (page) => {
        if (mine !== generation.current) return;
        older.current = false;
        update(query, (now) => ({ ...mergeOlder(now, page, oldest), loadingOlder: false }));
      },
      (err: unknown) => {
        if (mine !== generation.current) return;
        older.current = false;
        update(query, () => ({ loadingOlder: false, error: err instanceof Error ? err.message : String(err) }));
      },
    );
  }, [client, query, key, kind, update]);

  // A state left from another query shows nothing, and one being gone back to is not "loading older".
  const shown: Requests = state.query === query ? { ...state, loadingOlder: state.loadingOlder && older.current } : noRequests;
  // The count of what was let go is of every table: one that went back to nothing and
  // returned would be taken for a new loss.
  return { ...shown, lost: state.lost ?? 0, unsupported: unsupported.current, loadOlder };
}

/**
 * What flags a request, remembered for each row until what flags are worked out from
 * changes: most polls bring the same incidents and mostly the same rows.
 */
export function useFlags(
  canaries: ReadonlySet<string> | undefined,
  incidents: Incident[] | undefined,
  { details, version }: { details: ReadonlyMap<number, IncidentDetail>; version: number },
) {
  const basis = JSON.stringify([
    canaries && [...canaries],
    incidents?.filter((i) => i.state !== 'resolved').map((i) => [i.id, i.key_id, i.opened_at, i.rules]),
    version,
  ]);
  return useMemo(() => {
    const sources = { canaries, incidents, details };
    const known = new WeakMap<RequestEvent, { flag: Flag | undefined }>();
    return (e: RequestEvent) => {
      let held = known.get(e);
      if (!held) known.set(e, (held = { flag: flagOf(e, sources) }));
      return held.flag;
    };
  }, [basis]);
}

/** The requests a key's detail lists. */
export const KEY_REQUESTS = 5;

export interface KeyRequestsState {
  id?: string;
  events?: RequestEvent[];
  error?: string;
}

/**
 * The last few requests of the key `id`, fetched again on every poll, one read at a time:
 * an answer slower than a poll still counts while the key is still the one shown.
 */
export function useKeyRequests(client: AdminClient, id: string | undefined, at: number): KeyRequestsState {
  const [state, setState] = useState<KeyRequestsState>({});
  const shown = useRef(id);
  shown.current = id;
  const reading = useRef<{ id: string }>(undefined);
  useEffect(() => {
    if (!id || reading.current?.id === id) return;
    const mine = (reading.current = { id });
    const wanted = () => reading.current === mine && shown.current === id;
    client
      .events({ key: id, limit: KEY_REQUESTS })
      .then(
        (page) => {
          if (wanted()) setState({ id, events: page.events });
        },
        (err: unknown) => {
          if (!wanted()) return;
          // An older kman, or a key that has gone: nothing to list.
          if (isMissingRoute(err)) return setState({ id });
          const error = err instanceof Error ? err.message : String(err);
          setState((s) => ({ id, events: s.id === id ? s.events : undefined, error }));
        },
      )
      .finally(() => {
        if (reading.current === mine) reading.current = undefined;
      });
  }, [client, id, at]);
  return state.id === id ? state : { id };
}

/** Details fetched at once, so a hundred open incidents do not become a hundred requests together. */
const DETAILS_AT_ONCE = 6;

/** What makes a held detail old: another firing, or addresses being given out or not. */
const detailMark = (i: Incident, exposed: boolean | undefined) => `${i.last_fired_at}|${i.detection_count}|${exposed}`;

/**
 * The detail of each unresolved incident that is not a canary's, which is what says which
 * requests look like it. Fetched again only when the incident fires or counts more, or
 * `exposed` changes, which changes what its evidence holds.
 * `incomplete` says one is missing or behind, so a request may lack its flag.
 */
export function useIncidentDetails(client: AdminClient, incidents: Incident[] | undefined, at: number, exposed?: boolean) {
  const cache = useRef(new Map<number, { mark: string; detail: IncidentDetail }>());
  const fetching = useRef(new Set<number>());
  const [version, setVersion] = useState(0);
  const wanted = (incidents ?? []).filter((i) => i.state !== 'resolved' && !isCanary(i));
  useEffect(() => {
    // What is resolved is let go.
    const ids = new Set(wanted.map((i) => i.id));
    for (const id of cache.current.keys()) if (!ids.has(id)) cache.current.delete(id);
    const stale = wanted.filter((i) => cache.current.get(i.id)?.mark !== detailMark(i, exposed) && !fetching.current.has(i.id));
    if (stale.length === 0) return;
    for (const i of stale) fetching.current.add(i.id);
    const queue = [...stale];
    const worker = async () => {
      for (let i = queue.shift(); i; i = queue.shift()) {
        try {
          cache.current.set(i.id, { mark: detailMark(i, exposed), detail: await client.incident(i.id) });
          setVersion((v) => v + 1);
        } catch {
          // Tried again on the next poll; what was held stays, and the table says it may be behind.
        } finally {
          fetching.current.delete(i.id);
        }
      }
    };
    for (let n = 0; n < DETAILS_AT_ONCE; n++) void worker();
  }, [client, incidents, at, exposed]);
  return useMemo(() => {
    const details = new Map<number, IncidentDetail>();
    let incomplete = false;
    for (const i of wanted) {
      const held = cache.current.get(i.id);
      if (held) details.set(i.id, held.detail);
      if (held?.mark !== detailMark(i, exposed)) incomplete = true;
    }
    // `version` changes only when a detail does.
    return { details, incomplete, version };
  }, [incidents, version, exposed]);
}
