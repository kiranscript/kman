import type { Key } from './api.js';

/** What to call a key: its name, or its id when it has none. */
export function keyLabel(key: Pick<Key, 'id' | 'name'>): string {
  return key.name || key.id;
}

const statusOrder = { active: 0, disabled: 1, revoked: 2 } as const;

/** Keys that can still be used come first, then by name. Filtered by name, id, tag or upstream. */
export function visibleKeys(keys: Key[], filter: string): Key[] {
  const q = filter.trim().toLowerCase();
  const matches = (k: Key) =>
    !q ||
    [k.name, k.id, k.upstream, ...k.tags].some((s) => s?.toLowerCase().includes(q));
  return keys
    .filter(matches)
    .sort(
      (a, b) =>
        statusOrder[a.status] - statusOrder[b.status] ||
        keyLabel(a).localeCompare(keyLabel(b)),
    );
}
