import type { Allocation, ApportionProblem, IntInput } from './types.ts';
import type { SearchItem } from './search.ts';
import { DEFAULT_NODE_LIMIT, exactSearch } from './search.ts';
import {
  makeConservationReport,
  makeLowerBoundCertificate,
  proveOptimalityByEnumeration,
  type ConservationReport,
  type EnumerationProof,
  type LowerBoundCertificate,
} from './certificate.ts';
import { normalizeProblem, toBigInt, type NormProblem } from './solver.ts';

/**
 * Weight-correction repair.
 *
 * Given a previous allocation and corrected weights, re-solve globally
 * (never group-by-group, so overlapping group constraints cannot be broken
 * by sequential passes). Hard constraints: conservation including a pending
 * sink, all bounds and groups, and the optional move budget.
 *
 * Lexicographic objective:
 *   1. pending   - unallocated units are minimized first; when the move
 *                  budget cannot even restore conservation, the shortfall
 *                  stays pending ("保留未决") instead of violating anything.
 *   2. deviation - total distance to the new ideal rational shares
 *                  ("优化" toward the corrected weights).
 *   3. moves     - sum |x_i - previous_i|, so among equally optimal
 *                  allocations the one requiring the fewest moves wins
 *                  ("最少移动" as the final tie-break, no needless churn).
 */

export interface RepairOptions {
  /** Maximum allowed sum of |x_i - previous_i|. Omit for unlimited. */
  readonly moveBudget?: IntInput;
  readonly nodeLimit?: number;
  readonly enumerationLimit?: number;
}

export interface RepairResult {
  readonly status: 'ok' | 'pending';
  readonly allocation: Allocation;
  /** Units left unallocated because the move budget was insufficient. */
  readonly pending: bigint;
  /** Total moves used: sum |x_i - previous_i|. */
  readonly moves: bigint;
  readonly moveBudget: bigint | null;
  readonly deviation: bigint;
  readonly deviationScale: bigint;
  readonly certificates: {
    readonly conservation: ConservationReport;
    readonly lowerBound: LowerBoundCertificate;
    readonly enumeration: EnumerationProof | null;
  };
}

export function repairAllocation(
  problem: ApportionProblem,
  previous: Record<string, IntInput>,
  options: RepairOptions = {},
): RepairResult {
  const norm: NormProblem = normalizeProblem(problem);
  const nodeLimit = options.nodeLimit ?? DEFAULT_NODE_LIMIT;
  const moveBudget =
    options.moveBudget === undefined ? null : toBigInt(options.moveBudget, 'moveBudget');
  if (moveBudget !== null && moveBudget < 0n) {
    throw new Error('moveBudget must be non-negative');
  }

  const prev: bigint[] = norm.ids.map((id) => {
    const v = previous[id];
    return v === undefined ? 0n : toBigInt(v, `previous of ${id}`);
  });
  for (const id of Object.keys(previous)) {
    if (!norm.ids.includes(id)) throw new Error(`previous: unknown id ${id}`);
  }

  const items: SearchItem[] = norm.ids.map((id, i) => ({
    id,
    N: norm.numerators[i],
    lo: norm.lo[i],
    hi: norm.hi[i],
    prev: prev[i],
    isPending: false,
  }));
  // Virtual pending sink: absorbs whatever cannot be placed within budget.
  items.push({
    id: '__pending__',
    N: 0n,
    lo: 0n,
    hi: norm.total,
    prev: null,
    isPending: true,
  });

  const outcome = exactSearch({
    total: norm.total,
    scale: norm.scale,
    items,
    groups: norm.groups,
    moveBudget,
    nodeLimit,
  });
  if (outcome === null) {
    // The pending sink always admits a solution; reaching this means the
    // per-item/group constraints themselves are contradictory.
    throw new Error('repair: infeasible constraints (bounds/groups unsatisfiable)');
  }

  const realValues = outcome.values.slice(0, norm.ids.length);
  const pending = outcome.values[norm.ids.length];
  const deviation = outcome.cost[1];
  const moves = outcome.cost[2];
  const allocation: Allocation = {};
  for (let i = 0; i < norm.ids.length; i++) allocation[norm.ids[i]] = realValues[i];

  const enumeration = proveOptimalityByEnumeration(
    norm,
    { mode: 'repair', prev, moveBudget },
    outcome.cost,
    options.enumerationLimit ?? 200_000,
  );

  return {
    status: pending > 0n ? 'pending' : 'ok',
    allocation,
    pending,
    moves,
    moveBudget,
    deviation,
    deviationScale: norm.scale,
    certificates: {
      conservation: makeConservationReport(norm, allocation, pending),
      lowerBound: makeLowerBoundCertificate(norm, deviation),
      enumeration,
    },
  };
}
