import type {
  Allocation,
  ApportionProblem,
  IntInput,
  WeightInput,
} from './types.ts';
import type { Rational } from './rational.ts';
import { parseRational, rat } from './rational.ts';
import { largestRemainder, scaleWeights } from './apportion.ts';
import type { SearchGroup, SearchItem } from './search.ts';
import { DEFAULT_NODE_LIMIT, exactSearch, feasibleSumRange } from './search.ts';
import {
  makeConservationReport,
  makeLowerBoundCertificate,
  proveOptimalityByEnumeration,
  type ConservationReport,
  type EnumerationProof,
  type LowerBoundCertificate,
} from './certificate.ts';

/** Normalized, BigInt-only internal problem shape (ids sorted ascending). */
export interface NormProblem {
  readonly total: bigint;
  readonly ids: readonly string[];
  /** Scaled weight numerators N_i on a common denominator. */
  readonly numerators: readonly bigint[];
  /** S = sum(numerators). */
  readonly scale: bigint;
  readonly lo: readonly bigint[];
  readonly hi: readonly bigint[];
  readonly groups: readonly SearchGroup[];
}

export function toBigInt(v: IntInput, what: string): bigint {
  if (typeof v === 'bigint') return v;
  if (typeof v === 'string') {
    try {
      return BigInt(v.trim());
    } catch {
      throw new Error(`${what}: invalid integer string: ${v}`);
    }
  }
  throw new Error(`${what}: expected bigint or string, got ${typeof v}`);
}

export function toRational(w: WeightInput, what: string): Rational {
  if (typeof w === 'bigint') return rat(w);
  if (typeof w === 'string') return parseRational(w);
  if (w && typeof w === 'object' && typeof w.num === 'bigint' && typeof w.den === 'bigint') {
    return rat(w.num, w.den);
  }
  throw new Error(`${what}: unsupported weight`);
}

export function normalizeProblem(p: ApportionProblem): NormProblem {
  const total = toBigInt(p.total, 'total');
  if (total < 0n) throw new Error('total must be non-negative');
  if (p.items.length === 0) throw new Error('problem: no items');

  const weighted = p.items.map((it) => ({
    id: it.id,
    weight: toRational(it.weight, `weight of ${it.id}`),
    lower: it.lower === undefined ? 0n : toBigInt(it.lower, `lower of ${it.id}`),
    upper: it.upper === undefined ? total : toBigInt(it.upper, `upper of ${it.id}`),
  }));
  const scaled = scaleWeights(weighted); // sorts by id, rejects negatives/duplicates

  const byId = new Map(weighted.map((w) => [w.id, w]));
  const lo: bigint[] = [];
  const hi: bigint[] = [];
  for (const id of scaled.ids) {
    const w = byId.get(id)!;
    if (w.lower < 0n) throw new Error(`lower of ${id} must be non-negative`);
    lo.push(w.lower);
    hi.push(w.upper);
  }

  const indexOf = new Map(scaled.ids.map((id, i) => [id, i]));
  const groups: SearchGroup[] = (p.groups ?? []).map((g) => {
    const members = g.members.map((id) => {
      const idx = indexOf.get(id);
      if (idx === undefined) throw new Error(`group ${g.id}: unknown member ${id}`);
      return idx;
    });
    return {
      id: g.id,
      min: g.min === undefined ? 0n : toBigInt(g.min, `min of group ${g.id}`),
      max: g.max === undefined ? total : toBigInt(g.max, `max of group ${g.id}`),
      members,
    };
  });

  return {
    total,
    ids: scaled.ids,
    numerators: scaled.numerators,
    scale: scaled.scale,
    lo,
    hi,
    groups,
  };
}

export interface SolveOk {
  readonly status: 'ok';
  readonly allocation: Allocation;
  /** Exact scaled deviation: sum_i |x_i*S - T*N_i|. */
  readonly deviation: bigint;
  /** S; real deviation equals deviation / deviationScale. */
  readonly deviationScale: bigint;
  /** Largest-remainder baseline (no extra constraints applied). */
  readonly baseline: Allocation;
  readonly certificates: {
    readonly conservation: ConservationReport;
    readonly lowerBound: LowerBoundCertificate;
    /** Independent brute-force optimality proof, when the instance is tiny. */
    readonly enumeration: EnumerationProof | null;
  };
}

export interface SolveInfeasible {
  readonly status: 'infeasible';
  readonly reason: string;
  /** Units the total exceeds the maximum feasible sum (never forced). */
  readonly deficit?: bigint;
  /** Units the total falls below the minimum feasible sum. */
  readonly surplus?: bigint;
  readonly details: readonly string[];
}

export type SolveResult = SolveOk | SolveInfeasible;

function toAllocation(ids: readonly string[], values: readonly bigint[]): Allocation {
  const out: Allocation = {};
  for (let i = 0; i < ids.length; i++) out[ids[i]] = values[i];
  return out;
}

function infeasibilityDetails(norm: NormProblem): string[] {
  const details: string[] = [];
  let sumLo = 0n;
  let sumHi = 0n;
  for (let i = 0; i < norm.ids.length; i++) {
    if (norm.lo[i] > norm.hi[i]) {
      details.push(`item ${norm.ids[i]}: lower ${norm.lo[i]} exceeds upper ${norm.hi[i]}`);
    }
    sumLo += norm.lo[i];
    sumHi += norm.hi[i];
  }
  if (sumLo > norm.total) details.push(`sum of lowers ${sumLo} exceeds total ${norm.total}`);
  if (sumHi < norm.total) details.push(`sum of uppers ${sumHi} below total ${norm.total}`);
  for (const g of norm.groups) {
    let capHi = 0n;
    let capLo = 0n;
    for (const m of g.members) {
      capHi += norm.hi[m];
      capLo += norm.lo[m];
    }
    if (capHi < g.min) {
      details.push(`group ${g.id}: needs >= ${g.min} but member capacity is ${capHi} (shortfall ${g.min - capHi})`);
    }
    if (capLo > g.max) {
      details.push(`group ${g.id}: member lowers ${capLo} exceed group max ${g.max} (excess ${capLo - g.max})`);
    }
  }
  return details;
}

/**
 * Solves the constrained apportionment exactly:
 *   1. All integer constraints are hard (conservation, per-item bounds,
 *      every overlapping group min/max, enforced jointly).
 *   2. Among feasible allocations, total deviation from the ideal rational
 *      shares is minimized exactly (BigInt branch-and-bound).
 * Infeasible bounds/groups yield a deficit/surplus report; the total is
 * never force-fit.
 */
export function solveApportion(
  problem: ApportionProblem,
  opts: { nodeLimit?: number; enumerationLimit?: number } = {},
): SolveResult {
  const norm = normalizeProblem(problem);
  const nodeLimit = opts.nodeLimit ?? DEFAULT_NODE_LIMIT;

  const baselineValues = largestRemainder(norm.ids, norm.numerators, norm.scale, norm.total);

  const details = infeasibilityDetails(norm);
  const range = feasibleSumRange(
    norm.lo.map((lo, i) => ({ lo, hi: norm.hi[i] })),
    norm.groups,
    nodeLimit,
  );
  if (range === null) {
    return {
      status: 'infeasible',
      reason: 'constraints-unsatisfiable',
      details: details.length > 0 ? details : ['no feasible integer allocation satisfies all bounds and group constraints'],
    };
  }
  if (norm.total > range.max) {
    return {
      status: 'infeasible',
      reason: 'total-exceeds-capacity',
      deficit: norm.total - range.max,
      details,
    };
  }
  if (norm.total < range.min) {
    return {
      status: 'infeasible',
      reason: 'total-below-mandatory-minimum',
      surplus: range.min - norm.total,
      details,
    };
  }

  const items: SearchItem[] = norm.ids.map((id, i) => ({
    id,
    N: norm.numerators[i],
    lo: norm.lo[i],
    hi: norm.hi[i],
    prev: null,
    isPending: false,
  }));
  const outcome = exactSearch({
    total: norm.total,
    scale: norm.scale,
    items,
    groups: norm.groups,
    moveBudget: null,
    nodeLimit,
  });
  if (outcome === null) {
    // range said feasible, so this should not happen; stay honest anyway.
    return { status: 'infeasible', reason: 'constraints-unsatisfiable', details };
  }

  const allocation = toAllocation(norm.ids, outcome.values);
  const deviation = outcome.cost[1];
  const enumeration = proveOptimalityByEnumeration(
    norm,
    { mode: 'apportion' },
    outcome.cost,
    opts.enumerationLimit ?? 200_000,
  );

  return {
    status: 'ok',
    allocation,
    deviation,
    deviationScale: norm.scale,
    baseline: toAllocation(norm.ids, baselineValues),
    certificates: {
      conservation: makeConservationReport(norm, allocation),
      lowerBound: makeLowerBoundCertificate(norm, deviation),
      enumeration,
    },
  };
}
