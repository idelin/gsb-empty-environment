import { patternMatchesPath } from './pattern.ts';
import type { Pattern } from './pattern.ts';
import { matchPathsForPatch } from './store.ts';
import type { Patch } from './store.ts';

export interface ReferenceResult {
  /** subId -> matched patches, in patch (operation) order. */
  bySub: Map<number, Patch[]>;
  /** Total segment comparisons performed (naive per-subscription cost). */
  comparisons: number;
}

/**
 * Independent reference dispatcher: for every patch, check every subscription
 * with the plain prefix matcher — no automaton, no sharing. Tests cross-check
 * the automaton-based dispatch against this naive baseline.
 */
export function referenceDispatch(
  subs: ReadonlyMap<number, Pattern>,
  patches: readonly Patch[],
): ReferenceResult {
  const bySub = new Map<number, Patch[]>();
  let comparisons = 0;
  for (const patch of patches) {
    const paths = matchPathsForPatch(patch);
    for (const [id, pattern] of subs) {
      let hit = false;
      for (const path of paths) {
        comparisons += Math.min(pattern.length, path.length) + 1;
        if (patternMatchesPath(pattern, path)) {
          hit = true;
          break;
        }
      }
      if (hit) {
        let list = bySub.get(id);
        if (!list) {
          list = [];
          bySub.set(id, list);
        }
        list.push(patch);
      }
    }
  }
  return { bySub, comparisons };
}
