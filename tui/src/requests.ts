// The requests table as plain data: which requests are flagged and why, and how what each
// poll brings is joined to the rows already held.
import { BlockList, isIP } from 'node:net';
import type { Detection, EventPage, Incident, IncidentDetail, RequestEvent } from './api.js';
import { isCanary } from './incidents.js';
import { ruleText } from './risk.js';

export type RequestFilter = 'all' | 'flagged' | 'refused';
export const FILTERS: RequestFilter[] = ['all', 'flagged', 'refused'];
export const nextFilter = (f: RequestFilter) => FILTERS[(FILTERS.indexOf(f) + 1) % FILTERS.length]!;

// ---- Flags ----------------------------------------------------------------

export interface Flag {
  /** danger: a canary, or like an open incident. warn: refused, or no key kman knows. */
  severity: 'danger' | 'warn';
  why: string;
  /** The unresolved incident it is flagged for, which Enter opens. */
  incident?: number;
  canary?: boolean;
}

/** What flags are derived from, as events carry none. */
export interface FlagSources {
  canaries?: ReadonlySet<string>;
  /** The unresolved incidents. */
  incidents?: Incident[];
  details: ReadonlyMap<number, IncidentDetail>;
}

/** Whether an address is in a network such as 203.0.113.0/24. */
function inNetwork(address: string, network: string): boolean {
  const [base = '', bits = ''] = network.split('/');
  const family = (a: string) => (isIP(a) === 6 ? 'ipv6' : 'ipv4');
  if (!isIP(base) || !isIP(address) || !/^\d+$/.test(bits)) return false;
  const list = new BlockList();
  list.addSubnet(base, Number(bits), family(base));
  return list.check(address, family(address));
}

/**
 * Whether a request looks like what a detection saw: every one of country, ASN and client
 * that it recorded is the request's, what was new among them. A new network needs the
 * network too, which can only be told while addresses are exposed. A detection that
 * recorded only a client counts only for a new client: on its own, an app's own name for
 * itself says too little.
 */
export function matches({ rule, evidence: seen }: Detection, e: RequestEvent): boolean {
  if (seen.country && seen.country !== e.country) return false;
  if (seen.asn && seen.asn !== e.asn) return false;
  if (seen.client && seen.client !== e.client) return false;
  if (rule === 'new_country' && !seen.country) return false;
  if (rule === 'new_asn' && !seen.asn) return false;
  if (rule === 'new_client' && !seen.client) return false;
  if (rule === 'new_network') return Boolean(seen.network && e.client_address && inNetwork(e.client_address, seen.network));
  if (!seen.country && !seen.asn) return rule === 'new_client';
  return true;
}

export function flagOf(e: RequestEvent, { canaries, incidents, details }: FlagSources): Flag | undefined {
  const incident = e.key_id ? incidents?.find((i) => i.key_id === e.key_id && i.state !== 'resolved') : undefined;
  // A canary's incident says so too, for when the canaries could not be listed.
  if (e.key_id && (canaries?.has(e.key_id) || (incident && isCanary(incident)))) {
    return { severity: 'danger', why: `used a canary key${incident ? `: incident #${incident.id}` : ''}`, incident: incident?.id, canary: true };
  }
  // A request from before the incident opened is not part of it, however alike.
  if (incident && Date.parse(e.time) >= Date.parse(incident.opened_at)) {
    const rules = [...new Set((details.get(incident.id)?.detections ?? []).filter((d) => matches(d, e)).map((d) => ruleText(d.rule)))];
    if (rules.length) return { severity: 'danger', why: `like incident #${incident.id}: ${rules.join(', ')}`, incident: incident.id };
  }
  if (e.unidentified_key) return { severity: 'warn', why: `no key kman knows${e.refusal_code ? `: ${e.refusal_code}` : ''}` };
  if (e.kind === 'refused') return { severity: 'warn', why: `refused${e.refusal_code ? `: ${e.refusal_code}` : ''}` };
  return undefined;
}

/** "403 #12 address_not_allowed": the status, the incident it is flagged for, and the refusal. */
export function resultText(e: RequestEvent, flag: Flag | undefined): string {
  return [e.status || (e.kind === 'refused' ? 'refused' : '—'), flag?.incident === undefined ? '' : `#${flag.incident}`, flag?.canary ? 'canary' : (e.refusal_code ?? '')]
    .filter(Boolean)
    .join(' ');
}

export const requestKey = (e: RequestEvent) => (e.unidentified_key || !e.key_id ? '(no key)' : e.key_name || e.key_id);

export const requestFrom = (e: RequestEvent) => [e.country, e.asn ? `AS${e.asn}` : ''].filter(Boolean).join(' ') || '—';

// ---- The rows held --------------------------------------------------------

export interface Feed {
  /** Newest first. */
  rows: RequestEvent[];
  /** There are older events to ask for. */
  more: boolean;
}

/** The most rows held: past it the oldest are let go, and can be asked for again. */
export const MAX_ROWS = 5000;

/** An address given out earlier is not kept once the install stops giving them out. */
const withhold = (rows: RequestEvent[], pages: EventPage[]) =>
  pages.some((p) => p.client_addresses_withheld) ? rows.map(({ client_address: _, ...r }) => r) : rows;

const byNewest = (rows: RequestEvent[]) => [...new Map(rows.map((r) => [r.id, r])).values()].sort((a, b) => b.id - a.id);

/**
 * The rows held joined with the newest pages of a poll (newest page first). Pages that do
 * not reach what is held replace it, so a stretch is never missing from the middle.
 */
export function mergeNewest(feed: Feed, pages: EventPage[]): Feed {
  const fresh = pages.flatMap((p) => p.events);
  const last = pages[pages.length - 1]!;
  const newestHeld = feed.rows[0]?.id;
  const oldestFresh = fresh.at(-1)?.id;
  const joins = newestHeld !== undefined && oldestFresh !== undefined && oldestFresh <= newestHeld;
  // With nothing more to ask for, the pages are everything there is.
  const keep = joins && last.more;
  const rows = byNewest([...fresh, ...(keep ? feed.rows : [])]);
  return {
    rows: withhold(rows.slice(0, MAX_ROWS), pages),
    more: rows.length > MAX_ROWS || (keep ? feed.more : last.more),
  };
}

/** The rows held with the page that follows the one with id `before`. */
export function mergeOlder(feed: Feed, page: EventPage, before: number): Feed {
  // The rows moved on while it was asked for: it no longer follows them.
  if (feed.rows.at(-1)?.id !== before) return feed;
  return { rows: withhold(byNewest([...feed.rows, ...page.events]), [page]), more: page.more };
}
