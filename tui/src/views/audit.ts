import type { AuditEntry, Key, Upstream } from '../api.js';
import { keyLabel } from '../keys.js';
import { color } from '../ui/theme.js';

/** The colour of an audit action by what it does to a key or credential. */
export function actionColor(action: string): string | undefined {
  if (/(revoke|delete|remove)/.test(action)) return color.danger;
  if (/disable/.test(action)) return color.warn;
  if (/(create|enable|add)/.test(action)) return color.ok;
  return undefined;
}

export interface Names {
  keys: Key[];
  upstreams: Upstream[];
}

/** A target kind as words: price_override -> price override. */
export const kindName = (kind: string) => kind.replaceAll('_', ' ');

/** "key leaked-demo": the target's name where kman still knows it, else its id. */
export function targetText(e: AuditEntry, names: Names): string {
  return `${kindName(e.target_kind)} ${targetName(e, names)}`;
}

/** Just the target's name, or its id. */
export function targetName(e: AuditEntry, { keys, upstreams }: Names): string {
  let name: string | undefined;
  if (e.target_kind === 'key') {
    const k = keys.find((x) => x.id === e.target_id);
    name = k && keyLabel(k);
  } else if (e.target_kind === 'upstream') {
    name = upstreams.find((u) => u.id === e.target_id)?.name;
  } else if (e.target_kind === 'credential') {
    name = upstreams.flatMap((u) => u.credentials).find((c) => c.id === e.target_id)?.name;
  }
  return name ?? e.target_id;
}

/**
 * What a list row says the entry is about: the target's name. "key.disable" already
 * says it is a key, so only an action that does not name its kind gets the kind first.
 */
export function rowTarget(e: AuditEntry, names: Names): string {
  return e.action.startsWith(`${e.target_kind}.`) ? targetName(e, names) : targetText(e, names);
}
