// Polls the admin API and keeps the last good answer, so a dropped connection
// shows stale data marked as stale rather than an empty screen.
import { useCallback, useEffect, useRef, useState } from 'react';
import { ApiError } from './api.js';
import type { AdminClient, AuditEntry, Key, PriceDataStatus, Settings, Upstream } from './api.js';

export interface AdminData {
  keys: Key[];
  upstreams: Upstream[];
  /** Newest first. */
  audit: AuditEntry[];
  price?: PriceDataStatus;
  settings?: Settings;
  actor?: string;
}

export type Link =
  | { state: 'connecting' }
  | { state: 'ok'; at: number }
  /** The API did not answer; `lastOk` is when it last did. */
  | { state: 'down'; error: string; lastOk?: number; answered?: boolean }
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
      const [keys, upstreams, audit, price, settings] = await Promise.all([
        client.listKeys(),
        client.listUpstreams(),
        client.latestAudit(AUDIT_LIMIT),
        client.priceStatus().catch(() => undefined),
        client.settings().catch(() => undefined),
      ]);
      actor.current ??= await client.whoami().catch(() => undefined);
      const newest = [...audit].reverse();
      // Entries that arrived since the last poll are marked until the next one.
      const prev = maxAudit.current;
      setFresh(prev === undefined ? new Set() : new Set(newest.filter((e) => e.id > prev).map((e) => e.id)));
      maxAudit.current = audit.reduce((m, e) => Math.max(m, e.id), prev ?? 0);
      setData({ keys, upstreams, audit: newest, price, settings, actor: actor.current });
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
      setLink({ state: 'down', error: message, lastOk: lastOk.current, answered });
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
