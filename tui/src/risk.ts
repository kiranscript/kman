// What needs a look, worst first. kman is for seeing who uses a key and where,
// and for answering a leak, so an open incident (a key turning up somewhere new,
// or a canary being used) comes first. Then the "key at risk" signals (GLOSSARY):
// open to every address, unused, about to expire, and last the spend: at or
// close to a cap, unmetered requests, no cap.
import { BlockList, isIP } from 'node:net';
import type { ExposureFinding, Incident, Key, SpendPeriod, Upstream } from './api.js';
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
  /** The kman command that answers it, ready to run. */
  fix?: string;
}

/** What the admin API says about leaks: undefined when it was not asked or could not say. */
export interface Watch {
  /** Incidents not yet resolved. */
  incidents?: Incident[];
  findings?: ExposureFinding[];
}

const ruleWords: Record<string, string> = {
  new_country: 'new country',
  new_asn: 'new ASN',
  new_network: 'new network',
  new_client: 'new client',
  canary: 'canary used',
};

/** "new country", or the rule's own name for one this client does not know. */
export const ruleText = (rule: string) => ruleWords[rule] ?? rule.replace(/_/g, ' ');

const severityRank: Record<Incident['severity'], number> = { low: 0, medium: 1, high: 2, critical: 3 };

/** An incident as a risk: high and critical are red until someone acknowledges it. */
export function incidentRisk(i: Incident): Risk {
  const acked = i.state === 'acknowledged';
  const rules = i.rules.length ? i.rules.map(ruleText).join(', ') : 'a detection';
  const canary = i.rules.includes('canary');
  const first = i.rules[0] ? ruleText(i.rules[0]) : 'incident';
  return {
    severity: !acked && severityRank[i.severity] >= 2 ? 'danger' : 'warn',
    label: `incident #${i.id} (${i.severity}${acked ? ', acknowledged' : ''}${i.quiet ? ', quiet' : ''}): ${rules}`,
    short: canary ? 'canary used' : 'incident',
    listLabel: i.rules.length > 1 ? `${first} +${i.rules.length - 1}` : first,
    weight: (acked ? 2500 : 4000) + severityRank[i.severity] * 100,
    fix: `kman incident show ${i.id}`,
  };
}

/** An exposure finding as a risk, or undefined for a rule this client does not know. */
function findingRisk(f: ExposureFinding): Risk | undefined {
  switch (f.rule) {
    case 'open_to_every_address':
      return { severity: 'warn', label: 'open to every address: anyone who holds it can use it from anywhere', short: 'open to all', weight: 2000, fix: f.fix };
    case 'never_expires':
      return { severity: 'warn', label: 'never expires', short: 'no expiry', weight: 1500, fix: f.fix };
    case 'unused':
      return { severity: 'warn', label: f.last_used_at ? 'not used for 30 days' : 'never used', short: 'unused', weight: 1200, fix: f.fix };
    default:
      return undefined;
  }
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

/**
 * A key's risks. An incident not yet resolved is one whatever the key's state, since it is
 * a case still to answer. The rest are only for a key that can still be used: a disabled or
 * revoked key is contained.
 */
export function keyRisks(key: Key, now: number, watch: Watch = {}): Risk[] {
  const risks: Risk[] = (watch.incidents ?? []).filter((i) => i.key_id === key.id && i.state !== 'resolved').map(incidentRisk);
  if (key.status !== 'active') return risks;
  for (const f of watch.findings ?? []) {
    const r = f.key_id === key.id ? findingRisk(f) : undefined;
    if (r) risks.push(r);
  }

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

/** Keys with something to look at, worst first. */
export function attention(keys: Key[], now: number, watch: Watch = {}): Attention[] {
  return keys
    .map((key) => ({ key, risks: keyRisks(key, now, watch) }))
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
/** Incidents about a key that is not in the list: a canary, which GET /keys leaves out. */
export function otherIncidents(keys: Key[], incidents: Incident[] = []): Incident[] {
  const ids = new Set(keys.map((k) => k.id));
  return incidents.filter((i) => i.state !== 'resolved' && !ids.has(i.key_id));
}

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
