/**
 * Exact two-phase simplex method over BigInt rationals (dictionary form).
 *
 * Solves:  max c·x  s.t.  A x <= b,  x >= 0.
 *
 * - All arithmetic is exact (Frac); no floating point anywhere.
 * - Bland's rule (lowest-index entering / leaving) guarantees termination:
 *   no cycling on degenerate problems.
 * - A pivot budget is enforced; exhausting it reports "budget_exhausted"
 *   rather than any (possibly false) classification.
 * - Certificates:
 *     optimal    -> primal x and dual y >= 0 with Aᵀy >= c, b·y = c·x
 *     infeasible -> Farkas y >= 0 with Aᵀy >= 0, b·y < 0
 *     unbounded  -> feasible x and ray d >= 0 with A d <= 0, c·d > 0
 */
import { Frac, ZERO, ONE } from "./fraction";

export interface LP {
  c: Frac[];
  A: Frac[][];
  b: Frac[];
}

export type Status = "optimal" | "infeasible" | "unbounded" | "budget_exhausted";

export interface SolveResult {
  status: Status;
  pivots: number;
  /** optimal objective value (status "optimal") */
  value?: Frac;
  /** primal point (status "optimal" | "unbounded") */
  x?: Frac[];
  /** dual certificate (status "optimal" | "infeasible") */
  y?: Frac[];
  /** unbounded ray (status "unbounded") */
  d?: Frac[];
}

const NEG_ONE = ONE.neg();

interface Budget {
  left: number;
  used: number;
}

function spend(b: Budget): boolean {
  if (b.left <= 0) return false;
  b.left--;
  b.used++;
  return true;
}

/**
 * Dictionary tableau. Row i reads:
 *   x_{B[i]} = rhs[i] - sum_j T[i][j] * x_j
 * where the sum ranges over nonbasic variables (basic columns are stored as 0).
 * Objective: z = z0 + sum_j z[j] * x_j.
 */
class Tableau {
  T: Frac[][] = [];
  rhs: Frac[] = [];
  z: Frac[] = [];
  z0: Frac = ZERO;
  B: number[] = [];
  constructor(public rows: number, public cols: number) {
    for (let i = 0; i < rows; i++) {
      this.T.push(new Array<Frac>(cols).fill(ZERO));
      this.rhs.push(ZERO);
      this.B.push(-1);
    }
    for (let j = 0; j < cols; j++) this.z.push(ZERO);
  }
}

/** Pivot on row l, entering column e (T[l][e] must be nonzero). */
function pivot(t: Tableau, l: number, e: number): void {
  const piv = t.T[l][e];
  if (piv.isZero()) throw new Error("pivot on zero element");
  const oldBasic = t.B[l];
  const inv = ONE.div(piv);
  const cols = t.cols;

  // New row for the entering variable: x_e = rhs/piv - sum newRow[j] x_j.
  const newRow = new Array<Frac>(cols);
  for (let j = 0; j < cols; j++) newRow[j] = t.T[l][j].mul(inv);
  newRow[oldBasic] = inv; // old basic variable becomes nonbasic
  newRow[e] = ZERO; // e becomes basic
  const newRhs = t.rhs[l].mul(inv);

  // Substitute x_e into the other rows.
  for (let i = 0; i < t.rows; i++) {
    if (i === l) continue;
    const factor = t.T[i][e];
    if (factor.isZero()) continue;
    const row = t.T[i];
    for (let j = 0; j < cols; j++) {
      const coeff = j === e ? ONE : newRow[j];
      row[j] = row[j].sub(factor.mul(coeff));
    }
    t.rhs[i] = t.rhs[i].sub(factor.mul(newRhs));
  }

  // Substitute x_e into the objective row.
  const ze = t.z[e];
  if (!ze.isZero()) {
    for (let j = 0; j < cols; j++) {
      const coeff = j === e ? ONE : newRow[j];
      t.z[j] = t.z[j].sub(ze.mul(coeff));
    }
    t.z0 = t.z0.add(ze.mul(newRhs));
  }

  t.T[l] = newRow;
  t.rhs[l] = newRhs;
  t.B[l] = e;
}

type LoopOutcome =
  | { kind: "optimal" }
  | { kind: "unbounded"; entering: number }
  | { kind: "budget" };

/** Primal simplex iterations with Bland's rule. Requires a feasible dictionary. */
function bland(t: Tableau, budget: Budget): LoopOutcome {
  for (;;) {
    // Entering: smallest index with positive reduced cost.
    let e = -1;
    for (let j = 0; j < t.cols; j++) {
      if (t.z[j].sign() > 0) {
        e = j;
        break;
      }
    }
    if (e < 0) return { kind: "optimal" };

    // Leaving: minimum ratio; ties broken by smallest basic variable index.
    let l = -1;
    let best: Frac = ZERO;
    for (let i = 0; i < t.rows; i++) {
      const a = t.T[i][e];
      if (a.sign() > 0) {
        const ratio = t.rhs[i].div(a);
        if (
          l < 0 ||
          ratio.cmp(best) < 0 ||
          (ratio.cmp(best) === 0 && t.B[i] < t.B[l])
        ) {
          best = ratio;
          l = i;
        }
      }
    }
    if (l < 0) return { kind: "unbounded", entering: e };

    if (!spend(budget)) return { kind: "budget" };
    pivot(t, l, e);
  }
}

function extractX(t: Tableau, n: number): Frac[] {
  const x = new Array<Frac>(n).fill(ZERO);
  for (let i = 0; i < t.rows; i++) {
    if (t.B[i] >= 0 && t.B[i] < n) x[t.B[i]] = t.rhs[i];
  }
  return x;
}

export function solve(
  lp: LP,
  maxPivots: number = Number.MAX_SAFE_INTEGER
): SolveResult {
  const m = lp.A.length;
  const n = lp.c.length;
  const N0 = n + m; // variables: n structural + m slack
  const budget: Budget = { left: maxPivots, used: 0 };

  const t = new Tableau(m, N0);
  for (let i = 0; i < m; i++) {
    for (let j = 0; j < n; j++) t.T[i][j] = lp.A[i][j];
    t.T[i][n + i] = ONE;
    t.rhs[i] = lp.b[i];
    t.B[i] = n + i;
  }
  // rowOf[i] = original constraint index of tableau row i (rows may be dropped).
  const rowOf: number[] = [];
  for (let i = 0; i < m; i++) rowOf.push(i);

  // ---- Phase 1: find a feasible dictionary via the auxiliary problem ----
  // maximize -x_art  s.t.  A x - x_art + s = b.
  let l = -1;
  for (let i = 0; i < m; i++) {
    if (t.rhs[i].sign() < 0 && (l < 0 || t.rhs[i].cmp(t.rhs[l]) < 0)) l = i;
  }

  if (l >= 0) {
    const art = N0;
    for (let i = 0; i < m; i++) t.T[i].push(NEG_ONE);
    t.z.push(NEG_ONE);
    t.cols = N0 + 1;

    // Pivot the artificial variable into the most-violated row; the
    // dictionary then becomes feasible for the auxiliary objective.
    if (!spend(budget)) return { status: "budget_exhausted", pivots: budget.used };
    pivot(t, l, art);

    const out = bland(t, budget);
    if (out.kind === "budget")
      return { status: "budget_exhausted", pivots: budget.used };
    // The auxiliary problem is bounded above by 0, so "unbounded" cannot occur.

    if (t.z0.sign() < 0) {
      // Infeasible. Farkas certificate: y_i = -(reduced cost of slack i)
      // gives y >= 0, Aᵀy >= 0, b·y = z0 < 0.
      const y: Frac[] = [];
      for (let i = 0; i < m; i++) y.push(t.z[n + i].neg());
      return { status: "infeasible", pivots: budget.used, y };
    }

    // Drive the artificial variable out of the basis (value is 0, so any
    // nonzero pivot in its row preserves feasibility); if its row vanished
    // entirely the constraint is redundant and can be dropped.
    for (let i = 0; i < t.rows; i++) {
      if (t.B[i] !== art) continue;
      let e = -1;
      for (let j = 0; j < N0; j++) {
        if (!t.T[i][j].isZero()) {
          e = j;
          break;
        }
      }
      if (e >= 0) {
        if (!spend(budget))
          return { status: "budget_exhausted", pivots: budget.used };
        pivot(t, i, e);
      } else {
        t.T.splice(i, 1);
        t.rhs.splice(i, 1);
        t.B.splice(i, 1);
        rowOf.splice(i, 1);
        t.rows--;
        i--;
      }
    }

    // Remove the artificial column (it is nonbasic now, and last).
    for (let i = 0; i < t.rows; i++) t.T[i].pop();
    t.z.pop();
    t.cols = N0;
  }

  // ---- Restore the original objective in terms of nonbasic variables ----
  for (let j = 0; j < N0; j++) t.z[j] = j < n ? lp.c[j] : ZERO;
  t.z0 = ZERO;
  for (let i = 0; i < t.rows; i++) {
    const bv = t.B[i];
    if (bv < n && !lp.c[bv].isZero()) {
      const cb = lp.c[bv];
      t.z0 = t.z0.add(cb.mul(t.rhs[i]));
      for (let j = 0; j < N0; j++) t.z[j] = t.z[j].sub(cb.mul(t.T[i][j]));
      t.z[bv] = ZERO;
    }
  }

  // ---- Phase 2 ----
  const out = bland(t, budget);
  if (out.kind === "budget")
    return { status: "budget_exhausted", pivots: budget.used };

  if (out.kind === "unbounded") {
    const e = out.entering;
    const x = extractX(t, n);
    const d = new Array<Frac>(n).fill(ZERO);
    if (e < n) d[e] = ONE;
    for (let i = 0; i < t.rows; i++) {
      if (t.B[i] >= 0 && t.B[i] < n) d[t.B[i]] = t.T[i][e].neg();
    }
    return { status: "unbounded", pivots: budget.used, x, d };
  }

  const x = extractX(t, n);
  const y = new Array<Frac>(m).fill(ZERO);
  for (let i = 0; i < t.rows; i++) y[rowOf[i]] = t.z[n + rowOf[i]].neg();
  return { status: "optimal", pivots: budget.used, value: t.z0, x, y };
}
