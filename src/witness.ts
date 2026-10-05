import { MASK_A, MASK_B, MASK_N, MASK_ALL } from "./formula.ts";
import type { Formula } from "./formula.ts";
import { existsDisjointPair } from "./search.ts";
import type { SearchStats } from "./search.ts";

export interface AggregateStats extends SearchStats {
  oracleCalls: number;
}

export interface Witness {
  a: number[];
  b: number[];
  stats: AggregateStats;
}

export function lexMinWitness(fa: Formula, fb: Formula, n: number): Witness | null {
  const stats: AggregateStats = {
    oracleCalls: 0,
    nodesVisited: 0,
    pruned: 0,
    memoHits: 0,
    memoStates: 0,
    maxDepth: 0,
    elapsedMs: 0,
  };
  const t0 = performance.now();

  const feasible = (masks: number[]): boolean => {
    stats.oracleCalls++;
    const r = existsDisjointPair(fa, fb, n, masks);
    stats.nodesVisited += r.stats.nodesVisited;
    stats.pruned += r.stats.pruned;
    stats.memoHits += r.stats.memoHits;
    stats.memoStates += r.stats.memoStates;
    if (r.stats.maxDepth > stats.maxDepth) stats.maxDepth = r.stats.maxDepth;
    return r.found;
  };

  const buildSide = (
    masks: number[],
    inBit: number,
    outBits: number,
  ): number[] | null => {
    const chosen: number[] = [];
    const inSet = new Set<number>();
    let last = -1;
    for (;;) {
      const term = masks.slice();
      for (let v = 0; v < n; v++) if (!inSet.has(v)) term[v] &= outBits;
      if (feasible(term)) {
        for (let v = 0; v < n; v++) masks[v] = term[v];
        return chosen;
      }
      let pick = -1;
      for (let v = last + 1; v < n; v++) {
        if ((masks[v] & inBit) === 0) continue;
        const trial = masks.slice();
        trial[v] = inBit;
        if (feasible(trial)) {
          pick = v;
          break;
        }
        masks[v] &= outBits;
      }
      if (pick === -1) return null;
      masks[pick] = inBit;
      inSet.add(pick);
      chosen.push(pick);
      last = pick;
    }
  };

  const masks = new Array<number>(n).fill(MASK_ALL);
  const a = buildSide(masks, MASK_A, MASK_B | MASK_N);
  if (a === null) {
    stats.elapsedMs = performance.now() - t0;
    return null;
  }
  const b = buildSide(masks, MASK_B, MASK_A | MASK_N);
  stats.elapsedMs = performance.now() - t0;
  if (b === null) return null;
  return { a, b, stats };
}
