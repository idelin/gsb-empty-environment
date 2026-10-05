/** Brute-force reference solver: enumerates all permutations. For tests. */

import type { ProblemView } from './verify';

export interface BruteResult {
  feasible: boolean;
  /** null when infeasible */
  minCost: bigint | null;
  /** number of distinct optimal assignments (0 when infeasible) */
  optimumCount: number;
  /** one optimal assignment, null when infeasible */
  assignment: number[] | null;
}

export function bruteForce(problem: ProblemView): BruteResult {
  const n = problem.size;
  let minCost: bigint | null = null;
  let optimumCount = 0;
  let best: number[] | null = null;

  const perm = new Array<number>(n).fill(-1);
  const used = new Array<boolean>(n).fill(false);

  const rec = (i: number, acc: bigint): void => {
    if (i === n) {
      if (minCost === null || acc < minCost) {
        minCost = acc;
        optimumCount = 1;
        best = perm.slice();
      } else if (acc === minCost) {
        optimumCount++;
      }
      return;
    }
    for (let j = 0; j < n; j++) {
      if (used[j] || !problem.isEnabled(i, j)) continue;
      used[j] = true;
      perm[i] = j;
      rec(i + 1, acc + (problem.cost(i, j) as bigint));
      used[j] = false;
      perm[i] = -1;
    }
  };
  rec(0, 0n);

  return {
    feasible: minCost !== null,
    minCost,
    optimumCount,
    assignment: best,
  };
}
