import type { Pattern } from './pattern.ts';
import type { Change, Notification } from './store.ts';

/**
 * Independent per-subscription reference matcher. Deliberately implemented
 * separately from the shared automaton (plain recursion over one pattern at a
 * time) so tests and `Store.verifyLastCommit()` can cross-check the automaton.
 *
 * Semantics: `pattern` matches `path` iff it matches a prefix of `path`.
 */
export function matchPatternRef(pattern: Pattern, path: readonly string[]): boolean {
  return matchPatternRefCounted(pattern, path).match;
}

export function matchPatternRefCounted(
  pattern: Pattern,
  path: readonly string[],
): { match: boolean; visited: number } {
  let visited = 0;
  const rec = (pi: number, si: number): boolean => {
    visited++;
    if (pi === pattern.length) return true; // pattern exhausted: prefix matched
    const p = pattern[pi];
    if (p === '**') {
      for (let k = si; k <= path.length; k++) if (rec(pi + 1, k)) return true;
      return false;
    }
    if (si === path.length) return false;
    if (p === '*' || p === path[si]) return rec(pi + 1, si + 1);
    return false;
  };
  return { match: rec(0, 0), visited };
}

/**
 * Recomputes expected notifications per subscription from scratch and compares
 * them with what the automaton-driven pipeline produced. Returns a list of
 * human-readable mismatches; an empty list means the implementations agree.
 */
export function verifyNotifications(
  subs: ReadonlyMap<number, Pattern>,
  changes: readonly Change[],
  notifications: readonly Notification[],
): string[] {
  const errors: string[] = [];
  const expected = new Map<number, { opIds: number[]; changeCount: number }>();
  for (const ch of changes) {
    for (const [id, pat] of subs) {
      const hit = matchPatternRef(pat, ch.path) || (ch.oldPath !== undefined && matchPatternRef(pat, ch.oldPath));
      if (!hit) continue;
      let e = expected.get(id);
      if (!e) {
        e = { opIds: [], changeCount: 0 };
        expected.set(id, e);
      }
      if (!e.opIds.includes(ch.opId)) e.opIds.push(ch.opId);
      e.changeCount++;
    }
  }
  const actual = new Map(notifications.map(n => [n.subscriptionId, n]));
  for (const [id, e] of expected) {
    const a = actual.get(id);
    if (!a) {
      errors.push(`sub ${id}: expected a notification but none was delivered`);
      continue;
    }
    const gotOps = a.ops.map(o => o.id);
    if (JSON.stringify(gotOps) !== JSON.stringify(e.opIds)) {
      errors.push(`sub ${id}: op ids/order mismatch, expected [${e.opIds}] got [${gotOps}]`);
    }
    if (a.changes.length !== e.changeCount) {
      errors.push(`sub ${id}: change count mismatch, expected ${e.changeCount} got ${a.changes.length}`);
    }
  }
  for (const n of notifications) {
    if (!expected.has(n.subscriptionId)) errors.push(`sub ${n.subscriptionId}: unexpected notification`);
  }
  return errors;
}
