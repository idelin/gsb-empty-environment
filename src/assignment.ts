/**
 * Minimum-cost perfect bipartite assignment solver with:
 *  - local cost updates (setCost / enableEdge / disableEdge)
 *  - forbidden (disabled) edges
 *  - BigInt costs (negative and beyond Number.MAX_SAFE_INTEGER)
 *  - primal + dual certificates for independent verification
 *  - Hall deficiency certificate when no perfect assignment exists
 *
 * Any mutation invalidates the cached result: potentials (duals) computed
 * before an update are never reused afterwards.
 */

export type CostInput = number | bigint;

export interface DualCertificate {
  /** u[i] for rows, i = 0..n-1 */
  rowPotential: bigint[];
  /** v[j] for columns, j = 0..n-1 */
  colPotential: bigint[];
}

export interface OptimalResult {
  status: 'optimal';
  /** assignment[i] = column assigned to row i (a permutation of 0..n-1) */
  assignment: number[];
  /** total cost, equals sum of dual potentials */
  cost: bigint;
  dual: DualCertificate;
}

export interface HallDeficiency {
  /** Row set S (sorted). Every enabled edge leaving S ends in `cols`. */
  rows: number[];
  /** N(S) (sorted). Always |rows| > |cols|, so no perfect matching exists. */
  cols: number[];
}

export interface InfeasibleResult {
  status: 'infeasible';
  hall: HallDeficiency;
}

export type SolveResult = OptimalResult | InfeasibleResult;

function toBigInt(cost: CostInput): bigint {
  if (typeof cost === 'bigint') return cost;
  if (!Number.isSafeInteger(cost)) {
    throw new RangeError(
      `cost ${cost} is not a safe integer; pass a bigint instead`,
    );
  }
  return BigInt(cost);
}

export class AssignmentSolver {
  readonly size: number;
  /** costs[i][j] === null means the edge is forbidden. */
  private costs: (bigint | null)[][];
  private cached: SolveResult | null = null;

  constructor(size: number, costs?: (CostInput | null)[][]) {
    if (!Number.isInteger(size) || size < 0) {
      throw new RangeError(`size must be a non-negative integer, got ${size}`);
    }
    this.size = size;
    this.costs = Array.from({ length: size }, () =>
      new Array<bigint | null>(size).fill(null),
    );
    if (costs !== undefined) {
      if (costs.length !== size || costs.some((row) => row.length !== size)) {
        throw new RangeError('cost matrix must be size x size');
      }
      for (let i = 0; i < size; i++) {
        for (let j = 0; j < size; j++) {
          const c = costs[i][j];
          if (c !== null && c !== undefined) this.costs[i][j] = toBigInt(c);
        }
      }
    }
  }

  private checkIndices(i: number, j: number): void {
    const n = this.size;
    if (!Number.isInteger(i) || !Number.isInteger(j) || i < 0 || j < 0 || i >= n || j >= n) {
      throw new RangeError(`edge index out of range: (${i}, ${j}) for size ${n}`);
    }
  }

  /** Local update: set (or replace) the cost of edge (i, j), enabling it. */
  setCost(i: number, j: number, cost: CostInput): void {
    this.checkIndices(i, j);
    this.costs[i][j] = toBigInt(cost);
    this.invalidate();
  }

  /** Local update: enable edge (i, j) with the given cost. */
  enableEdge(i: number, j: number, cost: CostInput): void {
    this.setCost(i, j, cost);
  }

  /** Local update: forbid edge (i, j) so it can never be selected. */
  disableEdge(i: number, j: number): void {
    this.checkIndices(i, j);
    this.costs[i][j] = null;
    this.invalidate();
  }

  isEnabled(i: number, j: number): boolean {
    this.checkIndices(i, j);
    return this.costs[i][j] !== null;
  }

  cost(i: number, j: number): bigint | null {
    this.checkIndices(i, j);
    return this.costs[i][j];
  }

  /**
   * Any mutation drops the cached result, including dual potentials.
   * Stale potentials are never reused after an update.
   */
  private invalidate(): void {
    this.cached = null;
  }

  /**
   * Solve the current instance. The returned object is cached and shared
   * between calls until the next mutation; treat it as read-only.
   */
  solve(): SolveResult {
    if (this.cached !== null) return this.cached;

    const n = this.size;
    const allowed: boolean[][] = this.costs.map((row) =>
      row.map((c) => c !== null),
    );

    const matching = maxMatching(n, allowed);
    if (matching.size < n) {
      const hall = hallDeficiency(n, allowed, matching.matchRow, matching.matchCol);
      this.cached = { status: 'infeasible', hall };
      return this.cached;
    }

    // A perfect matching over enabled edges exists. Forbidden edges enter the
    // Hungarian matrix with a big-M cost that exceeds any all-enabled total,
    // so the optimum never uses them.
    let absSum = 0n;
    for (let i = 0; i < n; i++) {
      for (let j = 0; j < n; j++) {
        const c = this.costs[i][j];
        if (c !== null) absSum += c < 0n ? -c : c;
      }
    }
    const bigM = 2n * BigInt(Math.max(n, 1)) * absSum + 1n;

    const a: bigint[][] = Array.from({ length: n + 1 }, () =>
      new Array<bigint>(n + 1).fill(0n),
    );
    for (let i = 1; i <= n; i++) {
      for (let j = 1; j <= n; j++) {
        const c = this.costs[i - 1][j - 1];
        a[i][j] = c === null ? bigM : c;
      }
    }

    const hung = hungarian(n, a);

    // Defensive check: the optimum must only use enabled edges.
    for (let i = 0; i < n; i++) {
      if (this.costs[i][hung.assignment[i]] === null) {
        throw new Error('internal error: optimum uses a forbidden edge');
      }
    }

    this.cached = {
      status: 'optimal',
      assignment: hung.assignment,
      cost: hung.cost,
      dual: { rowPotential: hung.u, colPotential: hung.v },
    };
    return this.cached;
  }
}

interface Matching {
  size: number;
  /** matchRow[i] = column matched to row i, or -1 */
  matchRow: number[];
  /** matchCol[j] = row matched to column j, or -1 */
  matchCol: number[];
}

/** Kuhn's augmenting-path algorithm, O(n * E). */
function maxMatching(n: number, allowed: boolean[][]): Matching {
  const matchRow = new Array<number>(n).fill(-1);
  const matchCol = new Array<number>(n).fill(-1);
  const seen = new Array<number>(n).fill(0);
  let stamp = 0;
  let size = 0;

  const tryKuhn = (i: number): boolean => {
    for (let j = 0; j < n; j++) {
      if (!allowed[i][j] || seen[j] === stamp) continue;
      seen[j] = stamp;
      if (matchCol[j] === -1 || tryKuhn(matchCol[j])) {
        matchRow[i] = j;
        matchCol[j] = i;
        return true;
      }
    }
    return false;
  };

  for (let i = 0; i < n; i++) {
    stamp++;
    if (tryKuhn(i)) size++;
  }
  return { size, matchRow, matchCol };
}

/**
 * Hall deficiency certificate from a maximum matching:
 * alternating BFS from all unmatched rows. S = reachable rows,
 * N(S) = reachable columns; every enabled edge from S ends in N(S)
 * and |S| - |N(S)| = n - matchingSize >= 1.
 */
function hallDeficiency(
  n: number,
  allowed: boolean[][],
  matchRow: number[],
  matchCol: number[],
): HallDeficiency {
  const rowSeen = new Array<boolean>(n).fill(false);
  const colSeen = new Array<boolean>(n).fill(false);
  const queue: number[] = [];
  for (let i = 0; i < n; i++) {
    if (matchRow[i] === -1) {
      rowSeen[i] = true;
      queue.push(i);
    }
  }
  while (queue.length > 0) {
    const i = queue.pop() as number;
    for (let j = 0; j < n; j++) {
      if (!allowed[i][j] || matchRow[i] === j || colSeen[j]) continue;
      colSeen[j] = true;
      const r = matchCol[j];
      if (r !== -1 && !rowSeen[r]) {
        rowSeen[r] = true;
        queue.push(r);
      }
    }
  }
  const rows: number[] = [];
  const cols: number[] = [];
  for (let i = 0; i < n; i++) if (rowSeen[i]) rows.push(i);
  for (let j = 0; j < n; j++) if (colSeen[j]) cols.push(j);
  return { rows, cols };
}

/**
 * Hungarian algorithm for minimization, O(n^3), 1-indexed, BigInt costs.
 * Produces potentials u (rows), v (columns) with u[i] + v[j] <= a[i][j]
 * and equality on matched edges, so sum(u) + sum(v) = optimal cost.
 */
function hungarian(
  n: number,
  a: bigint[][],
): { assignment: number[]; cost: bigint; u: bigint[]; v: bigint[] } {
  const u = new Array<bigint>(n + 1).fill(0n);
  const v = new Array<bigint>(n + 1).fill(0n);
  const p = new Array<number>(n + 1).fill(0);
  const way = new Array<number>(n + 1).fill(0);

  for (let i = 1; i <= n; i++) {
    p[0] = i;
    let j0 = 0;
    const minv = new Array<bigint | null>(n + 1).fill(null);
    const used = new Array<boolean>(n + 1).fill(false);
    do {
      used[j0] = true;
      const i0 = p[j0];
      let delta: bigint | null = null;
      let j1 = 0;
      for (let j = 1; j <= n; j++) {
        if (used[j]) continue;
        const cur = a[i0][j] - u[i0] - v[j];
        if (minv[j] === null || cur < (minv[j] as bigint)) {
          minv[j] = cur;
          way[j] = j0;
        }
        if (delta === null || (minv[j] as bigint) < delta) {
          delta = minv[j];
          j1 = j;
        }
      }
      const d = delta as bigint;
      for (let j = 0; j <= n; j++) {
        if (used[j]) {
          u[p[j]] += d;
          v[j] -= d;
        } else if (minv[j] !== null) {
          minv[j] = (minv[j] as bigint) - d;
        }
      }
      j0 = j1;
    } while (p[j0] !== 0);
    do {
      const j1 = way[j0];
      p[j0] = p[j1];
      j0 = j1;
    } while (j0 !== 0);
  }

  const assignment = new Array<number>(n).fill(-1);
  for (let j = 1; j <= n; j++) assignment[p[j] - 1] = j - 1;
  return { assignment, cost: -v[0], u: u.slice(1), v: v.slice(1) };
}
