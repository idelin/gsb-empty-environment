// Evidence construction: causal closure plus budget-bounded minimization.
//
// Minimality is defined as: fewest events first, then the lexicographically
// smallest sorted list of event ids. Subsets are enumerated in exactly that
// order and tested with the independent verifier, so the first verifying
// subset found is the minimum. If the verifier-call budget runs out, the
// current valid counterexample is returned with minimalProven = false.

import type { Evidence, TraceEvent, ViolationClaim } from './types.ts';
import { verifyEvidence } from './verifier.ts';

export const DEFAULT_BUDGET = 200_000;

/** All events reachable from the roots via causal references, in trace order. */
export function causalClosure(
  rootIds: Iterable<string>,
  byId: ReadonlyMap<string, TraceEvent>,
  indexOf: ReadonlyMap<string, number>,
): { events: TraceEvent[]; missing: string[] } {
  const included = new Set<string>();
  const missing = new Set<string>();
  const queue = [...rootIds];
  while (queue.length > 0) {
    const id = queue.shift() as string;
    if (included.has(id) || missing.has(id)) continue;
    const event = byId.get(id);
    if (!event) {
      missing.add(id);
      continue;
    }
    included.add(id);
    for (const cause of event.causes) queue.push(cause);
  }
  const events = [...included]
    .map((id) => byId.get(id) as TraceEvent)
    .sort((a, b) => (indexOf.get(a.id) as number) - (indexOf.get(b.id) as number));
  return { events, missing: [...missing].sort() };
}

/** Yields combinations of the given size in lexicographic order of the id lists. */
export function* combinations(ids: string[], size: number): Generator<string[]> {
  const n = ids.length;
  if (size < 0 || size > n) return;
  if (size === 0) {
    yield [];
    return;
  }
  const idx = Array.from({ length: size }, (_, i) => i);
  for (;;) {
    yield idx.map((i) => ids[i]);
    let i = size - 1;
    while (i >= 0 && idx[i] === n - size + i) i--;
    if (i < 0) return;
    idx[i]++;
    for (let j = i + 1; j < size; j++) idx[j] = idx[j - 1] + 1;
  }
}

/**
 * Shrinks a causal closure to the minimal subset that still proves the claim.
 * The closure itself must verify; the full closure is the fallback
 * counterexample when the budget is exhausted.
 */
export function minimizeEvidence(
  closureEvents: TraceEvent[],
  claim: ViolationClaim,
  budget: number = DEFAULT_BUDGET,
): Evidence {
  if (!verifyEvidence(closureEvents, claim)) {
    throw new Error(`internal error: causal closure does not verify claim ${claim.kind}`);
  }
  let verifierCalls = 1;
  const sortedIds = closureEvents.map((e) => e.id).sort();

  const fallback = (): Evidence => ({
    claim,
    eventIds: sortedIds,
    events: closureEvents,
    minimalProven: false,
    budgetExhausted: true,
    verifierCalls,
  });

  for (let size = 1; size < sortedIds.length; size++) {
    for (const combo of combinations(sortedIds, size)) {
      if (verifierCalls >= budget) return fallback();
      verifierCalls++;
      const keep = new Set(combo);
      const events = closureEvents.filter((e) => keep.has(e.id));
      if (verifyEvidence(events, claim)) {
        return {
          claim,
          eventIds: combo,
          events,
          minimalProven: true,
          budgetExhausted: false,
          verifierCalls,
        };
      }
    }
  }
  return {
    claim,
    eventIds: sortedIds,
    events: closureEvents,
    minimalProven: true,
    budgetExhausted: false,
    verifierCalls,
  };
}
