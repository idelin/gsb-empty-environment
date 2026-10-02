import { Fraction } from './fraction.ts';
import {
  normalize,
  type NormalizedProblem,
  type SolveInput,
} from './model.ts';
import { checkFeasibility, tighten } from './bounds.ts';
import { buildCertificate, type Certificate } from './certificates.ts';
import { largestRemainder } from './largestRemainder.ts';

export interface Objective {
  /** Present only in re-weighting mode (previous allocation given). */
  movement?: bigint;
  /** Sum of |x_i - ideal_i| as an exact Fraction. */
  deviation: Fraction;
}

export interface SearchStats {
  nodesExplored: number;
  feasibleCount: number;
  /**
   * 'largest-remainder-baseline': no extra constraints, where the largest
   * remainder method is provably deviation-optimal (exact, O(n log n)).
   * 'exhaustive-enumeration': exact small-scale search over the feasible
   * region with all item/group bounds applied jointly.
   */
  method: 'largest-remainder-baseline' | 'exhaustive-enumeration';
}

export type SolveResult =
  | {
      status: 'optimal';
      allocation: Record<string, bigint>;
      objective: Objective;
      search: SearchStats;
      certificate: Certificate;
    }
  | {
      status: 'infeasible';
      /** 缺额: budget shortfall that stays pending instead of being forced. */
      deficit: bigint;
      /** 未决: the undecided (unallocatable) amount, equal to the deficit. */
      pending: bigint;
      violations: string[];
    }
  | { status: 'error'; error: string };

class SearchLimitExceeded extends Error {}

const maxB = (a: bigint, b: bigint): bigint => (a > b ? a : b);
const minB = (a: bigint, b: bigint): bigint => (a < b ? a : b);
const absB = (x: bigint): bigint => (x < 0n ? -x : x);

interface GroupRuntime {
  lower: bigint | null;
  upper: bigint | null;
  /** remLo[pos]/remHi[pos]: min/max sum of members AFTER position pos. */
  remLo: Map<number, bigint>;
  remHi: Map<number, bigint>;
}

/**
 * Exact small-scale solver. Enumerates the integer feasible region with
 * strong pruning (bounds pre-tightened to a fixpoint across ALL item and
 * overlapping group constraints jointly), so the optimum is global —
 * never a per-group greedy pass that could break another group.
 *
 * Objective (lexicographic):
 *   re-weighting mode: (1) movement sum|x - prev|, (2) deviation sum|x - ideal|
 *   plain mode:        (1) deviation sum|x - ideal|
 * Remaining ties: lexicographically smallest allocation in ascending id
 * order (deterministic, stable).
 */
export function solve(input: SolveInput): SolveResult {
  const norm = normalize(input);
  if (!norm.ok) return { status: 'error', error: norm.error };
  const problem = norm.problem;

  // Fast path: with NO extra constraints (no groups, no item bounds, no
  // previous allocation) the largest remainder method is exactly the
  // deviation-minimizing allocation, even for astronomically large totals.
  const unconstrained =
    (input.groups === undefined || input.groups.length === 0) &&
    input.previous === undefined &&
    input.items.every((it) => it.lower === undefined && it.upper === undefined);
  if (unconstrained) {
    const lr = largestRemainder(input.items, input.total);
    if (lr.status === 'error') return { status: 'error', error: lr.error };
    let deviation = Fraction.zero();
    for (const d of lr.details) {
      deviation = deviation.add(d.ideal.sub(new Fraction(d.allocation, 1n)).abs());
    }
    const objective: Objective = { deviation };
    const search: SearchStats = {
      nodesExplored: 0,
      feasibleCount: 1,
      method: 'largest-remainder-baseline',
    };
    return {
      status: 'optimal',
      allocation: lr.allocation,
      objective,
      search,
      certificate: buildCertificate(problem, lr.allocation, objective, search),
    };
  }

  const bounds = tighten(problem);
  const report = checkFeasibility(problem, bounds);
  if (!report.feasible) {
    return {
      status: 'infeasible',
      deficit: report.deficit,
      pending: report.deficit,
      violations: report.violations,
    };
  }

  try {
    return runSearch(problem, bounds.lo, bounds.hi);
  } catch (e) {
    if (e instanceof SearchLimitExceeded) {
      return {
        status: 'error',
        error: `search node cap ${problem.maxNodes} exceeded; instance too large for exact solving`,
      };
    }
    throw e;
  }
}

function runSearch(
  problem: NormalizedProblem,
  lo: bigint[],
  hi: bigint[],
): SolveResult {
  const n = problem.items.length;
  const total = problem.total;
  const prev = problem.previous;

  // Items are visited in ascending id order: together with ascending value
  // iteration and strict-improvement updates, ties resolve to the
  // lexicographically smallest allocation (stable, id-based).
  const order = problem.items
    .map((_, i) => i)
    .sort((a, b) => (problem.items[a].id < problem.items[b].id ? -1 : 1));

  const posOf = new Array<number>(n);
  order.forEach((itemIdx, pos) => {
    posOf[itemIdx] = pos;
  });

  const loAt = (pos: number): bigint => lo[order[pos]];
  const hiAt = (pos: number): bigint => hi[order[pos]];

  const suffixLo = new Array<bigint>(n + 1).fill(0n);
  const suffixHi = new Array<bigint>(n + 1).fill(0n);
  for (let p = n - 1; p >= 0; p--) {
    suffixLo[p] = suffixLo[p + 1] + loAt(p);
    suffixHi[p] = suffixHi[p + 1] + hiAt(p);
  }

  const groups: GroupRuntime[] = problem.groups.map((g) => {
    const positions = g.memberIdx.map((m) => posOf[m]).sort((a, b) => a - b);
    const remLo = new Map<number, bigint>();
    const remHi = new Map<number, bigint>();
    let accLo = 0n;
    let accHi = 0n;
    for (let k = positions.length - 1; k >= 0; k--) {
      remLo.set(positions[k], accLo);
      remHi.set(positions[k], accHi);
      accLo += loAt(positions[k]);
      accHi += hiAt(positions[k]);
    }
    return { lower: g.lower, upper: g.upper, remLo, remHi };
  });
  const groupsAt: number[][] = Array.from({ length: n }, () => []);
  problem.groups.forEach((g, gi) => {
    for (const m of g.memberIdx) groupsAt[posOf[m]].push(gi);
  });

  const x = new Array<bigint>(n).fill(0n);
  const gsum = new Array<bigint>(groups.length).fill(0n);

  let nodes = 0;
  let feasibleCount = 0;
  let bestX: bigint[] | null = null;
  let bestMovement: bigint | null = null;
  let bestDeviation: Fraction | null = null;

  const deviationOf = (alloc: bigint[]): Fraction => {
    let dev = Fraction.zero();
    for (let i = 0; i < n; i++) {
      dev = dev.add(problem.ideal[i].sub(new Fraction(alloc[i], 1n)).abs());
    }
    return dev;
  };

  const dfs = (pos: number, sumSoFar: bigint, movementSoFar: bigint): void => {
    if (++nodes > problem.maxNodes) throw new SearchLimitExceeded();

    if (sumSoFar + suffixLo[pos] > total || sumSoFar + suffixHi[pos] < total) return;

    if (pos === n) {
      if (sumSoFar !== total) return;
      for (let gi = 0; gi < groups.length; gi++) {
        const g = groups[gi];
        if (g.lower !== null && gsum[gi] < g.lower) return;
        if (g.upper !== null && gsum[gi] > g.upper) return;
      }
      feasibleCount++;
      const alloc = x.slice(); // x is indexed by original item index
      const movement = prev === null ? null : movementSoFar;
      const deviation = deviationOf(alloc);
      const better =
        bestX === null ||
        (movement !== null && bestMovement !== null && movement !== bestMovement
          ? movement < bestMovement
          : bestDeviation === null || deviation.cmp(bestDeviation) < 0);
      if (better) {
        bestX = alloc;
        bestMovement = movement;
        bestDeviation = deviation;
      }
      return;
    }

    const itemIdx = order[pos];
    const minV = maxB(loAt(pos), total - sumSoFar - suffixHi[pos + 1]);
    const maxV = minB(hiAt(pos), total - sumSoFar - suffixLo[pos + 1]);

    for (let v = minV; v <= maxV; v++) {
      let movementNext = movementSoFar;
      if (prev !== null) {
        movementNext += absB(v - prev[itemIdx]);
        if (bestMovement !== null && movementNext > bestMovement) continue;
      }
      let ok = true;
      for (const gi of groupsAt[pos]) {
        const g = groups[gi];
        const s = gsum[gi] + v;
        if (g.upper !== null && s + (g.remLo.get(pos) ?? 0n) > g.upper) {
          ok = false;
          break;
        }
        if (g.lower !== null && s + (g.remHi.get(pos) ?? 0n) < g.lower) {
          ok = false;
          break;
        }
      }
      if (!ok) continue;
      x[itemIdx] = v;
      for (const gi of groupsAt[pos]) gsum[gi] += v;
      dfs(pos + 1, sumSoFar + v, movementNext);
      for (const gi of groupsAt[pos]) gsum[gi] -= v;
    }
  };

  dfs(0, 0n, 0n);

  if (bestX === null || bestDeviation === null) {
    // Tightening said feasible but search found nothing: treat as infeasible.
    return {
      status: 'infeasible',
      deficit: 0n,
      pending: 0n,
      violations: ['no integer allocation satisfies all constraints'],
    };
  }

  const allocation: Record<string, bigint> = {};
  problem.items.forEach((it, i) => {
    allocation[it.id] = (bestX as bigint[])[i];
  });

  const objective: Objective = { deviation: bestDeviation };
  if (bestMovement !== null) objective.movement = bestMovement;

  const search: SearchStats = {
    nodesExplored: nodes,
    feasibleCount,
    method: 'exhaustive-enumeration',
  };

  return {
    status: 'optimal',
    allocation,
    objective,
    search,
    certificate: buildCertificate(problem, allocation, objective, search),
  };
}
