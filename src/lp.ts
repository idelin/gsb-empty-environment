import { Fraction } from './fraction.ts';

export type Sense = '<=' | '>=' | '=';

export interface LPConstraint {
  coeffs: Fraction[];
  sense: Sense;
  rhs: Fraction;
}

export type LPStatus = 'optimal' | 'infeasible' | 'unbounded';

export interface LPResult {
  status: LPStatus;
  /** Optimal primal solution (length numVars); empty unless status === 'optimal'. */
  x: Fraction[];
  /** Optimal objective value; null unless status === 'optimal'. */
  objective: Fraction | null;
}

function flipSense(sense: Sense): Sense {
  if (sense === '<=') return '>=';
  if (sense === '>=') return '<=';
  return '=';
}

function pivot(A: Fraction[][], b: Fraction[], basis: number[], row: number, col: number): void {
  const piv = A[row][col];
  const cols = A[row].length;
  for (let j = 0; j < cols; j++) A[row][j] = A[row][j].div(piv);
  b[row] = b[row].div(piv);
  for (let i = 0; i < A.length; i++) {
    if (i === row) continue;
    const f = A[i][col];
    if (f.isZero()) continue;
    for (let j = 0; j < cols; j++) A[i][j] = A[i][j].sub(f.mul(A[row][j]));
    b[i] = b[i].sub(f.mul(b[row]));
  }
  basis[row] = col;
}

/**
 * Primal simplex with Bland's rule (guaranteed termination) on a system
 * A x = b, x >= 0, maximizing c^T x. Assumes b >= 0 and basis is feasible.
 */
function simplex(A: Fraction[][], b: Fraction[], basis: number[], c: Fraction[]): 'optimal' | 'unbounded' {
  const m = A.length;
  const n = c.length;
  for (;;) {
    const cB = basis.map((j) => c[j]);
    let enter = -1;
    for (let j = 0; j < n; j++) {
      let reduced = c[j];
      for (let i = 0; i < m; i++) {
        if (!cB[i].isZero() && !A[i][j].isZero()) reduced = reduced.sub(cB[i].mul(A[i][j]));
      }
      if (reduced.sign() > 0) {
        enter = j;
        break; // Bland: smallest index with positive reduced cost
      }
    }
    if (enter === -1) return 'optimal';
    let leave = -1;
    let bestRatio: Fraction | null = null;
    for (let i = 0; i < m; i++) {
      const a = A[i][enter];
      if (a.sign() <= 0) continue;
      const ratio = b[i].div(a);
      if (
        bestRatio === null ||
        ratio.cmp(bestRatio) < 0 ||
        (ratio.cmp(bestRatio) === 0 && basis[i] < basis[leave])
      ) {
        bestRatio = ratio;
        leave = i;
      }
    }
    if (leave === -1) return 'unbounded';
    pivot(A, b, basis, leave, enter);
  }
}

function currentObjective(basis: number[], b: Fraction[], c: Fraction[]): Fraction {
  let acc = Fraction.ZERO;
  for (let i = 0; i < basis.length; i++) acc = acc.add(c[basis[i]].mul(b[i]));
  return acc;
}

/**
 * Solve  max/min  objective^T x  subject to the given constraints over the
 * rationals, exactly. Variables are free unless constrained; non-negativity
 * is NOT assumed — callers should split free variables if needed.
 *
 * Internally converts to standard form with slack/surplus/artificial
 * variables and runs the two-phase simplex method with exact Fraction
 * arithmetic, so results are exact rational numbers.
 */
export function solveLP(
  numVars: number,
  objective: Fraction[],
  constraints: LPConstraint[],
  maximize: boolean = true,
): LPResult {
  if (objective.length !== numVars) throw new Error('solveLP: objective dimension mismatch');
  for (const cons of constraints) {
    if (cons.coeffs.length !== numVars) throw new Error('solveLP: constraint dimension mismatch');
  }

  // Normalize: rhs >= 0.
  const norm = constraints.map((cons) => {
    let { coeffs, sense, rhs } = cons;
    if (rhs.sign() < 0) {
      coeffs = coeffs.map((c) => c.neg());
      rhs = rhs.neg();
      sense = flipSense(sense);
    }
    return { coeffs, sense, rhs };
  });

  const m = norm.length;
  const numSlack = norm.filter((c) => c.sense !== '=').length;
  const numArtificial = norm.filter((c) => c.sense !== '<=').length;
  const totalCols = numVars + numSlack + numArtificial;

  const A: Fraction[][] = [];
  const b: Fraction[] = [];
  const basis: number[] = [];
  const isArtificial: boolean[] = new Array(totalCols).fill(false);

  let slackIdx = numVars;
  let artIdx = numVars + numSlack;
  for (const cons of norm) {
    const row: Fraction[] = new Array(totalCols).fill(Fraction.ZERO);
    for (let j = 0; j < numVars; j++) row[j] = cons.coeffs[j];
    if (cons.sense === '<=') {
      row[slackIdx] = Fraction.ONE;
      basis.push(slackIdx);
      slackIdx++;
    } else if (cons.sense === '>=') {
      row[slackIdx] = Fraction.ONE.neg();
      row[artIdx] = Fraction.ONE;
      isArtificial[artIdx] = true;
      basis.push(artIdx);
      slackIdx++;
      artIdx++;
    } else {
      row[artIdx] = Fraction.ONE;
      isArtificial[artIdx] = true;
      basis.push(artIdx);
      artIdx++;
    }
    A.push(row);
    b.push(cons.rhs);
  }

  // Phase 1: minimize sum of artificials == maximize their negated sum.
  if (numArtificial > 0) {
    const c1: Fraction[] = new Array(totalCols).fill(Fraction.ZERO);
    for (let j = 0; j < totalCols; j++) if (isArtificial[j]) c1[j] = Fraction.ONE.neg();
    simplex(A, b, basis, c1);
    if (currentObjective(basis, b, c1).sign() < 0) {
      return { status: 'infeasible', x: [], objective: null };
    }
    // Drive remaining basic artificials (at zero level) out of the basis.
    for (let i = 0; i < m; i++) {
      if (!isArtificial[basis[i]]) continue;
      let pivoted = false;
      for (let j = 0; j < totalCols; j++) {
        if (isArtificial[j]) continue;
        if (!A[i][j].isZero()) {
          pivot(A, b, basis, i, j);
          pivoted = true;
          break;
        }
      }
      if (!pivoted) {
        // Redundant constraint row: drop it.
        A.splice(i, 1);
        b.splice(i, 1);
        basis.splice(i, 1);
        i--;
      }
    }
  }

  // Remove artificial columns entirely.
  let keep: number[] = [];
  for (let j = 0; j < totalCols; j++) if (!isArtificial[j]) keep.push(j);
  const remap = new Map<number, number>();
  keep.forEach((old, idx) => remap.set(old, idx));
  const A2 = A.map((row) => keep.map((j) => row[j]));
  const basis2 = basis.map((j) => remap.get(j) as number);
  const keptCols = keep.length;

  // Phase 2: optimize the real objective.
  const sign = maximize ? 1 : -1;
  const c2: Fraction[] = new Array(keptCols).fill(Fraction.ZERO);
  for (let j = 0; j < numVars; j++) {
    c2[j] = sign === 1 ? objective[j] : objective[j].neg();
  }
  const status = simplex(A2, b, basis2, c2);
  if (status === 'unbounded') return { status: 'unbounded', x: [], objective: null };

  const x: Fraction[] = new Array(numVars).fill(Fraction.ZERO);
  for (let i = 0; i < basis2.length; i++) {
    if (basis2[i] < numVars) x[basis2[i]] = b[i];
  }
  let obj = currentObjective(basis2, b, c2);
  if (sign === -1) obj = obj.neg();
  return { status: 'optimal', x, objective: obj };
}
