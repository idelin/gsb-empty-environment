import { alertIdOf, type EventRevision, type EventType } from './types.ts';

interface ResolvedEvent {
  id: string;
  revision: number;
  type: EventType;
  timestamp: number;
  retracted: boolean;
}

/**
 * Resolve a revision log to the final state per event id: highest revision
 * wins; equal revisions must carry identical content (otherwise the log is
 * conflicting and an error is thrown).
 */
export function resolveLatest(revisions: Iterable<EventRevision>): ResolvedEvent[] {
  const byId = new Map<string, ResolvedEvent>();
  for (const rev of revisions) {
    const retracted = rev.retracted === true;
    const existing = byId.get(rev.id);
    if (existing !== undefined) {
      if (rev.revision < existing.revision) continue;
      if (rev.revision === existing.revision) {
        const same =
          existing.type === rev.type &&
          existing.timestamp === rev.timestamp &&
          existing.retracted === retracted;
        if (!same) {
          throw new Error(`conflicting content for ${rev.id}@rev${rev.revision}`);
        }
        continue;
      }
    }
    byId.set(rev.id, {
      id: rev.id,
      revision: rev.revision,
      type: rev.type,
      timestamp: rev.timestamp,
      retracted,
    });
  }
  return [...byId.values()];
}

/**
 * One-shot reference computation: resolve revisions, sort the surviving
 * events by (timestamp, id), and emit every (A, B) pair with
 * tB - tA <= windowMs (inclusive) and no C in [tA, tB] (inclusive).
 * Returns a map alertId -> {aId, bId}.
 */
export function computeReferenceAlerts(
  revisions: Iterable<EventRevision>,
  windowMs: number,
): Map<string, { aId: string; bId: string }> {
  const live = resolveLatest(revisions)
    .filter((e) => !e.retracted)
    .sort((a, b) =>
      a.timestamp !== b.timestamp ? a.timestamp - b.timestamp : a.id < b.id ? -1 : a.id > b.id ? 1 : 0,
    );
  const cPrefix = new Array<number>(live.length + 1).fill(0);
  for (let i = 0; i < live.length; i += 1) {
    cPrefix[i + 1] = cPrefix[i] + (live[i].type === 'C' ? 1 : 0);
  }
  const lowerBoundTs = (ts: number): number => {
    let lo = 0;
    let hi = live.length;
    while (lo < hi) {
      const mid = (lo + hi) >>> 1;
      if (live[mid].timestamp < ts) lo = mid + 1;
      else hi = mid;
    }
    return lo;
  };
  const upperBoundTs = (ts: number): number => {
    let lo = 0;
    let hi = live.length;
    while (lo < hi) {
      const mid = (lo + hi) >>> 1;
      if (live[mid].timestamp <= ts) lo = mid + 1;
      else hi = mid;
    }
    return lo;
  };
  const out = new Map<string, { aId: string; bId: string }>();
  for (let i = 0; i < live.length; i += 1) {
    const a = live[i];
    if (a.type !== 'A') continue;
    const lo = lowerBoundTs(a.timestamp); // first index with ts >= tA
    for (let j = lo; j < live.length; j += 1) {
      const b = live[j];
      if (b.timestamp > a.timestamp + windowMs) break;
      if (b.type !== 'B') continue;
      // C count inside [tA, tB], inclusive on both ends.
      const hi = upperBoundTs(b.timestamp); // one past last ts <= tB
      if (cPrefix[hi] - cPrefix[lo] !== 0) continue;
      out.set(alertIdOf(a.id, b.id), { aId: a.id, bId: b.id });
    }
  }
  return out;
}
