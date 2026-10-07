/**
 * Exact linear algebra over the rationals.
 *
 * All routines are based on fraction-free-free (plain BigInt-backed) Gauss-Jordan
 * elimination on `Rational` entries, so results are exact: no floating point is
 * involved anywhere.
 */

import { Rational } from "./rational.ts";

export type Matrix = Rational[][];
export type Vector = Rational[];

export function zeroMatrix(rows: number, cols: number): Matrix {
  return Array.from({ length: rows }, () =>
    Array.from({ length: cols }, () => Rational.zero()),
  );
}

export function identityMatrix(n: number): Matrix {
  const m = zeroMatrix(n, n);
  for (let i = 0; i < n; i++) m[i][i] = Rational.one();
  return m;
}

export function matVecMul(a: Matrix, x: Vector): Vector {
  return a.map((row) => {
    let acc = Rational.zero();
    for (let j = 0; j < row.length; j++) {
      if (!row[j].isZero() && !x[j].isZero()) acc = acc.add(row[j].mul(x[j]));
    }
    return acc;
  });
}

export function vecSub(a: Vector, b: Vector): Vector {
  return a.map((v, i) => v.sub(b[i]));
}

export function vecIsZero(a: Vector): boolean {
  return a.every((v) => v.isZero());
}

export interface RrefResult {
  /** Row-reduced echelon form of the input. */
  rref: Matrix;
  /** Pivot column index per non-zero row. */
  pivotCols: number[];
  /**
   * The invertible transform `t` with `rref = t * input`
   * (only computed when `trackTransform` is set).
   */
  transform?: Matrix;
}

/**
 * Gauss-Jordan elimination to reduced row echelon form, exact.
 * When `trackTransform` is true, an identity is augmented on the right so row
 * operations are recorded, yielding `transform` with `rref = transform * a`.
 */
export function rref(
  a: Matrix,
  trackTransform = false,
  pivotColLimit?: number,
): RrefResult {
  const rows = a.length;
  const cols = rows === 0 ? 0 : a[0].length;
  const pivotLimit = pivotColLimit ?? cols;
  const m: Matrix = a.map((row) => row.slice());
  const t: Matrix | undefined = trackTransform ? identityMatrix(rows) : undefined;
  const pivotCols: number[] = [];

  let r = 0;
  for (let c = 0; c < pivotLimit && r < rows; c++) {
    let pivot = -1;
    for (let i = r; i < rows; i++) {
      if (!m[i][c].isZero()) {
        pivot = i;
        break;
      }
    }
    if (pivot === -1) continue;
    if (pivot !== r) {
      [m[r], m[pivot]] = [m[pivot], m[r]];
      if (t) [t[r], t[pivot]] = [t[pivot], t[r]];
    }
    const inv = Rational.one().div(m[r][c]);
    for (let j = c; j < cols; j++) m[r][j] = m[r][j].mul(inv);
    if (t) for (let j = 0; j < rows; j++) t[r][j] = t[r][j].mul(inv);
    for (let i = 0; i < rows; i++) {
      if (i === r) continue;
      const f = m[i][c];
      if (f.isZero()) continue;
      for (let j = c; j < cols; j++) m[i][j] = m[i][j].sub(f.mul(m[r][j]));
      if (t) for (let j = 0; j < rows; j++) t[i][j] = t[i][j].sub(f.mul(t[r][j]));
    }
    pivotCols.push(c);
    r++;
  }
  return { rref: m, pivotCols, transform: t };
}

export interface LinearSolution {
  consistent: boolean;
  /** One particular solution when consistent. */
  particular: Vector | null;
  /**
   * Basis of the homogeneous solution space `{ v : A v = 0 }`, as a list of
   * column vectors. Empty when the solution is unique.
   */
  nullspace: Matrix;
  /**
   * When inconsistent: multipliers `y` (one per row of `A`) with
   * `y^T A = 0` but `y^T b != 0` — an exact Farkas-style certificate that the
   * system has no solution.
   */
  certificate: Vector | null;
  /** Value of `y^T b` for the certificate (non-zero). */
  certificateRhs: Rational | null;
}

/**
 * Solves `A x = b` exactly.
 *
 * Elimination runs on `[A | b | I]`; if a row reduces to `0 ... 0 | c` with
 * `c != 0`, the recorded identity part of that row is precisely a vector `y`
 * with `y^T A = 0` and `y^T b = c != 0`.
 */
export function solveLinear(a: Matrix, b: Vector): LinearSolution {
  const rows = a.length;
  const cols = rows === 0 ? 0 : a[0].length;
  const augmented: Matrix = a.map((row, i) => [...row, b[i]]);
  // Never pivot on the appended b column: a row reducing to 0...0 | c with
  // c != 0 is exactly an inconsistency, and its transform row is the certificate.
  const { rref: m, pivotCols, transform } = rref(augmented, true, cols);
  const t = transform!;

  for (let i = pivotCols.length; i < rows; i++) {
    if (!m[i][cols].isZero()) {
      return {
        consistent: false,
        particular: null,
        nullspace: [],
        certificate: t[i].slice(),
        certificateRhs: m[i][cols],
      };
    }
  }

  const pivotSet = new Set(pivotCols);
  const freeCols: number[] = [];
  for (let c = 0; c < cols; c++) if (!pivotSet.has(c)) freeCols.push(c);

  const particular: Vector = Array.from({ length: cols }, () => Rational.zero());
  for (let i = 0; i < pivotCols.length; i++) particular[pivotCols[i]] = m[i][cols];

  const nullspace: Matrix = freeCols.map((freeCol) => {
    const v: Vector = Array.from({ length: cols }, () => Rational.zero());
    v[freeCol] = Rational.one();
    for (let i = 0; i < pivotCols.length; i++) v[pivotCols[i]] = m[i][freeCol].neg();
    return v;
  });

  return { consistent: true, particular, nullspace, certificate: null, certificateRhs: null };
}

/** Exact nullspace basis of a matrix: columns `v` with `A v = 0`. */
export function nullspaceOf(a: Matrix): Matrix {
  const cols = a.length === 0 ? 0 : a[0].length;
  return solveLinear(a, Array.from({ length: a.length }, () => Rational.zero())).nullspace.map(
    (v) => v.slice(0, cols),
  );
}

/** Matrix-vector product `A^T y`. */
export function matTVecMul(a: Matrix, y: Vector): Vector {
  const cols = a.length === 0 ? 0 : a[0].length;
  const out: Vector = Array.from({ length: cols }, () => Rational.zero());
  for (let j = 0; j < cols; j++) {
    let acc = Rational.zero();
    for (let i = 0; i < a.length; i++) {
      if (!a[i][j].isZero() && !y[i].isZero()) acc = acc.add(a[i][j].mul(y[i]));
    }
    out[j] = acc;
  }
  return out;
}
