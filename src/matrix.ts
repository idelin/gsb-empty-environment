/**
 * Exact linear algebra over the rationals: Gauss-Jordan elimination with
 * row-combination tracking, used for solving M x = c, computing nullspace
 * bases, and extracting Farkas-style inconsistency certificates.
 */

import { Rational } from './rational.ts';

export type SolveResult =
  | { consistent: true; particular: Rational[]; nullspace: Rational[][] }
  | { consistent: false; certificate: Rational[]; contradictionValue: Rational };

function zeros(n: number): Rational[] {
  return Array.from({ length: n }, () => Rational.ZERO);
}

/**
 * Solve M x = c exactly.
 *
 * On consistency returns a particular solution and a basis of null(M).
 * On inconsistency returns a certificate vector y (one entry per row of M)
 * with y·M = 0 but y·c = contradictionValue != 0, i.e. a linear combination
 * of the equations yielding 0 = nonzero.
 */
export function solveLinearSystem(rows: Rational[][], rhs: Rational[]): SolveResult {
  const m = rows.length;
  const n = m > 0 ? rows[0].length : 0;
  if (rhs.length !== m) throw new Error('solveLinearSystem: rhs length mismatch');
  for (const r of rows) {
    if (r.length !== n) throw new Error('solveLinearSystem: ragged matrix');
  }

  // Augmented matrix [M | c | E], E starts as identity and tracks the row
  // operations so an inconsistent row exposes its coefficient combination.
  const width = n + 1 + m;
  const aug: Rational[][] = rows.map((r, i) => {
    const line = new Array<Rational>(width).fill(Rational.ZERO);
    for (let j = 0; j < n; j++) line[j] = r[j];
    line[n] = rhs[i];
    line[n + 1 + i] = Rational.ONE;
    return line;
  });

  const pivotCols: number[] = [];
  let rank = 0;
  for (let col = 0; col < n && rank < m; col++) {
    let piv = -1;
    for (let i = rank; i < m; i++) {
      if (!aug[i][col].isZero()) {
        piv = i;
        break;
      }
    }
    if (piv < 0) continue;
    if (piv !== rank) {
      const tmp = aug[piv];
      aug[piv] = aug[rank];
      aug[rank] = tmp;
    }
    const pivVal = aug[rank][col];
    const pivRow = aug[rank];
    for (let i = 0; i < m; i++) {
      if (i === rank) continue;
      const head = aug[i][col];
      if (head.isZero()) continue;
      const factor = head.div(pivVal);
      const row = aug[i];
      for (let j = 0; j < width; j++) {
        row[j] = row[j].sub(factor.mul(pivRow[j]));
      }
    }
    pivotCols.push(col);
    rank++;
  }

  for (let i = rank; i < m; i++) {
    if (!aug[i][n].isZero()) {
      return {
        consistent: false,
        certificate: aug[i].slice(n + 1),
        contradictionValue: aug[i][n],
      };
    }
  }

  const isPivot = new Array<boolean>(n).fill(false);
  for (const c of pivotCols) isPivot[c] = true;

  const particular = zeros(n);
  for (let i = 0; i < rank; i++) {
    particular[pivotCols[i]] = aug[i][n].div(aug[i][pivotCols[i]]);
  }

  const nullspace: Rational[][] = [];
  for (let free = 0; free < n; free++) {
    if (isPivot[free]) continue;
    const v = zeros(n);
    v[free] = Rational.ONE;
    for (let i = 0; i < rank; i++) {
      const entry = aug[i][free];
      if (!entry.isZero()) {
        v[pivotCols[i]] = entry.div(aug[i][pivotCols[i]]).neg();
      }
    }
    nullspace.push(v);
  }

  return { consistent: true, particular, nullspace };
}

/** Basis of null(M) = { x : M x = 0 }. */
export function nullspaceOf(rows: Rational[][]): Rational[][] {
  const result = solveLinearSystem(rows, zeros(rows.length));
  if (!result.consistent) throw new Error('nullspaceOf: unreachable');
  return result.nullspace;
}

export function matVecMul(M: Rational[][], x: Rational[]): Rational[] {
  return M.map((row) => {
    let acc = Rational.ZERO;
    for (let j = 0; j < row.length; j++) {
      if (!row[j].isZero() && !x[j].isZero()) acc = acc.add(row[j].mul(x[j]));
    }
    return acc;
  });
}

export function dot(a: Rational[], b: Rational[]): Rational {
  let acc = Rational.ZERO;
  for (let i = 0; i < a.length; i++) {
    if (!a[i].isZero() && !b[i].isZero()) acc = acc.add(a[i].mul(b[i]));
  }
  return acc;
}

export function transpose(M: Rational[][]): Rational[][] {
  const m = M.length;
  const n = m > 0 ? M[0].length : 0;
  const T: Rational[][] = Array.from({ length: n }, () => zeros(m));
  for (let i = 0; i < m; i++) {
    for (let j = 0; j < n; j++) T[j][i] = M[i][j];
  }
  return T;
}

/** Infinity norm (max absolute entry) of a rational vector. */
export function maxAbs(v: Rational[]): Rational {
  let acc = Rational.ZERO;
  for (const x of v) acc = Rational.max(acc, x.abs());
  return acc;
}
