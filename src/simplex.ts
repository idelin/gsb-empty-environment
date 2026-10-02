/**
 * Two-phase primal simplex method over exact rational arithmetic.
 *
 * Model (all variables are implicitly >= 0):
 *
 *   max|min  c^T x
 *   s.t.     A_i x {<=, >=, =} b_i   for each constraint i
 *
 * Every tableau entry is a `Fraction` (BigInt numerator/denominator), so the
 * basis exchanges performed during iteration are exact: no floating-point
 * rounding can ever change a pivot choice or the final optimum.
 *
 * Bland's rule (lowest-index entering / leaving variable) is used for both
 * phases, which guarantees termination even on degenerate problems.
 *
 * Results include:
 *   - the exact optimal primal solution and objective value,
 *   - exact dual (shadow price) values y per constraint (y = c_B^T B^{-1},
 *     sign-adjusted to the original objective direction),
 *   - an unboundedness ray certificate, or
 *   - a Farkas infeasibility certificate.
 */

import { Fraction, type FractionInput } from "./fraction.ts";

export type ConstraintSense = "<=" | ">=" | "=";

export interface LPConstraint {
  coefficients: FractionInput[];
  sense: ConstraintSense;
  rhs: FractionInput;
}

export interface LPProblem {
  objective: "max" | "min";
  /** Objective coefficients c, one per variable. */
  coefficients: FractionInput[];
  constraints: LPConstraint[];
}

export type LPStatus = "optimal" | "unbounded" | "infeasible";

export interface LPSolution {
  status: LPStatus;
  /** Number of pivots performed across both phases. */
  iterations: number;
  /** Exact optimal objective value (only when status === "optimal"). */
  objectiveValue?: Fraction;
  /** Exact primal solution x for the original variables. */
  primal?: Fraction[];
  /**
   * Exact dual values y = c_B^T B^{-1}, one per constraint.
   * For a maximization problem: y_i >= 0 on "<=" rows, y_i <= 0 on ">="
   * rows, y_i free on "=" rows, and y^T b equals the optimal objective
   * (strong duality), so y certifies optimality.
   */
  duals?: Fraction[];
  /**
   * Unboundedness certificate: a feasible point `unboundedPoint` and a
   * recession direction `unboundedRay` (over the original variables) with
   * A d <= 0 row-wise (resp. = 0 / >= 0 per sense), d >= 0 and c^T d > 0,
   * so x(t) = point + t * d stays feasible while the objective grows.
   */
  unboundedPoint?: Fraction[];
  unboundedRay?: Fraction[];
  /**
   * Farkas infeasibility certificate y (one entry per constraint):
   * y_i >= 0 on "<=" rows, y_i <= 0 on ">=" rows, y^T A >= 0
   * componentwise, yet y^T b < 0 — a contradiction, proving no feasible
   * x >= 0 exists.
   */
  farkasCertificate?: Fraction[];
}

const ZERO = Fraction.ZERO;

function zeroRow(length: number): Fraction[] {
  return Array.from({ length }, () => ZERO);
}

export function solveLP(problem: LPProblem): LPSolution {
  const numVars = problem.coefficients.length;
  const numRows = problem.constraints.length;
  if (numVars === 0) throw new Error("solveLP: problem has no variables");
  const maximize = problem.objective !== "min";
  const objectiveSign = maximize ? 1n : -1n;
  const objective = Fraction.array(problem.coefficients).map(
    (c) => new Fraction(c.num * objectiveSign, c.den),
  );

  // ---------------------------------------------------------------------
  // Build the equality tableau. Columns: original variables, then slack /
  // surplus / artificial columns appended per constraint. The initial basis
  // is always an identity matrix (slack or artificial columns), so `bInv`
  // starts as the identity and tracks B^{-1} through every pivot.
  // ---------------------------------------------------------------------
  const A: Fraction[][] = [];
  const b: Fraction[] = [];
  const basis: number[] = [];
  const artificial: boolean[] = Array.from({ length: numVars }, () => false);
  let numCols = numVars;

  const addColumn = (isArtificial: boolean): number => {
    for (const row of A) row.push(ZERO);
    artificial.push(isArtificial);
    return numCols++;
  };

  for (let i = 0; i < numRows; i++) {
    const constraint = problem.constraints[i];
    if (constraint.coefficients.length !== numVars) {
      throw new Error(`solveLP: constraint ${i} has wrong coefficient count`);
    }
    const row = Fraction.array(constraint.coefficients);
    while (row.length < numCols) row.push(ZERO);
    let rhs = Fraction.from(constraint.rhs);
    let sense = constraint.sense;
    if (rhs.isNegative()) {
      // Normalize to a non-negative right-hand side.
      for (let j = 0; j < row.length; j++) row[j] = row[j].neg();
      rhs = rhs.neg();
      sense = sense === "<=" ? ">=" : sense === ">=" ? "<=" : "=";
    }
    A.push(row);
    b.push(rhs);
    if (sense === "<=") {
      const slack = addColumn(false);
      row[slack] = Fraction.ONE;
      basis.push(slack);
    } else if (sense === ">=") {
      const surplus = addColumn(false);
      row[surplus] = Fraction.ONE.neg();
      const aux = addColumn(true);
      row[aux] = Fraction.ONE;
      basis.push(aux);
    } else {
      const aux = addColumn(true);
      row[aux] = Fraction.ONE;
      basis.push(aux);
    }
  }

  let bInv: Fraction[][] = Array.from({ length: numRows }, (_, i) =>
    Array.from({ length: numRows }, (_, j) => (i === j ? Fraction.ONE : ZERO)),
  );

  // Reduced costs for the maximization form: reduced[j] = c_j - z_j.
  // Optimal when every (enterable) reduced cost is <= 0.
  let reduced: Fraction[] = zeroRow(numCols);
  let objectiveValue = ZERO;
  let iterations = 0;

  const setCosts = (costs: Fraction[]): void => {
    const next = zeroRow(numCols);
    let value = ZERO;
    for (let j = 0; j < numCols; j++) {
      let zj = ZERO;
      for (let i = 0; i < numRows; i++) {
        const cb = costs[basis[i]];
        if (!cb.isZero()) zj = zj.add(cb.mul(A[i][j]));
      }
      next[j] = costs[j].sub(zj);
    }
    for (let i = 0; i < numRows; i++) {
      const cb = costs[basis[i]];
      if (!cb.isZero()) value = value.add(cb.mul(b[i]));
    }
    reduced = next;
    objectiveValue = value;
  };

  /** Gauss-Jordan pivot on (pivotRow, pivotCol); exact rational arithmetic. */
  const pivot = (pivotRow: number, pivotCol: number): void => {
    const pivotValue = A[pivotRow][pivotCol];
    if (!pivotValue.equals(Fraction.ONE)) {
      for (let j = 0; j < numCols; j++) {
        A[pivotRow][j] = A[pivotRow][j].div(pivotValue);
      }
      for (let i = 0; i < numRows; i++) {
        bInv[pivotRow][i] = bInv[pivotRow][i].div(pivotValue);
      }
      b[pivotRow] = b[pivotRow].div(pivotValue);
    }
    for (let i = 0; i < numRows; i++) {
      if (i === pivotRow) continue;
      const factor = A[i][pivotCol];
      if (factor.isZero()) continue;
      for (let j = 0; j < numCols; j++) {
        A[i][j] = A[i][j].sub(factor.mul(A[pivotRow][j]));
      }
      for (let k = 0; k < numRows; k++) {
        bInv[i][k] = bInv[i][k].sub(factor.mul(bInv[pivotRow][k]));
      }
      b[i] = b[i].sub(factor.mul(b[pivotRow]));
    }
    const enteringReduced = reduced[pivotCol];
    if (!enteringReduced.isZero()) {
      for (let j = 0; j < numCols; j++) {
        reduced[j] = reduced[j].sub(enteringReduced.mul(A[pivotRow][j]));
      }
      objectiveValue = objectiveValue.add(enteringReduced.mul(b[pivotRow]));
    }
    basis[pivotRow] = pivotCol;
    iterations++;
  };

  /**
   * Primal simplex with Bland's rule. Returns "optimal", or "unbounded"
   * together with the entering column that proves it.
   */
  const runSimplex = (
    forbidArtificialEntering: boolean,
  ): { status: "optimal" } | { status: "unbounded"; entering: number } => {
    for (;;) {
      let entering = -1;
      for (let j = 0; j < numCols; j++) {
        if (forbidArtificialEntering && artificial[j]) continue;
        if (reduced[j].isPositive()) {
          entering = j; // Bland: lowest index with positive reduced cost
          break;
        }
      }
      if (entering === -1) return { status: "optimal" };

      let leaving = -1;
      let bestRatio = ZERO;
      for (let i = 0; i < numRows; i++) {
        const coeff = A[i][entering];
        if (!coeff.isPositive()) continue;
        const ratio = b[i].div(coeff);
        if (
          leaving === -1 ||
          ratio.compare(bestRatio) < 0 ||
          (ratio.equals(bestRatio) && basis[i] < basis[leaving]) // Bland tie-break
        ) {
          leaving = i;
          bestRatio = ratio;
        }
      }
      if (leaving === -1) return { status: "unbounded", entering };
      pivot(leaving, entering);
    }
  };

  const currentPrimal = (): Fraction[] => {
    const x = zeroRow(numVars);
    for (let i = 0; i < numRows; i++) {
      if (basis[i] < numVars) x[basis[i]] = b[i];
    }
    return x;
  };

  /** y^T = c_B^T B^{-1} using the given per-column cost vector. */
  const currentDuals = (costs: Fraction[]): Fraction[] => {
    const y = zeroRow(numRows);
    for (let i = 0; i < numRows; i++) {
      const cb = costs[basis[i]];
      if (cb.isZero()) continue;
      for (let k = 0; k < numRows; k++) {
        y[k] = y[k].add(cb.mul(bInv[i][k]));
      }
    }
    return y;
  };

  // ---------------------------------------------------------------------
  // Phase 1: maximize -(sum of artificials). Drives the artificials to zero
  // or proves infeasibility (Farkas certificate from the phase-1 duals).
  // ---------------------------------------------------------------------
  const hasArtificials = artificial.some(Boolean);
  if (hasArtificials) {
    const phase1Costs = zeroRow(numCols).map((_, j) =>
      artificial[j] ? Fraction.ONE.neg() : ZERO,
    );
    setCosts(phase1Costs);
    runSimplex(false);
    if (objectiveValue.isNegative()) {
      return {
        status: "infeasible",
        iterations,
        farkasCertificate: currentDuals(phase1Costs),
      };
    }
  }

  // ---------------------------------------------------------------------
  // Phase 2: the real objective. Artificial columns may stay in the basis
  // at value zero (degenerate redundant rows) but may never re-enter.
  // ---------------------------------------------------------------------
  const phase2Costs = zeroRow(numCols);
  for (let j = 0; j < numVars; j++) phase2Costs[j] = objective[j];
  setCosts(phase2Costs);
  const outcome = runSimplex(true);

  if (outcome.status === "unbounded") {
    const entering = outcome.entering;
    const ray = zeroRow(numVars);
    if (entering < numVars) ray[entering] = Fraction.ONE;
    for (let i = 0; i < numRows; i++) {
      if (basis[i] < numVars) ray[basis[i]] = A[i][entering].neg();
    }
    return {
      status: "unbounded",
      iterations,
      unboundedPoint: currentPrimal(),
      unboundedRay: ray,
    };
  }

  const rawDuals = currentDuals(phase2Costs);
  const duals = maximize ? rawDuals : rawDuals.map((y) => y.neg());
  const finalObjective = maximize
    ? objectiveValue
    : objectiveValue.neg();
  return {
    status: "optimal",
    iterations,
    objectiveValue: finalObjective,
    primal: currentPrimal(),
    duals,
  };
}
