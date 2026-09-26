/**
 * Minimum hitting set: given `sets` (one per failed branch, each a list of
 * candidate error ids), choose the fewest candidate ids that intersect every
 * set. Exact branch-and-bound within a node budget; on budget exhaustion
 * falls back to a greedy solution plus a proven lower bound, so callers get
 * either a proven-minimal cover or honest unproven-optimality bounds.
 */

export interface CoverResult {
  /** Indices into the candidate array. */
  selected: number[];
  /** true when `selected` is proven to have minimum size. */
  optimal: boolean;
  /** Proven lower bound on the optimum size. */
  lowerBound: number;
  /** Size of `selected` (upper bound on the optimum). */
  upperBound: number;
  nodes: number;
}

class BudgetExhausted extends Error {}

/** Greedy maximal packing of pairwise-disjoint sets => valid lower bound. */
function packingLowerBound(sets: number[][], uncovered: boolean[]): number {
  const order: number[] = [];
  for (let i = 0; i < sets.length; i++) if (uncovered[i]) order.push(i);
  order.sort((a, b) => sets[a].length - sets[b].length);
  const used = new Set<number>();
  let count = 0;
  for (const i of order) {
    if (sets[i].every((c) => !used.has(c))) {
      for (const c of sets[i]) used.add(c);
      count++;
    }
  }
  return count;
}

function greedyCover(sets: number[][], coverage: number[][]): number[] {
  const uncovered = new Set(sets.keys());
  const chosen: number[] = [];
  while (uncovered.size > 0) {
    let best = -1;
    let bestGain = 0;
    for (let c = 0; c < coverage.length; c++) {
      let gain = 0;
      for (const s of coverage[c]) if (uncovered.has(s)) gain++;
      if (gain > bestGain) {
        bestGain = gain;
        best = c;
      }
    }
    if (best < 0) break; // remaining sets have no candidates (infeasible)
    chosen.push(best);
    for (const s of coverage[best]) uncovered.delete(s);
  }
  return chosen;
}

export function minimumHittingSet(
  rawSets: number[][],
  candidateCount: number,
  maxNodes = 50_000,
): CoverResult {
  const sets = rawSets.map((s) => [...new Set(s)]);
  const coverage: number[][] = Array.from({ length: candidateCount }, () => []);
  sets.forEach((set, i) => {
    for (const c of set) coverage[c].push(i);
  });

  const greedy = greedyCover(sets, coverage);
  const uncoveredAll = sets.map(() => true);
  const lowerBound = packingLowerBound(sets, uncoveredAll);

  if (sets.length === 0) {
    return { selected: [], optimal: true, lowerBound: 0, upperBound: 0, nodes: 0 };
  }
  if (sets.some((s) => s.length === 0)) {
    throw new Error('infeasible hitting set: a failed branch produced no errors');
  }

  let nodes = 0;
  let best = greedy;
  const chosen = new Set<number>();
  const uncovered = sets.map(() => true);
  let uncoveredCount = sets.length;

  function recurse(): void {
    if (++nodes > maxNodes) throw new BudgetExhausted();
    if (uncoveredCount === 0) {
      if (chosen.size < best.length) best = [...chosen];
      return;
    }
    if (chosen.size + packingLowerBound(sets, uncovered) >= best.length) return;
    // Branch on the uncovered set with the fewest candidates.
    let target = -1;
    for (let i = 0; i < sets.length; i++) {
      if (!uncovered[i]) continue;
      if (target < 0 || sets[i].length < sets[target].length) target = i;
    }
    const candidates = [...sets[target]].sort((a, b) => coverage[b].length - coverage[a].length);
    for (const c of candidates) {
      if (chosen.has(c)) continue;
      const newly: number[] = [];
      for (const s of coverage[c]) {
        if (uncovered[s]) {
          uncovered[s] = false;
          newly.push(s);
        }
      }
      if (newly.length === 0) continue;
      chosen.add(c);
      uncoveredCount -= newly.length;
      recurse();
      uncoveredCount += newly.length;
      chosen.delete(c);
      for (const s of newly) uncovered[s] = true;
    }
  }

  try {
    recurse();
    return { selected: best, optimal: true, lowerBound: best.length, upperBound: best.length, nodes };
  } catch (e) {
    if (!(e instanceof BudgetExhausted)) throw e;
    return {
      selected: greedy,
      optimal: false,
      lowerBound,
      upperBound: greedy.length,
      nodes,
    };
  }
}
