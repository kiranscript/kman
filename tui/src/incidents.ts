// What an incident is about and what can be done about it, as plain data, so it is tested
// without drawing.
import type { Detection, Incident, IncidentDetail, Key, PickKind, SourceKind } from './api.js';
import { resolutionWord } from './commands.js';
import { ruleText } from './risk.js';

/** The unresolved incident about a key, which kman keeps to one a key. */
export const incidentFor = (incidents: Incident[] | undefined, keyId: string | undefined) =>
  keyId === undefined ? undefined : incidents?.find((i) => i.key_id === keyId && i.state !== 'resolved');

export const isCanary = (i: Incident) => i.rules.includes('canary');

export type Action = 'acknowledge' | 'thisWasMe' | 'resolve' | 'disable' | 'enable' | 'rotate' | 'revoke' | 'reset';

/** The key that asks for each action. */
export const actionKeys: Record<Action, string> = {
  acknowledge: 'a',
  thisWasMe: 'm',
  resolve: 'R',
  disable: 'd',
  enable: 'e',
  rotate: 'o',
  revoke: 'x',
  reset: 'z',
};

export const actionFor = (input: string) => (Object.keys(actionKeys) as Action[]).find((a) => actionKeys[a] === input);

/**
 * What can be done from an incident, in the order shown. A canary is answered by
 * acknowledging, resolving and arming it again: resolving does not arm it, so that stays
 * after. `key` is the incident's key as listed, which a canary is not.
 */
export function actions(i: Incident, key: Key | undefined): Action[] {
  const open = i.state !== 'resolved';
  const ack: Action[] = i.state === 'open' ? ['acknowledge'] : [];
  if (isCanary(i)) return open ? [...ack, 'resolve', 'reset'] : ['reset'];
  if (!open) return [];
  const contain: Action[] = key && key.status !== 'revoked' ? [key.status === 'disabled' ? 'enable' : 'disable', 'rotate', 'revoke'] : [];
  return [...ack, 'thisWasMe', 'resolve', ...contain];
}

/** "resolved by alice as false positive", or that it expired, which no one did. */
export function resolvedText(i: Incident): string {
  if (i.resolution === 'expired') return 'expired: nobody acknowledged it';
  return `resolved${i.resolved_by ? ` by ${i.resolved_by}` : ''}${i.resolution ? ` as ${resolutionWord(i.resolution)}` : ''}`;
}

const asText = (asn: number | undefined, org: string | undefined) => (asn ? `AS${asn}${org ? ` ${org}` : ''}` : '');

export interface Fired {
  rule: string;
  /** "new country". */
  what: string;
  /** What was seen. */
  observed: string;
  /** What the key normally does, if the rule compares. */
  usual?: string;
  /** Set when a request behind it was refused, with why when kman said. */
  refused?: string;
  /** And one behind it was not. */
  forwarded: boolean;
  /** How many of the listed detections say this. */
  times: number;
}

const refusedPattern = /^the request was refused(?: \((.*)\))?$/;

function observedBy(d: Detection): { observed: string; usual?: string } {
  const e = d.evidence;
  const seen = (value: string | undefined) => value || e.observed || '—';
  switch (d.rule) {
    case 'new_country':
      return { observed: seen(e.country), usual: e.baseline };
    case 'new_asn':
      return { observed: seen(asText(e.asn, e.as_org)), usual: e.baseline };
    case 'new_client':
      return { observed: seen(e.client), usual: e.baseline };
    case 'new_network':
      return { observed: e.network || (e.address_withheld ? 'network withheld' : '—'), usual: e.usual || e.baseline };
    default:
      // A canary says where it was planted; another rule says what it compared.
      return { observed: e.observed || (refusedPattern.test(e.detail ?? '') ? '' : (e.detail ?? '')), usual: e.baseline };
  }
}

/** What fired, each thing once, in the order it first did. */
export function whatFired(detections: Detection[]): Fired[] {
  const seen = new Map<string, Fired>();
  for (const d of detections) {
    const { observed, usual } = observedBy(d);
    const why = refusedPattern.exec(d.evidence.detail ?? '');
    const id = `${d.rule}\u0000${observed}`;
    const was = seen.get(id);
    const refused = why ? (why[1] ?? '') : was?.refused;
    const forwarded = !why || Boolean(was?.forwarded);
    // The newest baseline is the one to compare with.
    seen.set(id, { rule: d.rule, what: ruleText(d.rule), observed, usual: usual ?? was?.usual, refused, forwarded, times: (was?.times ?? 0) + 1 });
  }
  return [...seen.values()];
}

/** "usually US", or kman's own sentence about what is usual, which says so already. */
export const usualText = (usual: string) => (/\busual\b/.test(usual) ? usual : `usually ${usual}`);

/** Why every request behind what fired was refused, when they all were, for one reason. */
export function allRefused(fired: Fired[]): string | undefined {
  const first = fired[0]?.refused;
  return fired.length > 1 && fired.every((f) => f.refused === first && !f.forwarded) ? first : undefined;
}

/** Where the requests came from, each origin once. */
export function whereFrom(detections: Detection[]): string[] {
  const origins = detections.map(({ evidence: e }) =>
    [e.address || e.network, e.country, asText(e.asn, e.as_org), e.client].filter(Boolean).join(' · '),
  );
  return [...new Set(origins.filter(Boolean))];
}

const actionWords: Record<string, string> = {
  'incident.open': 'opened',
  'incident.escalate': 'escalated',
  'incident.acknowledge': 'acknowledged',
  'incident.resolve': 'resolved',
  'incident.this_was_me': 'marked this was me, a false positive',
  'incident.expire': 'expired: nobody acknowledged it',
  'known_source.create': 'known source added',
  'key.disable': 'key disabled',
  'key.rotate': 'key rotated',
  'key.revoke': 'key revoked',
};

/** What was done, oldest first: "acknowledged by owner". */
export const timeline = (d: IncidentDetail) =>
  d.actions.map((a) => ({ id: a.id, time: a.time, text: `${actionWords[a.action] ?? `${a.action} ${a.target_id}`} by ${a.actor}` }));

export interface Pick {
  kind: PickKind;
  /** The values ticking it adds; none when it cannot be ticked. */
  values: string[];
  /** There is a network, which cannot be added while addresses are off. */
  withheld: boolean;
}

const pickKinds: { kind: PickKind; source: SourceKind }[] = [
  { kind: 'country', source: 'country' },
  { kind: 'asn', source: 'asn' },
  { kind: 'client', source: 'client' },
  { kind: 'network', source: 'cidr' },
];

/** The boxes of "this was me": the API picks places by kind, so one box for each kind seen. */
export function picks(d: IncidentDetail): Pick[] {
  return pickKinds.flatMap(({ kind, source }) => {
    const mine = d.known_source_candidates.filter((c) => c.kind === source);
    if (mine.length === 0) return [];
    const values = mine
      .filter((c) => !c.withheld && c.value)
      .map((c) => (kind === 'asn' ? `AS${c.value}${c.label ? ` ${c.label}` : ''}` : c.value!));
    return [{ kind, values, withheld: values.length === 0 }];
  });
}
