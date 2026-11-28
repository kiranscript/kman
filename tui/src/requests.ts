// The requests table as plain data: which requests are flagged and why, and how what each
// poll brings is joined to the rows already held.
import { isIP } from 'node:net';
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

// Every request of a key with an open incident is compared with every one of its
// detections, thousands by a hundred: an address and a network are each read once, and
// compared as text.

/** An address as hex digits, 8 for IPv4 (mapped into IPv6 or not) and 32 for IPv6. */
function hexOf(address: string): string | undefined {
  const family = isIP(address);
  if (!family) return undefined;
  if (family === 4) return address.split('.').map((n) => Number(n).toString(16).padStart(2, '0')).join('');
  // Without its zone, and with an IPv4 tail written as the two groups it is.
  const text = address.split('%')[0]!.replace(/\d+\.\d+\.\d+\.\d+$/, (v4) => hexOf(v4)!.replace(/^(.{4})/, '$1:'));
  const [front, back] = text.split('::').map((part) => (part ? part.split(':') : []));
  const groups = back === undefined ? front! : [...front!, ...Array<string>(8 - front!.length - back.length).fill('0'), ...back];
  const hex = groups.map((g) => g.padStart(4, '0')).join('').toLowerCase();
  return hex.startsWith('00000000000000000000ffff') ? hex.slice(24) : hex;
}

/** A network's whole hex digits, and the bits of the next one when its length ends inside it. */
interface Network {
  digits: number;
  prefix: string;
  last?: { value: number; shift: number };
}

function networkOf(network: string): Network | undefined {
  const [address = '', length = '', ...rest] = network.split('/');
  const hex = hexOf(address);
  const bits = Number(length);
  if (rest.length || !hex || !/^\d+$/.test(length) || bits > hex.length * 4) return undefined;
  const whole = Math.floor(bits / 4);
  const shift = 4 - (bits % 4);
  return { digits: hex.length, prefix: hex.slice(0, whole), last: shift < 4 ? { value: parseInt(hex[whole]!, 16) >> shift, shift } : undefined };
}

const networks = new WeakMap<Detection, Network | undefined>();
const addresses = new WeakMap<RequestEvent, string | undefined>();

function once<K extends object, V>(cache: WeakMap<K, V>, key: K, read: () => V): V {
  if (!cache.has(key)) cache.set(key, read());
  return cache.get(key)!;
}

/** Whether the request's address is in the network the detection recorded, such as 203.0.113.0/24. */
function inNetwork(e: RequestEvent, d: Detection): boolean {
  const net = once(networks, d, () => networkOf(d.evidence.network ?? ''));
  const hex = once(addresses, e, () => hexOf(e.client_address ?? ''));
  if (!net || !hex || hex.length !== net.digits || !hex.startsWith(net.prefix)) return false;
  return !net.last || parseInt(hex[net.prefix.length]!, 16) >> net.last.shift === net.last.value;
}

/**
 * Whether a request looks like what a detection saw: every one of country, ASN and client
 * that it recorded is the request's, what was new among them. A new network needs the
 * network too, which can only be told while addresses are exposed. A detection that
 * recorded only a client counts only for a new client: on its own, an app's own name for
 * itself says too little.
 */
export function matches(d: Detection, e: RequestEvent): boolean {
  const { rule, evidence: seen } = d;
  if (seen.country && seen.country !== e.country) return false;
  if (seen.asn && seen.asn !== e.asn) return false;
  if (seen.client && seen.client !== e.client) return false;
  if (rule === 'new_country' && !seen.country) return false;
  if (rule === 'new_asn' && !seen.asn) return false;
  if (rule === 'new_client' && !seen.client) return false;
  if (rule === 'new_network') return inNetwork(e, d);
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
    const rules = new Set<string>();
    // A rule already found to match is not looked for again.
    for (const d of details.get(incident.id)?.detections ?? []) if (!rules.has(d.rule) && matches(d, e)) rules.add(d.rule);
    if (rules.size) return { severity: 'danger', why: `like incident #${incident.id}: ${[...rules].map(ruleText).join(', ')}`, incident: incident.id };
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

/**
 * The most rows held: no older ones are asked for past it, and as new ones arrive the
 * oldest are let go. The selected request is not, until twice as many are held.
 */
export const MAX_ROWS = 5000;

/** An address given out earlier is not kept once the install stops giving them out. */
export const withoutAddresses = (rows: RequestEvent[]) =>
  rows.map((r) => {
    if (r.client_address === undefined) return r;
    const { client_address: _, ...rest } = r;
    return rest;
  });

const withhold = (rows: RequestEvent[], pages: EventPage[]) => (pages.some((p) => p.client_addresses_withheld) ? withoutAddresses(rows) : rows);

/** One row an id, newest first. Of two with one id the later in `rows` stays. */
const byNewest = (rows: RequestEvent[]) => [...new Map(rows.map((r) => [r.id, r])).values()].sort((a, b) => b.id - a.id);

const sameRow = (a: RequestEvent, b: RequestEvent) => {
  const fields = Object.keys(a) as (keyof RequestEvent)[];
  return fields.length === Object.keys(b).length && fields.every((f) => a[f] === b[f]);
};

/** `next`, or `last` itself when nothing in it differs: what is worked out from rows is then not worked out again. */
const unchanged = (last: RequestEvent[], next: RequestEvent[]) => (last.length === next.length && next.every((r, n) => r === last[n]) ? last : next);

/** How many rows to hold: the most, or as many as it takes to keep the one with id `keep`. */
function held(rows: RequestEvent[], keep: number | undefined): number {
  const at = keep === undefined ? -1 : rows.findIndex((r) => r.id === keep);
  return Math.min(2 * MAX_ROWS, Math.max(MAX_ROWS, at + 1));
}

/**
 * The rows held joined with the newest pages of a poll (newest page first). Pages that do
 * not reach what is held replace it, so a stretch is never missing from the middle. `keep`
 * is the selected request.
 */
export function mergeNewest(feed: Feed, pages: EventPage[], keep?: number): Feed {
  const fresh = pages.flatMap((p) => p.events);
  const last = pages[pages.length - 1]!;
  const newestHeld = feed.rows[0]?.id;
  const oldestFresh = fresh.at(-1)?.id;
  const joins = newestHeld !== undefined && oldestFresh !== undefined && oldestFresh <= newestHeld;
  // With nothing more to ask for, the pages are everything there is.
  const older = joins && last.more;
  // What was just fetched wins over what is held, which keeps its own object while the two say the same.
  const was = new Map(feed.rows.map((r) => [r.id, r]));
  const now = fresh.map((r) => {
    const last = was.get(r.id);
    return last && sameRow(last, r) ? last : r;
  });
  const rows = byNewest([...(older ? feed.rows : []), ...now]);
  const most = held(rows, keep);
  return {
    rows: unchanged(feed.rows, withhold(rows.slice(0, most), pages)),
    more: rows.length > most || (older ? feed.more : last.more),
  };
}

/** The rows held with the page that follows the one with id `before`. */
export function mergeOlder(feed: Feed, page: EventPage, before: number): Feed {
  // The rows moved on while it was asked for: it no longer follows them.
  if (feed.rows.at(-1)?.id !== before) return feed;
  return { rows: withhold(byNewest([...feed.rows, ...page.events]), [page]), more: page.more };
}
