/**
 * Independent certificate verification. These functions recompute everything
 * from the problem definition and the returned certificate only, so an
 * incorrect solver cannot make them pass.
 */

import type { InfeasibleResult, OptimalResult } from './assignment';

/** Read-only view of an assignment instance. */
export interface ProblemView {
  size: number;
  isEnabled(i: number, j: number): boolean;
  cost(i: number, j: number): bigint | null;
}

export function viewOf(problem: {
  size: number;
  isEnabled(i: number, j: number): boolean;
  cost(i: number, j: number): bigint | null;
}): ProblemView {
  return problem;
}

/**
 * Verify a primal + dual optimality certificate:
 *  1. assignment is a permutation using only enabled edges;
 *  2. its recomputed cost equals `result.cost`;
 *  3. dual feasibility: u[i] + v[j] <= c(i, j) on every enabled edge;
 *  4. strong duality: sum(u) + sum(v) === primal cost.
 * Conditions 3+4 prove optimality by LP duality.
 */
export function verifyOptimal(problem: ProblemView, result: OptimalResult): boolean {
  const n = problem.size;
  if (result.status !== 'optimal') return false;
  const { assignment, cost, dual } = result;
  if (assignment.length !== n) return false;
  if (dual.rowPotential.length !== n || dual.colPotential.length !== n) return false;

  const used = new Array<boolean>(n).fill(false);
  let total = 0n;
  for (let i = 0; i < n; i++) {
    const j = assignment[i];
    if (!Number.isInteger(j) || j < 0 || j >= n || used[j]) return false;
    used[j] = true;
    if (!problem.isEnabled(i, j)) return false;
    total += problem.cost(i, j) as bigint;
  }
  if (total !== cost) return false;

  for (let i = 0; i < n; i++) {
    for (let j = 0; j < n; j++) {
      if (!problem.isEnabled(i, j)) continue;
      const c = problem.cost(i, j) as bigint;
      if (dual.rowPotential[i] + dual.colPotential[j] > c) return false;
    }
  }

  let dualSum = 0n;
  for (let i = 0; i < n; i++) dualSum += dual.rowPotential[i] + dual.colPotential[i];
  return dualSum === total;
}

/**
 * Verify a Hall deficiency certificate: rows S and columns N(S) with
 * |S| > |N(S)| such that every enabled edge leaving S ends in N(S).
 * By Hall's theorem no perfect matching can exist.
 */
export function verifyInfeasible(
  problem: ProblemView,
  result: InfeasibleResult,
): boolean {
  const n = problem.size;
  if (result.status !== 'infeasible') return false;
  const { rows, cols } = result.hall;
  if (rows.length <= cols.length) return false;

  const rowSet = new Set(rows);
  const colSet = new Set(cols);
  if (rowSet.size !== rows.length || colSet.size !== cols.length) return false;
  for (const i of rows) if (!Number.isInteger(i) || i < 0 || i >= n) return false;
  for (const j of cols) if (!Number.isInteger(j) || j < 0 || j >= n) return false;

  for (const i of rows) {
    for (let j = 0; j < n; j++) {
      if (problem.isEnabled(i, j) && !colSet.has(j)) return false;
    }
  }
  return true;
}
