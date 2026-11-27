// What needs a look, worst first. kman is for seeing who uses a key and where,
// and for answering a leak, so an unresolved incident (a key turning up somewhere
// new, or a canary being used) comes before everything else, whatever its colour.
// Then what is red (expired, at a cap, an upstream with no credential), then the
// rest of the "key at risk" signals (GLOSSARY): open to every address, never
// expiring, unused, about to expire, and last the spend.
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
  /** How pressing it is among risks of one rank: higher is sooner. */
  weight: number;
  /** About an unresolved incident, so it ranks ahead of everything that is not. */
  incident?: boolean;
  /** The kman command that answers it, ready to run. */
  fix?: string;
}

/** What the admin API says about leaks: undefined when it was not asked or could not say. */
export interface Watch {
  /** Incidents not yet resolved. */
  incidents?: Incident[];
  /** Why they did not load: `incidents` is then old, or undefined. */
  incidentsError?: string;
  findings?: ExposureFinding[];
  findingsError?: string;
}

/** A part of the leak picture that did not load, so its silence means nothing. */
export interface Unknown {
  what: 'incidents' | 'exposure';
  /** An earlier answer is still shown. */
  stale: boolean;
  error: string;
  risk: Risk;
}

export function unknowns(watch: Watch): Unknown[] {
  const of = (what: Unknown['what'], error: string | undefined, stale: boolean): Unknown[] => {
    if (!error) return [];
    const risk: Risk = {
      severity: 'warn',
      label: `${what} ${stale ? 'not updated' : 'unknown'}: ${error}`,
      short: stale ? 'not updated' : 'unknown',
      // Behind every incident that is known, and ahead of the exposure findings.
      weight: what === 'incidents' ? 0 : 2100,
      incident: what === 'incidents',
      fix: what === 'incidents' ? 'kman incidents' : 'kman exposure',
    };
    return [{ what, stale, error, risk }];
  };
  return [...of('incidents', watch.incidentsError, watch.incidents !== undefined), ...of('exposure', watch.findingsError, watch.findings !== undefined)];
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
  // A severity this client does not know is taken as high.
  const rank = severityRank[i.severity] ?? 2;
  return {
    incident: true,
    severity: !acked && rank >= 2 ? 'danger' : 'warn',
    label: `incident #${i.id} (${i.severity}${acked ? ', acknowledged' : ''}${i.quiet ? ', quiet' : ''}): ${rules}`,
    short: canary ? 'canary used' : 'incident',
    listLabel: i.rules.length > 1 ? `${first} +${i.rules.length - 1}` : first,
    weight: (acked ? 2500 : 4000) + rank * 100,
    fix: `kman incident show ${i.id}`,
  };
}

/** An exposure finding as a risk. */
function findingRisk(f: ExposureFinding): Risk {
  switch (f.rule) {
    case 'open_to_every_address':
      return { severity: 'warn', label: 'open to every address: anyone who holds it can use it from anywhere', short: 'open to all', weight: 2000, fix: f.fix };
    case 'never_expires':
      return { severity: 'warn', label: 'never expires', short: 'no expiry', weight: 1500, fix: f.fix };
    case 'unused':
      return { severity: 'warn', label: f.last_used_at ? 'not used for 30 days' : 'never used', short: 'unused', weight: 1200, fix: f.fix };
    default: {
      const words = f.rule.replace(/_/g, ' ');
      return { severity: 'warn', label: words, short: 'exposed', weight: 1100, fix: f.fix };
    }
  }
}

/** A key kman holds no expiry for: the API sends the zero time, 0001-01-01. */
export const neverExpires = (iso: string) => !(Date.parse(iso) > 0);

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
  risks.push(...(watch.findings ?? []).filter((f) => f.key_id === key.id).map(findingRisk));

  if (key.expired) {
    risks.push({ severity: 'danger', label: 'expired', short: 'expired', weight: 2000 });
  } else if (!neverExpires(key.expires_at) && daysLeft(key.expires_at, now) < EXPIRING_DAYS) {
    const d = daysLeft(key.expires_at, now);
    const hours = Math.max(0, (Date.parse(key.expires_at) - now) / 3_600_000);
    risks.push({
      severity: 'warn',
      label: d < 1 ? 'expires within a day' : `expires in ${daysCeil(key.expires_at, now)} days`,
      short: 'expiring',
      // Fewer hours left, sooner; and any expiry ahead of any spend warning.
      weight: 1100 - hours / 2,
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

/** Where a risk stands before its weight: red incidents, other incidents, then by colour. */
const rank = (r: Risk) => (r.incident ? (r.severity === 'danger' ? 0 : 1) : 2 + order[r.severity]);

/** Orders risks, the most pressing first. */
export const compareRisk = (a: Risk, b: Risk) => rank(a) - rank(b) || b.weight - a.weight;

/** The one risk to name for a key. Its colour is the key's, so the mark matches the words. */
export function topRisk(risks: Risk[]): Risk | undefined {
  return [...risks].sort(compareRisk)[0];
}

/** Orders two things by their most pressing risks. Each has at least one. */
export const compareTop = (a: { risks: Risk[] }, b: { risks: Risk[] }) => compareRisk(topRisk(a.risks)!, topRisk(b.risks)!);

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
    .map((a) => ({ ...a, top: topRisk(a.risks)!.severity }))
    .sort((a, b) => compareTop(a, b) || (a.key.name ?? a.key.id).localeCompare(b.key.name ?? b.key.id));
}

/** Incidents about a key that is not in the list: a canary, which GET /keys leaves out. */
export function otherIncidents(keys: Key[], incidents: Incident[] = []): Incident[] {
  const ids = new Set(keys.map((k) => k.id));
  return incidents.filter((i) => i.state !== 'resolved' && !ids.has(i.key_id));
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
    .map((a) => ({ ...a, top: topRisk(a.risks)!.severity }))
    .sort((a, b) => compareTop(a, b) || a.upstream.name.localeCompare(b.upstream.name));
}
