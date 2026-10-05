/**
 * Minimum-cost perfect assignment solver with:
 *  - local cost updates (setCost / enableEdge / disableEdge)
 *  - forbidden (disabled) edges
 *  - bigint costs (negative and beyond Number.MAX_SAFE_INTEGER)
 *  - Hall deficiency certificate when infeasible
 *  - primal (matching) + dual (potentials) certificates for independent
 *    recomputation via verifyCertificate()
 *
 * Any mutation invalidates all cached state; solve() always recomputes
 * potentials from scratch, so stale duals are never reused after an update.
 */

export interface DualCertificate {
  /** Row potentials u[i], length n. Feasibility: u[i] + v[j] <= cost[i][j]. */
  u: bigint[];
  /** Column potentials v[j], length n. */
  v: bigint[];
}

export interface OptimalResult {
  status: 'optimal';
  /** matching[i] = column assigned to row i (a permutation of 0..n-1). */
  matching: number[];
  /** Total cost: sum of cost[i][matching[i]]. */
  cost: bigint;
  dual: DualCertificate;
}

export interface HallDefect {
  /** Row set S with |N(S)| < |S|. */
  rows: number[];
  /** N(S): every column adjacent to S through an allowed edge. */
  neighborhood: number[];
  /** rows.length - neighborhood.length (> 0). */
  deficiency: number;
}

export interface InfeasibleResult {
  status: 'infeasible';
  hallDefect: HallDefect;
}

export type AssignmentResult = OptimalResult | InfeasibleResult;

export class AssignmentSolver {
  readonly n: number;
  private readonly costs: bigint[][];
  private readonly allowed: boolean[][];
  private cached: AssignmentResult | null = null;

  constructor(n: number) {
    if (!Number.isInteger(n) || n < 1) {
      throw new RangeError('n must be a positive integer');
    }
    this.n = n;
    this.costs = Array.from({ length: n }, () => new Array<bigint>(n).fill(0n));
    this.allowed = Array.from({ length: n }, () => new Array<boolean>(n).fill(true));
  }

  /** Set the cost of edge (row, col). Does not change its enabled state. */
  setCost(row: number, col: number, cost: bigint | number): void {
    this.checkIndex(row, col);
    this.costs[row][col] = BigInt(cost);
    this.invalidate();
  }

  /** Forbid edge (row, col): it can no longer be used in any assignment. */
  disableEdge(row: number, col: number): void {
    this.checkIndex(row, col);
    this.allowed[row][col] = false;
    this.invalidate();
  }

  /** Re-allow a previously disabled edge. */
  enableEdge(row: number, col: number): void {
    this.checkIndex(row, col);
    this.allowed[row][col] = true;
    this.invalidate();
  }

  isAllowed(row: number, col: number): boolean {
    this.checkIndex(row, col);
    return this.allowed[row][col];
  }

  getCost(row: number, col: number): bigint {
    this.checkIndex(row, col);
    return this.costs[row][col];
  }

  /**
   * Solve the current instance. Results are cached only until the next
   * mutation; every mutation drops cached potentials and forces a full
   * recompute, so no stale dual can leak into a new certificate.
   */
  solve(): AssignmentResult {
    if (this.cached !== null) return this.cached;

    const { matchRowToCol, matchColToRow, size } = this.maxMatching();
    let result: AssignmentResult;
    if (size < this.n) {
      result = {
        status: 'infeasible',
        hallDefect: this.buildHallDefect(matchRowToCol, matchColToRow),
      };
    } else {
      const { matching, u, v } = this.hungarian();
      let cost = 0n;
      for (let i = 0; i < this.n; i++) cost += this.costs[i][matching[i]];
      result = { status: 'optimal', matching, cost, dual: { u, v } };
    }
    this.cached = result;
    return result;
  }

  private invalidate(): void {
    this.cached = null;
  }

  private checkIndex(row: number, col: number): void {
    if (row < 0 || row >= this.n || col < 0 || col >= this.n) {
      throw new RangeError(`index out of range: (${row}, ${col}), n=${this.n}`);
    }
  }

  /** Kuhn's augmenting-path algorithm on the allowed-edge graph. */
  private maxMatching(): {
    matchRowToCol: number[];
    matchColToRow: number[];
    size: number;
  } {
    const n = this.n;
    const matchRowToCol = new Array<number>(n).fill(-1);
    const matchColToRow = new Array<number>(n).fill(-1);
    let size = 0;

    const tryAugment = (row: number, seen: boolean[]): boolean => {
      for (let col = 0; col < n; col++) {
        if (!this.allowed[row][col] || seen[col]) continue;
        seen[col] = true;
        if (matchColToRow[col] === -1 || tryAugment(matchColToRow[col], seen)) {
          matchRowToCol[row] = col;
          matchColToRow[col] = row;
          return true;
        }
      }
      return false;
    };

    for (let row = 0; row < n; row++) {
      if (tryAugment(row, new Array<boolean>(n).fill(false))) size++;
    }
    return { matchRowToCol, matchColToRow, size };
  }

  /**
   * Maximum-deficiency Hall set: alternating BFS from every unmatched row.
   * Rows reach columns via non-matching allowed edges, columns reach their
   * matched rows. The reachable row set R satisfies N(R) = reachable columns
   * and |R| - |N(R)| = n - |matching| > 0.
   */
  private buildHallDefect(matchRowToCol: number[], matchColToRow: number[]): HallDefect {
    const n = this.n;
    const rowSeen = new Array<boolean>(n).fill(false);
    const colSeen = new Array<boolean>(n).fill(false);
    const queue: number[] = [];

    for (let row = 0; row < n; row++) {
      if (matchRowToCol[row] === -1) {
        rowSeen[row] = true;
        queue.push(row);
      }
    }
    while (queue.length > 0) {
      const row = queue.shift() as number;
      for (let col = 0; col < n; col++) {
        if (!this.allowed[row][col] || colSeen[col]) continue;
        if (matchRowToCol[row] === col) continue; // only non-matching edges
        colSeen[col] = true;
        const next = matchColToRow[col];
        if (next !== -1 && !rowSeen[next]) {
          rowSeen[next] = true;
          queue.push(next);
        }
      }
    }

    const rows: number[] = [];
    const neighborhood: number[] = [];
    for (let i = 0; i < n; i++) if (rowSeen[i]) rows.push(i);
    for (let j = 0; j < n; j++) if (colSeen[j]) neighborhood.push(j);
    return { rows, neighborhood, deficiency: rows.length - neighborhood.length };
  }

  /**
   * Hungarian (Kuhn-Munkres) algorithm, O(n^3), restricted to allowed edges.
   * 1-indexed with a dummy column 0. Maintains dual feasibility
   * u[i] + v[j] <= cost[i][j] on every allowed edge and tightness on matched
   * edges. Terminates because a perfect matching exists in the allowed graph:
   * the alternating tree's row set R always has an allowed edge to a column
   * outside the tree (otherwise N(R) would violate Hall's condition).
   */
  private hungarian(): { matching: number[]; u: bigint[]; v: bigint[] } {
    const n = this.n;
    const u = new Array<bigint>(n + 1).fill(0n);
    const v = new Array<bigint>(n + 1).fill(0n);
    const p = new Array<number>(n + 1).fill(0); // p[j] = row matched to column j
    const way = new Array<number>(n + 1).fill(0);

    for (let i = 1; i <= n; i++) {
      p[0] = i;
      let j0 = 0;
      const minv = new Array<bigint | null>(n + 1).fill(null); // null = +inf
      const used = new Array<boolean>(n + 1).fill(false);
      do {
        used[j0] = true;
        const i0 = p[j0];
        let delta: bigint | null = null;
        let j1 = 0;
        for (let j = 1; j <= n; j++) {
          if (used[j]) continue;
          if (this.allowed[i0 - 1][j - 1]) {
            const cur = this.costs[i0 - 1][j - 1] - u[i0] - v[j];
            if (minv[j] === null || cur < (minv[j] as bigint)) {
              minv[j] = cur;
              way[j] = j0;
            }
          }
          // minv[j] may have been relaxed by an earlier tree row even when
          // edge (i0, j) is disabled; it must still compete for delta.
          if (minv[j] !== null && (delta === null || (minv[j] as bigint) < delta)) {
            delta = minv[j];
            j1 = j;
          }
        }
        if (delta === null) {
          throw new Error('internal: no augmenting edge despite feasible instance');
        }
        const d = delta;
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

    const matching = new Array<number>(n).fill(-1);
    for (let j = 1; j <= n; j++) matching[p[j] - 1] = j - 1;
    return { matching, u: u.slice(1), v: v.slice(1) };
  }
}

/**
 * Independent verification of a solver result against the problem data.
 * Recomputes everything from scratch; does not trust any solver internals.
 */
export function verifyCertificate(solver: AssignmentSolver, result: AssignmentResult): boolean {
  const n = solver.n;

  if (result.status === 'optimal') {
    const { matching, cost, dual } = result;
    if (matching.length !== n || dual.u.length !== n || dual.v.length !== n) return false;

    // Primal: matching is a permutation using only allowed edges.
    const seenCols = new Array<boolean>(n).fill(false);
    let recomputed = 0n;
    for (let i = 0; i < n; i++) {
      const j = matching[i];
      if (!Number.isInteger(j) || j < 0 || j >= n || seenCols[j]) return false;
      if (!solver.isAllowed(i, j)) return false;
      seenCols[j] = true;
      recomputed += solver.getCost(i, j);
    }
    if (recomputed !== cost) return false;

    // Dual: feasibility u[i] + v[j] <= cost[i][j] on every allowed edge,
    // tightness on matched edges, and strong duality sum(u) + sum(v) = cost.
    let dualSum = 0n;
    for (let i = 0; i < n; i++) dualSum += dual.u[i];
    for (let j = 0; j < n; j++) dualSum += dual.v[j];
    if (dualSum !== cost) return false;
    for (let i = 0; i < n; i++) {
      for (let j = 0; j < n; j++) {
        if (!solver.isAllowed(i, j)) continue;
        if (dual.u[i] + dual.v[j] > solver.getCost(i, j)) return false;
      }
    }
    for (let i = 0; i < n; i++) {
      if (dual.u[i] + dual.v[matching[i]] !== solver.getCost(i, matching[i])) return false;
    }
    return true;
  }

  // Infeasible: check the Hall defect. N(S) must contain every column
  // adjacent to S via allowed edges, and |S| > |N(S)| must hold.
  const { rows, neighborhood, deficiency } = result.hallDefect;
  if (rows.length === 0 || deficiency <= 0) return false;
  if (rows.length - neighborhood.length !== deficiency) return false;
  const rowSet = new Set(rows);
  const colSet = new Set(neighborhood);
  if (rowSet.size !== rows.length || colSet.size !== neighborhood.length) return false;
  for (const r of rows) {
    if (!Number.isInteger(r) || r < 0 || r >= n) return false;
  }
  for (const c of neighborhood) {
    if (!Number.isInteger(c) || c < 0 || c >= n) return false;
  }
  for (const r of rows) {
    for (let j = 0; j < n; j++) {
      if (solver.isAllowed(r, j) && !colSet.has(j)) return false;
    }
  }
  return true;
}
