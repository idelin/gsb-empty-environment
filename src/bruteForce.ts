/**
 * Exhaustive-search reference solver used to cross-check `solveSeam`
 * on small overlap regions. Enumerates all 2^(w*h) labellings that
 * respect the fixed-source constraints and finds the minimum cost.
 */

import { evaluateCost, solveSeam } from './seamSolver.ts';
import type { SeamProblem, SeamResult, SourceLabel } from './seamSolver.ts';

export interface BruteForceResult {
  /** Minimum attainable total cost. */
  minCost: number;
  /** Number of distinct labellings attaining the minimum (1 = unique optimum). */
  optimalCount: number;
  /** Number of feasible labellings (after fixed constraints). */
  feasibleCount: number;
}

export const BRUTE_FORCE_MAX_PIXELS = 24;

function labelsFromMask(width: number, height: number, mask: number): SourceLabel[][] {
  const labels: SourceLabel[][] = [];
  for (let y = 0; y < height; y++) {
    const row: SourceLabel[] = new Array<SourceLabel>(width);
    for (let x = 0; x < width; x++) {
      row[x] = (mask >>> (y * width + x)) & 1 ? 'B' : 'A';
    }
    labels.push(row);
  }
  return labels;
}

export function bruteForceMinCost(problem: SeamProblem): BruteForceResult {
  const { width, height } = problem;
  if (!Number.isInteger(width) || !Number.isInteger(height) || width < 0 || height < 0) {
    throw new TypeError('width and height must be non-negative integers');
  }
  const n = width * height;
  if (n === 0) {
    return { minCost: 0, optimalCount: 1, feasibleCount: 1 };
  }
  if (n > BRUTE_FORCE_MAX_PIXELS) {
    throw new Error(`brute force is limited to ${BRUTE_FORCE_MAX_PIXELS} pixels, got ${n}`);
  }

  const fixed = problem.fixed;
  let minCost = Infinity;
  let optimalCount = 0;
  let feasibleCount = 0;
  const total = 1 << n;

  for (let mask = 0; mask < total; mask++) {
    if (fixed) {
      let ok = true;
      for (let y = 0; y < height && ok; y++) {
        for (let x = 0; x < width && ok; x++) {
          const f = fixed[y]?.[x];
          if (f === 'A' || f === 'B') {
            const bit: SourceLabel = (mask >>> (y * width + x)) & 1 ? 'B' : 'A';
            if (bit !== f) ok = false;
          }
        }
      }
      if (!ok) continue;
    }
    feasibleCount++;
    const cost = evaluateCost(problem, labelsFromMask(width, height, mask)).total;
    if (cost < minCost - 1e-12) {
      minCost = cost;
      optimalCount = 1;
    } else if (Math.abs(cost - minCost) <= 1e-12) {
      optimalCount++;
    }
  }

  return { minCost, optimalCount, feasibleCount };
}

export interface OptimalityCheck {
  result: SeamResult;
  bruteForce: BruteForceResult;
  optimal: boolean;
}

/** Solve exactly and verify against exhaustive enumeration. */
export function verifyOptimality(problem: SeamProblem): OptimalityCheck {
  const result = solveSeam(problem);
  const bruteForce = bruteForceMinCost(problem);
  return {
    result,
    bruteForce,
    optimal: Math.abs(result.costs.total - bruteForce.minCost) <= 1e-9,
  };
}
