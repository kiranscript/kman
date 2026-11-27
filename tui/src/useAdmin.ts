// Polls the admin API and keeps the last good answer, so a dropped connection
// shows stale data marked as stale rather than an empty screen.
import { useCallback, useEffect, useRef, useState } from 'react';
import { ApiError } from './api.js';
import type { AdminClient, AuditEntry, ExposureFinding, Incident, Key, KeyPlaces, PriceDataStatus, Settings, Upstream } from './api.js';

export interface AdminData {
  keys: Key[];
  upstreams: Upstream[];
  /** Newest first. */
  audit: AuditEntry[];
  price?: PriceDataStatus;
  settings?: Settings;
  actor?: string;
  /** Incidents not yet resolved. Undefined when the server did not say (an older kman). */
  incidents?: Incident[];
  /** Exposure findings. Undefined when the server did not say. */
  findings?: ExposureFinding[];
}

export type Link =
  | { state: 'connecting' }
  | { state: 'ok'; at: number }
  /** The API did not answer; `lastOk` is when it last did. */
  | { state: 'down'; error: string; lastOk?: number; answered?: boolean; status?: number }
  /** The API refused the token: retrying cannot help. */
  | { state: 'denied'; error: string };

export const AUDIT_LIMIT = 100;

export function useAdmin(client: AdminClient, intervalMs = 3000) {
  const [data, setData] = useState<AdminData>({ keys: [], upstreams: [], audit: [] });
  const [link, setLink] = useState<Link>({ state: 'connecting' });
  const [now, setNow] = useState(() => Date.now());
  const [fresh, setFresh] = useState<ReadonlySet<number>>(new Set());
  const running = useRef<Promise<string | undefined> | undefined>(undefined);
  const queued = useRef<Promise<string | undefined> | undefined>(undefined);
  const denied = useRef<string | undefined>(undefined);
  const lastOk = useRef<number | undefined>(undefined);
  const maxAudit = useRef<number | undefined>(undefined);
  const actor = useRef<string | undefined>(undefined);

  const load = useCallback(async (): Promise<string | undefined> => {
    if (denied.current) return denied.current;
    try {
      const [keys, upstreams, audit, price, settings, incidents, findings] = await Promise.all([
        client.listKeys(),
        client.listUpstreams(),
        client.latestAudit(AUDIT_LIMIT),
        client.priceStatus().catch(() => undefined),
        client.settings().catch(() => undefined),
        client.openIncidents().catch(() => undefined),
        client.exposure().catch(() => undefined),
      ]);
      actor.current ??= await client.whoami().catch(() => undefined);
      const newest = [...audit].reverse();
      // Entries that arrived since the last poll are marked until the next one.
      const prev = maxAudit.current;
      setFresh(prev === undefined ? new Set() : new Set(newest.filter((e) => e.id > prev).map((e) => e.id)));
      maxAudit.current = audit.reduce((m, e) => Math.max(m, e.id), prev ?? 0);
      setData({ keys, upstreams, audit: newest, price, settings, actor: actor.current, incidents, findings });
      lastOk.current = Date.now();
      setNow(lastOk.current);
      setLink({ state: 'ok', at: lastOk.current });
      return undefined;
    } catch (err) {
      if (err instanceof ApiError && err.code === 'unauthorized') {
        denied.current = err.message;
        setLink({ state: 'denied', error: err.message });
        return err.message;
      }
      const message = err instanceof Error ? err.message : String(err);
      // An answer that is an error (a 500) is not the same as no answer: the server is there.
      const answered = err instanceof ApiError && err.code !== 'unreachable';
      setLink({ state: 'down', error: message, lastOk: lastOk.current, answered, status: err instanceof ApiError ? err.status : undefined });
      return message;
    }
  }, [client]);

  /**
   * Fetches everything and resolves to why it failed, or undefined when the data is
   * fresh. A fetch already under way may have been asked for before the caller's
   * change, so it is not reused: one more starts when it ends (and callers who ask
   * meanwhile share that one).
   */
  const refresh = useCallback((): Promise<string | undefined> => {
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
  error?: string;
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
      (err: unknown) => live && setState((s) => (s.id === id && s.places ? s : { id, error: err instanceof Error ? err.message : String(err) })),
    );
    return () => {
      live = false;
    };
  }, [client, id, at]);
  return state.id === id ? state : { id };
}
