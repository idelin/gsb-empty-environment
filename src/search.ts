/**
 * Exact small-scale lexicographic branch-and-bound over integer allocations.
 *
 * Cost tuple (compared lexicographically):
 *   [pending, deviation, moves]
 * - pending:    units left unallocated (virtual sink item, repair mode only)
 * - deviation:  sum_i |x_i*S - T*N_i|, exact scaled distance to ideal shares
 * - moves:      sum_i |x_i - previous_i| (repair mode only, final tie-break)
 *
 * Hard constraints: conservation (sum of real items + pending = total),
 * per-item [lo, hi], arbitrary overlapping group min/max sums, and an
 * optional move budget. All arithmetic is BigInt; a node limit guards the
 * "small scale" contract and throws SearchSpaceTooLarge beyond it.
 */

export interface SearchItem {
  readonly id: string;
  /** Scaled weight numerator N_i (0 for the pending sink). */
  readonly N: bigint;
  readonly lo: bigint;
  readonly hi: bigint;
  /** Previous allocation (repair mode); null in pure apportion mode. */
  readonly prev: bigint | null;
  /** True for the virtual pending sink item. */
  readonly isPending: boolean;
}

export interface SearchGroup {
  readonly id: string;
  readonly min: bigint;
  readonly max: bigint;
  /** Indices into the items array. */
  readonly members: readonly number[];
}

/** [pending, deviation, moves], lexicographic. */
export type Cost = readonly [bigint, bigint, bigint];

export function cmpCost(a: Cost, b: Cost): number {
  if (a[0] !== b[0]) return a[0] < b[0] ? -1 : 1;
  if (a[1] !== b[1]) return a[1] < b[1] ? -1 : 1;
  if (a[2] !== b[2]) return a[2] < b[2] ? -1 : 1;
  return 0;
}

export class SearchSpaceTooLarge extends Error {
  readonly nodes: number;
  constructor(nodes: number) {
    super(`exact search aborted: node limit exceeded (${nodes})`);
    this.name = 'SearchSpaceTooLarge';
    this.nodes = nodes;
  }
}

export interface SearchOptions {
  readonly total: bigint;
  /** S = sum of scaled weight numerators. */
  readonly scale: bigint;
  readonly items: readonly SearchItem[];
  readonly groups: readonly SearchGroup[];
  readonly moveBudget: bigint | null;
  readonly nodeLimit: number;
}

export interface SearchOutcome {
  readonly values: readonly bigint[];
  readonly cost: Cost;
  readonly nodes: number;
}

export const DEFAULT_NODE_LIMIT = 5_000_000;

/**
 * Equal-cost tie-break: prefer the lexicographically larger value vector in
 * ascending-id order, so a contested unit goes to the smaller stable id,
 * matching the largest-remainder baseline's tie rule.
 */
function lexGreater(a: readonly bigint[], b: readonly bigint[]): boolean {
  for (let i = 0; i < a.length; i++) {
    if (a[i] !== b[i]) return a[i] > b[i];
  }
  return false;
}

export function exactSearch(opts: SearchOptions): SearchOutcome | null {
  const { total, scale, items, groups, moveBudget, nodeLimit } = opts;
  const n = items.length;

  // Suffix sums over positions: feasibility and bound pruning.
  const sufLo = new Array<bigint>(n + 1).fill(0n);
  const sufHi = new Array<bigint>(n + 1).fill(0n);
  const sufN = new Array<bigint>(n + 1).fill(0n);
  for (let i = n - 1; i >= 0; i--) {
    sufLo[i] = sufLo[i + 1] + items[i].lo;
    sufHi[i] = sufHi[i + 1] + items[i].hi;
    sufN[i] = sufN[i + 1] + items[i].N;
  }

  // Per-group suffix sums of member lo/hi: remLo[g][i] covers members at
  // positions >= i, so group feasibility can be checked incrementally.
  const remLo: bigint[][] = [];
  const remHi: bigint[][] = [];
  for (const g of groups) {
    const loArr = new Array<bigint>(n + 1).fill(0n);
    const hiArr = new Array<bigint>(n + 1).fill(0n);
    for (const m of g.members) {
      loArr[m] = items[m].lo;
      hiArr[m] = items[m].hi;
    }
    for (let i = n - 1; i >= 0; i--) {
      loArr[i] += loArr[i + 1];
      hiArr[i] += hiArr[i + 1];
    }
    remLo.push(loArr);
    remHi.push(hiArr);
  }
  const itemGroups: number[][] = items.map((_, i) => {
    const out: number[] = [];
    for (let gi = 0; gi < groups.length; gi++) {
      if (groups[gi].members.includes(i)) out.push(gi);
    }
    return out;
  });

  let nodes = 0;
  let bestValues: bigint[] | null = null;
  let bestCost: Cost | null = null;
  const values = new Array<bigint>(n).fill(0n);
  const groupSum = new Array<bigint>(groups.length).fill(0n);

  const abs = (v: bigint): bigint => (v < 0n ? -v : v);

  function tryAssign(i: number, v: bigint, sum: bigint, pend: bigint, mov: bigint, dev: bigint): void {
    const it = items[i];
    let p = pend;
    let m = mov;
    if (it.isPending) p += v;
    if (it.prev !== null) m += abs(v - it.prev);
    const d = it.isPending ? dev : dev + abs(v * scale - total * it.N);
    for (const gi of itemGroups[i]) groupSum[gi] += v;
    values[i] = v;
    dfs(i + 1, sum + v, p, m, d);
    for (const gi of itemGroups[i]) groupSum[gi] -= v;
  }

  function dfs(i: number, sum: bigint, pend: bigint, mov: bigint, dev: bigint): void {
    if (++nodes > nodeLimit) throw new SearchSpaceTooLarge(nodes);

    // Conservation feasibility: remaining items must be able to reach total.
    if (sum + sufLo[i] > total || sum + sufHi[i] < total) return;
    // Joint group feasibility (all overlapping groups checked together).
    for (let gi = 0; gi < groups.length; gi++) {
      const lo = groupSum[gi] + remLo[gi][i];
      const hi = groupSum[gi] + remHi[gi][i];
      if (hi < groups[gi].min || lo > groups[gi].max) return;
    }
    if (moveBudget !== null && mov > moveBudget) return;

    if (bestCost !== null) {
      // Lower bound on remaining deviation: |s*S - T*sumN| minimized over
      // the achievable remaining-sum interval [sufLo, sufHi].
      const loB = sufLo[i] * scale;
      const hiB = sufHi[i] * scale;
      const target = total * sufN[i];
      let lb = 0n;
      if (target < loB) lb = loB - target;
      else if (target > hiB) lb = target - hiB;
      const bound: Cost = [pend, dev + lb, mov];
      // Strict: equal-cost branches may still hold a tie-break winner.
      if (cmpCost(bound, bestCost) > 0) return;
    }

    if (i === n) {
      const cost: Cost = [pend, dev, mov];
      if (
        bestCost === null ||
        cmpCost(cost, bestCost) < 0 ||
        (cmpCost(cost, bestCost) === 0 && bestValues !== null && lexGreater(values, bestValues))
      ) {
        bestCost = cost;
        bestValues = [...values];
      }
      return;
    }

    const it = items[i];

    // Last item is forced by conservation.
    if (i === n - 1) {
      const v = total - sum;
      if (v >= it.lo && v <= it.hi) tryAssign(i, v, sum, pend, mov, dev);
      return;
    }

    // Visit values near the ideal share first so a strong incumbent is
    // found immediately; then expand outward with monotonic cutoffs.
    let center: bigint;
    if (it.isPending || it.N === 0n) {
      center = it.lo;
    } else {
      center = (2n * total * it.N + scale) / (2n * scale);
      if (center < it.lo) center = it.lo;
      if (center > it.hi) center = it.hi;
    }

    const hopeless = (v: bigint, dir: 1n | -1n): boolean => {
      if (bestCost === null) return false;
      // Once v moves past both the ideal point and the previous value in
      // this direction, deviation and moves only grow; a bound failure here
      // is final for the whole direction.
      const pastIdeal = it.N === 0n ? v * scale >= 0n : dir === 1n ? v * scale >= total * it.N : v * scale <= total * it.N;
      const pastPrev = it.prev === null || (dir === 1n ? v >= it.prev : v <= it.prev);
      if (!pastIdeal || !pastPrev) return false;
      let m = mov;
      if (it.prev !== null) m += abs(v - it.prev);
      if (moveBudget !== null && m > moveBudget) return true;
      const d = dev + abs(v * scale - total * it.N);
      const bound: Cost = [pend, d, m];
      return cmpCost(bound, bestCost) > 0;
    };

    for (let v = center; v <= it.hi; v++) {
      if (v > center && hopeless(v, 1n)) break;
      if (moveBudget !== null && it.prev !== null && mov + abs(v - it.prev) > moveBudget && hopeless(v, 1n)) break;
      tryAssign(i, v, sum, pend, mov, dev);
    }
    for (let v = center - 1n; v >= it.lo; v--) {
      if (hopeless(v, -1n)) break;
      tryAssign(i, v, sum, pend, mov, dev);
    }
  }

  dfs(0, 0n, 0n, 0n, 0n);
  if (bestValues === null || bestCost === null) return null;
  return { values: bestValues, cost: bestCost, nodes };
}

/**
 * Min/max achievable total sum under bounds + group constraints only
 * (no conservation target). Returns null when the feasible region is empty.
 */
export function feasibleSumRange(
  items: readonly { lo: bigint; hi: bigint }[],
  groups: readonly SearchGroup[],
  nodeLimit: number = DEFAULT_NODE_LIMIT,
): { min: bigint; max: bigint } | null {
  const n = items.length;
  if (groups.length === 0) {
    let min = 0n;
    let max = 0n;
    for (const it of items) {
      if (it.lo > it.hi) return null;
      min += it.lo;
      max += it.hi;
    }
    return { min, max };
  }
  const remLo: bigint[][] = [];
  const remHi: bigint[][] = [];
  for (const g of groups) {
    const loArr = new Array<bigint>(n + 1).fill(0n);
    const hiArr = new Array<bigint>(n + 1).fill(0n);
    for (const m of g.members) {
      loArr[m] = items[m].lo;
      hiArr[m] = items[m].hi;
    }
    for (let i = n - 1; i >= 0; i--) {
      loArr[i] += loArr[i + 1];
      hiArr[i] += hiArr[i + 1];
    }
    remLo.push(loArr);
    remHi.push(hiArr);
  }
  const itemGroups: number[][] = items.map((_, i) => {
    const out: number[] = [];
    for (let gi = 0; gi < groups.length; gi++) {
      if (groups[gi].members.includes(i)) out.push(gi);
    }
    return out;
  });
  let nodes = 0;
  let found = false;
  let best = 0n;
  let worst = 0n;
  let sum = 0n;
  const groupSum = new Array<bigint>(groups.length).fill(0n);

  function walk(i: number): void {
    if (++nodes > nodeLimit) throw new SearchSpaceTooLarge(nodes);
    for (let gi = 0; gi < groups.length; gi++) {
      const lo = groupSum[gi] + remLo[gi][i];
      const hi = groupSum[gi] + remHi[gi][i];
      if (hi < groups[gi].min || lo > groups[gi].max) return;
    }
    if (i === n) {
      if (!found || sum < best) best = sum;
      if (!found || sum > worst) worst = sum;
      found = true;
      return;
    }
    const it = items[i];
    for (let v = it.lo; v <= it.hi; v++) {
      sum += v;
      for (const gi of itemGroups[i]) groupSum[gi] += v;
      walk(i + 1);
      for (const gi of itemGroups[i]) groupSum[gi] -= v;
      sum -= v;
    }
  }

  walk(0);
  return found ? { min: best, max: worst } : null;
}
