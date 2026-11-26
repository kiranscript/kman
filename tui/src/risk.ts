// "Key at risk" (GLOSSARY): a virtual key whose settings or state would make a
// leak more costly. v0.1.0 has no address locks, so the signals are no spend
// cap, close to a cap, and about to expire (or already expired).
import { BlockList, isIP } from 'node:net';
import type { Key, SpendPeriod, Upstream } from './api.js';
import { daysCeil, daysLeft } from './format.js';

export type Severity = 'danger' | 'warn' | 'info';

export interface Risk {
  severity: Severity;
  label: string;
  /** Two words at most, for a list row: "near cap", "expiring". */
  short: string;
  listLabel?: string;
  /** How pressing it is among risks of one severity: higher is sooner. */
  weight: number;
}

/** Fraction of a cap used, counting what is reserved for requests in flight. Undefined with no cap. */
export function capRatio(p: SpendPeriod | undefined): number | undefined {
  if (!p?.cap_micro) return undefined;
  return (p.cost_micro + (p.reserved_micro ?? 0)) / p.cap_micro;
}

/** The most-used of a key's caps, with its name. */
export function tightestCap(key: Key): { ratio: number; period: 'daily' | 'monthly' } | undefined {
  const today = capRatio(key.spend?.today);
  const month = capRatio(key.spend?.month);
  if (today === undefined && month === undefined) return undefined;
  if (month === undefined || (today !== undefined && today >= month)) return { ratio: today ?? 0, period: 'daily' };
  return { ratio: month, period: 'monthly' };
}

export const NEAR_CAP = 0.8;
export const EXPIRING_DAYS = 7;

/** Risks for a key that can still be used. A disabled or revoked key is contained, so it has none. */
export function keyRisks(key: Key, now: number): Risk[] {
  if (key.status !== 'active') return [];
  const risks: Risk[] = [];

  if (key.expired) {
    risks.push({ severity: 'danger', label: 'expired', short: 'expired', weight: 2000 });
  } else if (daysLeft(key.expires_at, now) < EXPIRING_DAYS) {
    const d = daysLeft(key.expires_at, now);
    const hours = Math.max(0, (Date.parse(key.expires_at) - now) / 3_600_000);
    risks.push({
      severity: 'warn',
      label: d < 1 ? 'expires within a day' : `expires in ${daysCeil(key.expires_at, now)} days`,
      short: 'expiring',
      weight: 1000 - hours, // fewer hours left, sooner
    });
  }

  const cap = tightestCap(key);
  if (!cap) {
    risks.push({ severity: 'info', label: 'no spend cap', short: 'no cap', weight: 0 });
  } else if (cap.ratio >= 1) {
    risks.push({ severity: 'danger', label: `at its ${cap.period} cap`, short: 'at cap', listLabel: `${Math.round(cap.ratio * 100)}% ${cap.period}`, weight: 1500 });
  } else if (cap.ratio >= NEAR_CAP) {
    risks.push({ severity: 'warn', label: `${Math.round(cap.ratio * 100)}% of its ${cap.period} cap`, short: 'near cap', listLabel: `${Math.round(cap.ratio * 100)}% ${cap.period}`, weight: cap.ratio * 1000 });
  }

  const unmetered = key.spend?.today.unmetered_requests ?? 0;
  if (unmetered > 0) {
    risks.push({
      severity: 'warn',
      label: `${unmetered} unmetered today`,
      short: 'unmetered',
      weight: 500,
    });
  }
  return risks;
}

const order: Record<Severity, number> = { danger: 0, warn: 1, info: 2 };

export function worst(risks: Risk[]): Severity | undefined {
  return risks.map((r) => r.severity).sort((a, b) => order[a] - order[b])[0];
}

/** The one risk to name for a key: the worst severity, and within it the most pressing. */
export function topRisk(risks: Risk[]): Risk | undefined {
  return [...risks].sort((a, b) => order[a.severity] - order[b.severity] || b.weight - a.weight)[0];
}

export interface Attention {
  key: Key;
  risks: Risk[];
  top: Severity;
}

/** Active keys with something to look at, worst first. */
export function attention(keys: Key[], now: number): Attention[] {
  return keys
    .map((key) => ({ key, risks: keyRisks(key, now) }))
    .filter((a) => a.risks.length > 0)
    .map((a) => ({ ...a, top: worst(a.risks) ?? 'info' }))
    .sort(
      (a, b) =>
        order[a.top] - order[b.top] ||
        weightOf(b) - weightOf(a) ||
        (a.key.name ?? a.key.id).localeCompare(b.key.name ?? b.key.id),
    );
}

/** The most pressing risk of a key, for ordering keys of one severity. */
function weightOf(a: { risks: Risk[]; top: Severity }): number {
  return Math.max(...a.risks.filter((r) => r.severity === a.top).map((r) => r.weight));
}

export interface UpstreamAttention {
  upstream: Upstream;
  risks: Risk[];
  top: Severity;
}

const loopback = new BlockList();
loopback.addSubnet('127.0.0.0', 8);
loopback.addAddress('::1', 'ipv6');

function isLoopbackHost(host: string): boolean {
  if (/^localhost\.?$/i.test(host)) return true;
  const family = isIP(host);
  return family !== 0 && loopback.check(host, family === 6 ? 'ipv6' : 'ipv4');
}

/** What is wrong with an upstream: no credential (its keys cannot work), plain http, a newer preset. */
export function upstreamRisks(u: Upstream, includeLocalTransport = true): Risk[] {
  const risks: Risk[] = [];
  if (u.credentials.length === 0) risks.push({ severity: 'danger', label: 'no credential: its keys cannot work', short: 'no credential', weight: 2000 });
  if (u.tls === false && (includeLocalTransport || !isLoopbackHost(u.host))) risks.push({ severity: 'warn', label: 'plain http: traffic is not encrypted', short: 'plain http', weight: 1000 });
  if (u.update_available) risks.push({ severity: 'info', label: 'a newer preset exists', short: 'update', weight: 0 });
  return risks;
}

/** Upstreams with something to look at, worst first. */
export function upstreamAttention(upstreams: Upstream[]): UpstreamAttention[] {
  return upstreams
    // The upstream panel still identifies plaintext on local connections.
    .map((upstream) => ({ upstream, risks: upstreamRisks(upstream, false) }))
    .filter((a) => a.risks.length > 0)
    .map((a) => ({ ...a, top: worst(a.risks) ?? 'info' }))
    .sort((a, b) => order[a.top] - order[b.top] || weightOf(b) - weightOf(a) || a.upstream.name.localeCompare(b.upstream.name));
}
