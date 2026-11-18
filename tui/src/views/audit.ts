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

/** "key leaked-demo": the target's name where kman still knows it, else its id. */
export function targetText(e: AuditEntry, { keys, upstreams }: Names): string {
  let name: string | undefined;
  if (e.target_kind === 'key') {
    const k = keys.find((x) => x.id === e.target_id);
    name = k && keyLabel(k);
  } else if (e.target_kind === 'upstream') {
    name = upstreams.find((u) => u.id === e.target_id)?.name;
  } else if (e.target_kind === 'credential') {
    name = upstreams.flatMap((u) => u.credentials).find((c) => c.id === e.target_id)?.name;
  }
  return `${e.target_kind.replace('_', ' ')} ${name ?? e.target_id}`;
}
