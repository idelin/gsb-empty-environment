/**
 * Edit distance with optimal-path counting and deterministic unranking.
 *
 * Semantics
 * ---------
 * - Operations: insert, delete, substitute. A "match" (substituting a
 *   character with itself) is a free no-op and is NOT a configurable
 *   operation.
 * - Operation costs must be finite numbers strictly greater than zero.
 *   Zero-cost (and negative-cost) operations are forbidden and rejected
 *   by {@link validateCosts}.
 * - `count` is the number of distinct optimal *operation paths* (edit
 *   scripts), not the number of distinct outcome strings. Two paths that
 *   differ in any step count as two, even when their observable effect is
 *   identical (e.g. deleting the first vs. the second 'a' of "aa").
 * - `count` is an exact `bigint`; it is never rounded to a float.
 *
 * Ranking order (public, see {@link MOVE_ORDER})
 * ----------------------------------------------
 * Optimal paths are ordered lexicographically by their operation sequence
 * read from left to right, where the move kinds are ordered
 * `diagonal < delete < insert` (`diagonal` covers both match and
 * substitute). `unrank(0)` returns the lexicographically smallest optimal
 * path and `unrank(count - 1)` the largest. The order is total and
 * deterministic: same inputs always yield the same k-th path.
 */

export interface OperationCosts {
  insert: number;
  delete: number;
  substitute: number;
}

export const DEFAULT_COSTS: Readonly<OperationCosts> = Object.freeze({
  insert: 1,
  delete: 1,
  substitute: 1,
});

export type MoveKind = 'diagonal' | 'delete' | 'insert';

/**
 * Public tie-breaking (boundary) order used for ranking optimal paths.
 * At any position, a diagonal move (match or substitute) sorts before a
 * delete, which sorts before an insert.
 */
export const MOVE_ORDER: readonly MoveKind[] = Object.freeze([
  'diagonal',
  'delete',
  'insert',
]);

export type Operation =
  | { readonly type: 'match'; readonly char: string }
  | { readonly type: 'substitute'; readonly from: string; readonly to: string }
  | { readonly type: 'delete'; readonly char: string }
  | { readonly type: 'insert'; readonly char: string };

export interface EditDistanceResult {
  readonly cost: number;
  readonly count: bigint;
}

const COST_NAMES: readonly (keyof OperationCosts)[] = [
  'insert',
  'delete',
  'substitute',
];

/**
 * Validates operation costs. Every cost must be a finite number strictly
 * greater than zero: zero-cost operations are forbidden because they would
 * make the number of "optimal" paths ill-defined for this library, and
 * negative costs would break the notion of a minimum altogether.
 *
 * @throws {TypeError} if a cost is not a finite number.
 * @throws {RangeError} if a cost is zero or negative.
 */
export function validateCosts(costs: OperationCosts): void {
  for (const name of COST_NAMES) {
    const value = costs[name];
    if (typeof value !== 'number' || Number.isNaN(value) || !Number.isFinite(value)) {
      throw new TypeError(`cost "${name}" must be a finite number, got ${String(value)}`);
    }
    if (value <= 0) {
      throw new RangeError(
        `cost "${name}" must be > 0; zero-cost and negative-cost operations are forbidden, got ${value}`,
      );
    }
  }
}

function toRank(k: bigint | number | string): bigint {
  if (typeof k === 'bigint') return k;
  if (typeof k === 'number') {
    if (!Number.isSafeInteger(k)) {
      throw new TypeError(`rank must be a safe integer, got ${k}`);
    }
    return BigInt(k);
  }
  if (typeof k === 'string') {
    if (!/^-?\d+$/.test(k)) {
      throw new TypeError(`rank string must be an integer, got ${JSON.stringify(k)}`);
    }
    return BigInt(k);
  }
  throw new TypeError(`rank must be a bigint, number or string, got ${typeof k}`);
}

interface Tables {
  cost: number[][];
  count: bigint[][];
}

function emptyTables(rows: number, cols: number): Tables {
  const cost: number[][] = [];
  const count: bigint[][] = [];
  for (let i = 0; i < rows; i++) {
    cost.push(new Array<number>(cols).fill(0));
    count.push(new Array<bigint>(cols).fill(0n));
  }
  return { cost, count };
}

/** Forward tables: cost[i][j] = min cost turning src[0..i) into tgt[0..j). */
function computeForward(src: string[], tgt: string[], costs: OperationCosts): Tables {
  const m = src.length;
  const n = tgt.length;
  const { cost, count } = emptyTables(m + 1, n + 1);
  count[0][0] = 1n;
  for (let i = 1; i <= m; i++) {
    cost[i][0] = cost[i - 1][0] + costs.delete;
    count[i][0] = 1n;
  }
  for (let j = 1; j <= n; j++) {
    cost[0][j] = cost[0][j - 1] + costs.insert;
    count[0][j] = 1n;
  }
  for (let i = 1; i <= m; i++) {
    for (let j = 1; j <= n; j++) {
      const diagonal =
        cost[i - 1][j - 1] + (src[i - 1] === tgt[j - 1] ? 0 : costs.substitute);
      const del = cost[i - 1][j] + costs.delete;
      const ins = cost[i][j - 1] + costs.insert;
      const best = Math.min(diagonal, del, ins);
      let ways = 0n;
      if (diagonal === best) ways += count[i - 1][j - 1];
      if (del === best) ways += count[i - 1][j];
      if (ins === best) ways += count[i][j - 1];
      cost[i][j] = best;
      count[i][j] = ways;
    }
  }
  return { cost, count };
}

/** Suffix tables: cost[i][j] = min cost turning src[i..] into tgt[j..]. */
function computeSuffix(src: string[], tgt: string[], costs: OperationCosts): Tables {
  const m = src.length;
  const n = tgt.length;
  const { cost, count } = emptyTables(m + 1, n + 1);
  count[m][n] = 1n;
  for (let i = m; i >= 0; i--) {
    for (let j = n; j >= 0; j--) {
      if (i === m && j === n) continue;
      let best = Infinity;
      let ways = 0n;
      if (i < m && j < n) {
        const w = src[i] === tgt[j] ? 0 : costs.substitute;
        const cand = w + cost[i + 1][j + 1];
        if (cand < best) {
          best = cand;
          ways = 0n;
        }
        if (cand === best) ways += count[i + 1][j + 1];
      }
      if (i < m) {
        const cand = costs.delete + cost[i + 1][j];
        if (cand < best) {
          best = cand;
          ways = 0n;
        }
        if (cand === best) ways += count[i + 1][j];
      }
      if (j < n) {
        const cand = costs.insert + cost[i][j + 1];
        if (cand < best) {
          best = cand;
          ways = 0n;
        }
        if (cand === best) ways += count[i][j + 1];
      }
      cost[i][j] = best;
      count[i][j] = ways;
    }
  }
  return { cost, count };
}

export class EditDistance {
  readonly source: string;
  readonly target: string;
  readonly costs: Readonly<OperationCosts>;

  readonly #src: string[];
  readonly #tgt: string[];
  readonly #cost: number[][];
  readonly #count: bigint[][];
  #suffixCost: number[][] | null = null;
  #suffixCount: bigint[][] | null = null;

  constructor(source: string, target: string, costs: OperationCosts = DEFAULT_COSTS) {
    validateCosts(costs);
    this.source = source;
    this.target = target;
    this.costs = Object.freeze({ ...costs });
    this.#src = Array.from(source);
    this.#tgt = Array.from(target);
    const tables = computeForward(this.#src, this.#tgt, this.costs);
    this.#cost = tables.cost;
    this.#count = tables.count;
  }

  /** Minimum total cost of transforming `source` into `target`. */
  get cost(): number {
    return this.#cost[this.#src.length][this.#tgt.length];
  }

  /** Exact number of distinct optimal operation paths (edit scripts). */
  get count(): bigint {
    return this.#count[this.#src.length][this.#tgt.length];
  }

  #ensureSuffix(): void {
    if (this.#suffixCost === null || this.#suffixCount === null) {
      const tables = computeSuffix(this.#src, this.#tgt, this.costs);
      this.#suffixCost = tables.cost;
      this.#suffixCount = tables.count;
    }
  }

  /**
   * Returns the k-th (0-based) optimal operation path in the public
   * ranking order defined by {@link MOVE_ORDER}.
   *
   * @param k rank of the desired path; `0 <= k < count`.
   * @throws {RangeError} if `k` is out of range.
   */
  unrank(k: bigint | number | string): Operation[] {
    this.#ensureSuffix();
    const suffixCost = this.#suffixCost as number[][];
    const suffixCount = this.#suffixCount as bigint[][];
    let rank = toRank(k);
    const total = this.count;
    if (rank < 0n || rank >= total) {
      throw new RangeError(`rank ${rank} out of range [0, ${total})`);
    }
    const m = this.#src.length;
    const n = this.#tgt.length;
    const path: Operation[] = [];
    let i = 0;
    let j = 0;
    while (i < m || j < n) {
      const here = suffixCost[i][j];
      let advanced = false;
      for (const move of MOVE_ORDER) {
        let op: Operation;
        let ni: number;
        let nj: number;
        let w: number;
        if (move === 'diagonal') {
          if (i >= m || j >= n) continue;
          const a = this.#src[i];
          const b = this.#tgt[j];
          if (a === b) {
            op = { type: 'match', char: a };
            w = 0;
          } else {
            op = { type: 'substitute', from: a, to: b };
            w = this.costs.substitute;
          }
          ni = i + 1;
          nj = j + 1;
        } else if (move === 'delete') {
          if (i >= m) continue;
          op = { type: 'delete', char: this.#src[i] };
          w = this.costs.delete;
          ni = i + 1;
          nj = j;
        } else {
          if (j >= n) continue;
          op = { type: 'insert', char: this.#tgt[j] };
          w = this.costs.insert;
          ni = i;
          nj = j + 1;
        }
        if (w + suffixCost[ni][nj] !== here) continue;
        const block = suffixCount[ni][nj];
        if (rank < block) {
          path.push(op);
          i = ni;
          j = nj;
          advanced = true;
          break;
        }
        rank -= block;
      }
      if (!advanced) {
        throw new Error('internal error: inconsistent path tables');
      }
    }
    return path;
  }
}

/** Convenience wrapper: minimum cost and number of optimal paths. */
export function editDistance(
  source: string,
  target: string,
  costs: OperationCosts = DEFAULT_COSTS,
): EditDistanceResult {
  const ed = new EditDistance(source, target, costs);
  return { cost: ed.cost, count: ed.count };
}

/** Convenience wrapper: the k-th (0-based) optimal operation path. */
export function unrankPath(
  source: string,
  target: string,
  k: bigint | number | string,
  costs: OperationCosts = DEFAULT_COSTS,
): Operation[] {
  return new EditDistance(source, target, costs).unrank(k);
}

/** Total cost of a path under the given costs (matches are free). */
export function pathCost(path: readonly Operation[], costs: OperationCosts): number {
  let total = 0;
  for (const op of path) {
    if (op.type === 'insert') total += costs.insert;
    else if (op.type === 'delete') total += costs.delete;
    else if (op.type === 'substitute') total += costs.substitute;
  }
  return total;
}

/**
 * Applies an operation path to `source` and returns the resulting string.
 *
 * @throws {Error} if the path is inconsistent with `source`.
 */
export function applyPath(source: string, path: readonly Operation[]): string {
  const src = Array.from(source);
  const out: string[] = [];
  let i = 0;
  for (const op of path) {
    switch (op.type) {
      case 'match':
        if (src[i] !== op.char) {
          throw new Error(`match expects ${JSON.stringify(op.char)} at source[${i}]`);
        }
        out.push(op.char);
        i++;
        break;
      case 'substitute':
        if (src[i] !== op.from) {
          throw new Error(`substitute expects ${JSON.stringify(op.from)} at source[${i}]`);
        }
        out.push(op.to);
        i++;
        break;
      case 'delete':
        if (src[i] !== op.char) {
          throw new Error(`delete expects ${JSON.stringify(op.char)} at source[${i}]`);
        }
        i++;
        break;
      case 'insert':
        out.push(op.char);
        break;
    }
  }
  if (i !== src.length) {
    throw new Error('path does not consume the whole source string');
  }
  return out.join('');
}
