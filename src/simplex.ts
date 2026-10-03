/**
 * Exact two-phase simplex method over the rationals.
 *
 * Solves:  maximize  c^T x  subject to  A x (<=, >=, =) b,  x >= 0.
 *
 * All pivot operations are performed with exact Rational arithmetic
 * (bigint numerator/denominator), so results are globally exact.
 * Bland's rule is used for entering/leaving variable selection,
 * which guarantees termination (no cycling).
 */
import { Rational } from './rational.ts';

export type Sense = '<=' | '>=' | '=';

export interface ConstraintInput {
  coeffs: Rational[];
  sense: Sense;
  rhs: Rational;
}

export interface LPInput {
  /** Objective coefficients of the maximization problem. */
  objective: Rational[];
  constraints: ConstraintInput[];
}

export interface PivotStep {
  phase: 1 | 2;
  iteration: number;
  entering: number;
  leaving: number;
  enteringName: string;
  leavingName: string;
  pivot: Rational;
  basis: number[];
  basisNames: string[];
}

export type SimplexResult =
  | {
      status: 'optimal';
      objective: Rational;
      x: Rational[];
      duals: Rational[];
      steps: PivotStep[];
      variableNames: string[];
    }
  | {
      status: 'unbounded';
      point: Rational[];
      ray: Rational[];
      steps: PivotStep[];
      variableNames: string[];
    }
  | {
      status: 'infeasible';
      farkas: Rational[];
      steps: PivotStep[];
      variableNames: string[];
    };

interface NormalizedConstraint {
  coeffs: Rational[];
  sense: Sense;
  rhs: Rational;
  flipped: boolean;
}

function flipSense(sense: Sense): Sense {
  if (sense === '<=') return '>=';
  if (sense === '>=') return '<=';
  return '=';
}

export function solve(lp: LPInput): SimplexResult {
  const n = lp.objective.length;
  const m0 = lp.constraints.length;

  // Normalize so every rhs is nonnegative (flipping the sense if needed).
  const cons: NormalizedConstraint[] = lp.constraints.map((c) => {
    if (c.coeffs.length !== n) throw new Error('constraint width mismatch');
    if (c.rhs.sign() < 0) {
      return {
        coeffs: c.coeffs.map((a) => a.neg()),
        sense: flipSense(c.sense),
        rhs: c.rhs.neg(),
        flipped: true,
      };
    }
    return { coeffs: c.coeffs.slice(), sense: c.sense, rhs: c.rhs, flipped: false };
  });

  // Column layout: original variables, then slack/surplus, then artificials.
  const names: string[] = [];
  for (let j = 0; j < n; j++) names.push(`x${j + 1}`);
  const slackCol: number[] = new Array(m0).fill(-1);
  const artCol: number[] = new Array(m0).fill(-1);
  const isArtificial: boolean[] = [];
  for (let j = 0; j < n; j++) isArtificial.push(false);
  cons.forEach((c, i) => {
    if (c.sense !== '=') {
      slackCol[i] = names.length;
      names.push(`s${i + 1}`);
      isArtificial.push(false);
    }
    if (c.sense !== '<=') {
      artCol[i] = names.length;
      names.push(`a${i + 1}`);
      isArtificial.push(true);
    }
  });
  const totalCols = names.length;

  // Tableau: one row per constraint, last column is the rhs.
  let tab: Rational[][] = cons.map((c, i) => {
    const row: Rational[] = new Array(totalCols + 1).fill(null).map(() => Rational.zero());
    for (let j = 0; j < n; j++) row[j] = c.coeffs[j];
    if (slackCol[i] !== -1) row[slackCol[i]] = c.sense === '<=' ? Rational.one() : Rational.one().neg();
    if (artCol[i] !== -1) row[artCol[i]] = Rational.one();
    row[totalCols] = c.rhs;
    return row;
  });

  let basis: number[] = cons.map((_, i) => (artCol[i] !== -1 ? artCol[i] : slackCol[i]));
  // Columns of the initial basis (identity): their current contents give B^-1.
  const initBasisCols: number[] = basis.slice();
  // Maps a tableau row to its original constraint index (rows may be dropped).
  let rowCons: number[] = cons.map((_, i) => i);

  const steps: PivotStep[] = [];
  let iteration = 0;

  const doPivot = (r: number, c: number): void => {
    const piv = tab[r][c];
    tab[r] = tab[r].map((v) => v.div(piv));
    for (let i = 0; i < tab.length; i++) {
      if (i === r) continue;
      const factor = tab[i][c];
      if (factor.isZero()) continue;
      for (let j = 0; j <= totalCols; j++) {
        tab[i][j] = tab[i][j].sub(factor.mul(tab[r][j]));
      }
    }
  };

  const reducedCost = (costs: Rational[], j: number): Rational => {
    let rc = costs[j];
    for (let i = 0; i < tab.length; i++) {
      const cb = costs[basis[i]];
      if (!cb.isZero()) rc = rc.sub(cb.mul(tab[i][j]));
    }
    return rc;
  };

  /** Runs the simplex loop; returns 'optimal' or the unbounded entering column. */
  const runPhase = (costs: Rational[], phase: 1 | 2): number | 'optimal' => {
    for (;;) {
      // Bland's rule: lowest-index column with positive reduced cost.
      let enter = -1;
      for (let j = 0; j < totalCols; j++) {
        if (phase === 2 && isArtificial[j]) continue;
        if (reducedCost(costs, j).sign() > 0) {
          enter = j;
          break;
        }
      }
      if (enter === -1) return 'optimal';

      // Minimum ratio test; ties broken by lowest-index basic variable (Bland).
      let leaveRow = -1;
      let bestRatio: Rational | null = null;
      for (let i = 0; i < tab.length; i++) {
        const a = tab[i][enter];
        if (a.sign() > 0) {
          const ratio = tab[i][totalCols].div(a);
          if (
            bestRatio === null ||
            ratio.cmp(bestRatio) < 0 ||
            (ratio.cmp(bestRatio) === 0 && basis[i] < basis[leaveRow])
          ) {
            bestRatio = ratio;
            leaveRow = i;
          }
        }
      }
      if (leaveRow === -1) return enter;

      const leaving = basis[leaveRow];
      const pivotValue = tab[leaveRow][enter];
      doPivot(leaveRow, enter);
      basis[leaveRow] = enter;
      iteration++;
      steps.push({
        phase,
        iteration,
        entering: enter,
        leaving,
        enteringName: names[enter],
        leavingName: names[leaving],
        pivot: pivotValue,
        basis: basis.slice(),
        basisNames: basis.map((b) => names[b]),
      });
    }
  };

  const dualValues = (costs: Rational[]): Rational[] => {
    // y = c_B * B^-1, read off the columns of the initial identity basis.
    const y: Rational[] = new Array(m0).fill(null).map(() => Rational.zero());
    for (let k = 0; k < tab.length; k++) {
      const constraint = rowCons[k];
      let acc = Rational.zero();
      for (let i = 0; i < tab.length; i++) {
        const cb = costs[basis[i]];
        if (!cb.isZero()) acc = acc.add(cb.mul(tab[i][initBasisCols[constraint]]));
      }
      y[constraint] = acc;
    }
    return y;
  };

  const unflip = (y: Rational[]): Rational[] =>
    y.map((v, i) => (cons[i].flipped ? v.neg() : v));

  const currentPrimal = (): Rational[] => {
    const x: Rational[] = new Array(n).fill(null).map(() => Rational.zero());
    for (let i = 0; i < tab.length; i++) {
      if (basis[i] < n) x[basis[i]] = tab[i][totalCols];
    }
    return x;
  };

  // ---- Phase 1: maximize -(sum of artificials) ----
  const needsPhase1 = artCol.some((c) => c !== -1);
  if (needsPhase1) {
    const costs1: Rational[] = names.map((_, j) =>
      isArtificial[j] ? Rational.one().neg() : Rational.zero(),
    );
    runPhase(costs1, 1);
    let phase1Value = Rational.zero();
    for (let i = 0; i < tab.length; i++) {
      phase1Value = phase1Value.add(costs1[basis[i]].mul(tab[i][totalCols]));
    }
    if (phase1Value.sign() < 0) {
      return {
        status: 'infeasible',
        farkas: unflip(dualValues(costs1)),
        steps,
        variableNames: names,
      };
    }
    // Drive remaining artificial basics out; drop redundant rows.
    for (let i = tab.length - 1; i >= 0; i--) {
      if (!isArtificial[basis[i]]) continue;
      let pivotCol = -1;
      for (let j = 0; j < totalCols; j++) {
        if (!isArtificial[j] && !tab[i][j].isZero()) {
          pivotCol = j;
          break;
        }
      }
      if (pivotCol !== -1) {
        doPivot(i, pivotCol);
        basis[i] = pivotCol;
      } else {
        tab.splice(i, 1);
        basis.splice(i, 1);
        rowCons.splice(i, 1);
      }
    }
  }

  // ---- Phase 2: the real objective ----
  const costs2: Rational[] = names.map((_, j) => (j < n ? lp.objective[j] : Rational.zero()));
  const outcome = runPhase(costs2, 2);

  if (outcome !== 'optimal') {
    const enter = outcome;
    const ray: Rational[] = new Array(totalCols).fill(null).map(() => Rational.zero());
    ray[enter] = Rational.one();
    for (let i = 0; i < tab.length; i++) {
      ray[basis[i]] = tab[i][enter].neg();
    }
    return {
      status: 'unbounded',
      point: currentPrimal(),
      ray: ray.slice(0, n),
      steps,
      variableNames: names,
    };
  }

  const x = currentPrimal();
  let objective = Rational.zero();
  for (let j = 0; j < n; j++) objective = objective.add(lp.objective[j].mul(x[j]));

  return {
    status: 'optimal',
    objective,
    x,
    duals: unflip(dualValues(costs2)),
    steps,
    variableNames: names,
  };
}
