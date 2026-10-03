import type { Allocation } from './types.ts';
import type { NormProblem } from './solver.ts';
import { largestRemainder, scaledDeviation } from './apportion.ts';
import { cmpCost, type Cost } from './search.ts';

/**
 * Independent certificates. None of these functions share code with the
 * solver's search loop: conservation is re-checked from the raw problem,
 * the lower bound comes from a closed-form relaxation, and the enumeration
 * proof is a separate brute-force recursion with no pruning.
 */

export interface ConservationReport {
  readonly ok: boolean;
  readonly violations: readonly string[];
}

/**
 * Independently verifies conservation and every hard constraint:
 * sum(allocation) + pending === total, per-item bounds, and all
 * (overlapping) group min/max sums.
 */
export function verifyConservation(
  norm: NormProblem,
  allocation: Allocation,
  pending: bigint = 0n,
): ConservationReport {
  const violations: string[] = [];
  let sum = 0n;
  for (let i = 0; i < norm.ids.length; i++) {
    const id = norm.ids[i];
    const v = allocation[id];
    if (typeof v !== 'bigint') {
      violations.push(`item ${id}: missing or non-bigint allocation`);
      continue;
    }
    if (v < norm.lo[i]) violations.push(`item ${id}: ${v} below lower bound ${norm.lo[i]}`);
    if (v > norm.hi[i]) violations.push(`item ${id}: ${v} above upper bound ${norm.hi[i]}`);
    sum += v;
  }
  for (const id of Object.keys(allocation)) {
    if (!norm.ids.includes(id)) violations.push(`unknown item in allocation: ${id}`);
  }
  if (sum + pending !== norm.total) {
    violations.push(`conservation: sum ${sum} + pending ${pending} != total ${norm.total}`);
  }
  for (const g of norm.groups) {
    let gsum = 0n;
    for (const m of g.members) gsum += allocation[norm.ids[m]] ?? 0n;
    if (gsum < g.min) violations.push(`group ${g.id}: sum ${gsum} below min ${g.min}`);
    if (gsum > g.max) violations.push(`group ${g.id}: sum ${gsum} above max ${g.max}`);
  }
  return { ok: violations.length === 0, violations };
}

export const makeConservationReport = verifyConservation;

export interface LowerBoundCertificate {
  readonly relaxation: 'drop-bounds-and-groups-keep-conservation';
  /** Achieved scaled deviation of the returned allocation. */
  readonly objective: bigint;
  /**
   * Exact optimum of the relaxation that keeps only conservation and
   * integrality. That relaxation is solved in closed form by the largest
   * remainder method, so lowerBound <= objective for every feasible
   * allocation; equality certifies optimality.
   */
  readonly lowerBound: bigint;
  readonly scale: bigint;
  readonly optimal: boolean;
}

export function makeLowerBoundCertificate(
  norm: NormProblem,
  objective: bigint,
): LowerBoundCertificate {
  const lrm = largestRemainder(norm.ids, norm.numerators, norm.scale, norm.total);
  const lowerBound = scaledDeviation(lrm, norm.numerators, norm.scale, norm.total);
  return {
    relaxation: 'drop-bounds-and-groups-keep-conservation',
    objective,
    lowerBound,
    scale: norm.scale,
    optimal: objective === lowerBound,
  };
}

/** Re-derives the lower bound independently and checks the certificate. */
export function verifyLowerBoundCertificate(
  norm: NormProblem,
  cert: LowerBoundCertificate,
): { ok: boolean; violations: string[] } {
  const violations: string[] = [];
  const lrm = largestRemainder(norm.ids, norm.numerators, norm.scale, norm.total);
  const recomputed = scaledDeviation(lrm, norm.numerators, norm.scale, norm.total);
  if (cert.lowerBound !== recomputed) {
    violations.push(`lowerBound ${cert.lowerBound} != recomputed ${recomputed}`);
  }
  if (cert.objective < cert.lowerBound) {
    violations.push(`objective ${cert.objective} below lower bound ${cert.lowerBound}`);
  }
  if (cert.optimal !== (cert.objective === cert.lowerBound)) {
    violations.push('optimal flag inconsistent with objective/lowerBound');
  }
  return { ok: violations.length === 0, violations };
}

export type EnumerationMode =
  | { readonly mode: 'apportion' }
  | {
      readonly mode: 'repair';
      /** Previous allocation aligned with norm.ids. */
      readonly prev: readonly bigint[];
      readonly moveBudget: bigint | null;
    };

export interface EnumerationProof {
  /** True only when the whole space was enumerated and best === claimed. */
  readonly proven: boolean;
  /** False when the space exceeded the limit and nothing is claimed. */
  readonly exhausted: boolean;
  readonly feasibleChecked: number;
  readonly bestCost: Cost | null;
  readonly claimedCost: Cost;
  readonly limit: number;
}

/**
 * Independent brute-force optimality proof for small instances: enumerates
 * every integer tuple inside the per-item bounds, checks conservation,
 * groups, pending and move budget from scratch, and compares the best cost
 * tuple with the solver's claim. Shares no search code with the solver.
 */
export function proveOptimalityByEnumeration(
  norm: NormProblem,
  mode: EnumerationMode,
  claimedCost: Cost,
  limit: number,
): EnumerationProof {
  const n = norm.ids.length;
  // Size guard in BigInt: product of per-item ranges.
  let space = 1n;
  const bigLimit = BigInt(limit);
  for (let i = 0; i < n; i++) {
    space *= norm.hi[i] - norm.lo[i] + 1n;
    if (space > bigLimit) {
      return { proven: false, exhausted: false, feasibleChecked: 0, bestCost: null, claimedCost, limit };
    }
  }

  const abs = (v: bigint): bigint => (v < 0n ? -v : v);
  let best: Cost | null = null;
  let feasibleChecked = 0;
  const values = new Array<bigint>(n).fill(0n);

  function leaf(): void {
    let sum = 0n;
    for (let i = 0; i < n; i++) sum += values[i];
    let pending = 0n;
    if (mode.mode === 'apportion') {
      if (sum !== norm.total) return;
    } else {
      pending = norm.total - sum;
      if (pending < 0n) return;
    }
    for (const g of norm.groups) {
      let gsum = 0n;
      for (const m of g.members) gsum += values[m];
      if (gsum < g.min || gsum > g.max) return;
    }
    let moves = 0n;
    if (mode.mode === 'repair') {
      for (let i = 0; i < n; i++) moves += abs(values[i] - mode.prev[i]);
      if (mode.moveBudget !== null && moves > mode.moveBudget) return;
    }
    const dev = scaledDeviation(values, norm.numerators, norm.scale, norm.total);
    const cost: Cost = [pending, dev, moves];
    feasibleChecked++;
    if (best === null || cmpCost(cost, best) < 0) best = cost;
  }

  function walk(i: number): void {
    if (i === n) {
      leaf();
      return;
    }
    for (let v = norm.lo[i]; v <= norm.hi[i]; v++) {
      values[i] = v;
      walk(i + 1);
    }
  }
  walk(0);

  return {
    proven: best !== null && cmpCost(best, claimedCost) === 0,
    exhausted: true,
    feasibleChecked,
    bestCost: best,
    claimedCost,
    limit,
  };
}
