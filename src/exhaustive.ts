import {
  AllocationError,
  REJECTED,
  buildResult,
  compareObjectives,
  objectiveOf,
  prepare,
} from "./allocator.ts";
import type { AllocationResult, Problem } from "./types.ts";

/** Safety cap on the number of complete assignments enumerated. */
export const EXHAUSTIVE_LEAF_CAP = 1 << 24;

/**
 * Brute-force verifier for small instances (<= 10 requests): enumerates every
 * feasible assignment and returns the best one under the exact same
 * lexicographic objective as `solve`. Used to cross-check the bounded search.
 */
export function solveExhaustive(problem: Problem): AllocationResult {
  const prepared = prepare(problem);
  const { requests, nodes } = prepared;

  const leaves = Math.pow(nodes.length + 1, requests.length);
  if (leaves > EXHAUSTIVE_LEAF_CAP) {
    throw new AllocationError(
      `instance too large for exhaustive verification: ${leaves} assignments ` +
        `would be enumerated (cap ${EXHAUSTIVE_LEAF_CAP})`,
    );
  }

  let bestAssignment = requests.map(() => REJECTED);
  let best = objectiveOf(prepared, bestAssignment);
  let visited = 0;

  const remaining = nodes.map((n) => n.capacity);
  const current = new Array<string>(requests.length).fill(REJECTED);

  const walk = (i: number): void => {
    visited += 1;
    if (i === requests.length) {
      const candidate = objectiveOf(prepared, current);
      if (compareObjectives(candidate, best) < 0) {
        best = candidate;
        bestAssignment = current.slice();
      }
      return;
    }
    const request = requests[i];
    for (let ni = 0; ni < nodes.length; ni++) {
      if (remaining[ni] < request.demand) continue;
      remaining[ni] -= request.demand;
      current[i] = nodes[ni].id;
      walk(i + 1);
      remaining[ni] += request.demand;
    }
    current[i] = REJECTED;
    walk(i + 1);
    current[i] = REJECTED;
  };
  walk(0);

  return buildResult(prepared, bestAssignment, {
    searchNodes: visited,
    budget: EXHAUSTIVE_LEAF_CAP,
    budgetExhausted: false,
  });
}
