/**
 * Exact square-matrix algebra over the rationals.
 *
 * Determinant: fraction-free Bareiss algorithm (a division-free-in-the-
 * coefficients variant of Gaussian elimination; every intermediate stays
 * exact, no floating point anywhere).
 *
 * Inverse: adjugate (classical adjoint) method — inv(A) = adj(A) / det(A),
 * where each cofactor is itself an exact Bareiss determinant of a minor.
 * Singularity is decided exactly: det(A) === 0 (as a rational) iff A is
 * singular, and inversion then refuses with a precise error.
 */

import { Rational, r } from './rational.ts';

export type RationalLike = bigint | number | string | Rational;

export class Matrix {
  readonly size: number;
  /** Row-major cells, all exact rationals. */
  readonly a: Rational[][];

  constructor(entries: RationalLike[][]) {
    const n = entries.length;
    if (n === 0) throw new RangeError('Matrix must be non-empty');
    for (const row of entries) {
      if (row.length !== n) throw new RangeError('Matrix must be square');
    }
    this.size = n;
    this.a = entries.map((row) => row.map((v) => r(v)));
  }

  static identity(n: number): Matrix {
    const rows: Rational[][] = [];
    for (let i = 0; i < n; i++) {
      const row: Rational[] = [];
      for (let j = 0; j < n; j++) row.push(i === j ? Rational.ONE : Rational.ZERO);
      rows.push(row);
    }
    return new Matrix(rows);
  }

  get(i: number, j: number): Rational {
    return this.a[i][j];
  }

  equals(other: Matrix): boolean {
    if (this.size !== other.size) return false;
    for (let i = 0; i < this.size; i++) {
      for (let j = 0; j < this.size; j++) {
        if (!this.a[i][j].equals(other.a[i][j])) return false;
      }
    }
    return true;
  }

  multiply(other: Matrix): Matrix {
    if (this.size !== other.size) throw new RangeError('Matrix size mismatch');
    const n = this.size;
    const out: Rational[][] = [];
    for (let i = 0; i < n; i++) {
      const row: Rational[] = [];
      for (let j = 0; j < n; j++) {
        let sum = Rational.ZERO;
        for (let k = 0; k < n; k++) {
          const term = this.a[i][k].mul(other.a[k][j]);
          if (!term.isZero()) sum = sum.add(term);
        }
        row.push(sum);
      }
      out.push(row);
    }
    return new Matrix(out);
  }

  /**
   * Exact determinant via the fraction-free Bareiss algorithm.
   *
   * Bareiss performs Gaussian elimination where the pivot update
   *   m[i][j] <- (m[i][j] * pivot - m[i][k] * m[k][j]) / prevPivot
   * is always exact (Sylvester's identity), so no rounding ever occurs.
   * Row swaps only flip the sign. Singularity is detected exactly when a
   * whole pivot column below the diagonal is zero.
   */
  determinant(): Rational {
    const n = this.size;
    const m: Rational[][] = this.a.map((row) => row.slice());
    let sign = 1;
    let prevPivot = Rational.ONE;

    for (let k = 0; k < n - 1; k++) {
      if (m[k][k].isZero()) {
        let swap = -1;
        for (let i = k + 1; i < n; i++) {
          if (!m[i][k].isZero()) {
            swap = i;
            break;
          }
        }
        if (swap === -1) return Rational.ZERO; // exact singularity
        const tmp = m[k];
        m[k] = m[swap];
        m[swap] = tmp;
        sign = -sign;
      }
      const pivot = m[k][k];
      for (let i = k + 1; i < n; i++) {
        for (let j = k + 1; j < n; j++) {
          const num = m[i][j].mul(pivot).sub(m[i][k].mul(m[k][j]));
          m[i][j] = prevPivot.isOne() ? num : num.div(prevPivot);
        }
      }
      prevPivot = pivot;
      for (let i = k + 1; i < n; i++) m[i][k] = Rational.ZERO;
    }

    const det = m[n - 1][n - 1];
    return sign === 1 ? det : det.neg();
  }

  /** Exact (i, j)-minor: the matrix with row i and column j removed. */
  private minor(rowSkip: number, colSkip: number): Matrix {
    const rows: Rational[][] = [];
    for (let i = 0; i < this.size; i++) {
      if (i === rowSkip) continue;
      const row: Rational[] = [];
      for (let j = 0; j < this.size; j++) {
        if (j === colSkip) continue;
        row.push(this.a[i][j]);
      }
      rows.push(row);
    }
    return new Matrix(rows);
  }

  /** Classical adjoint (transpose of the cofactor matrix), computed exactly. */
  adjugate(): Matrix {
    const n = this.size;
    if (n === 1) return Matrix.identity(1);
    const rows: Rational[][] = [];
    for (let i = 0; i < n; i++) {
      const row: Rational[] = [];
      for (let j = 0; j < n; j++) {
        // adj(A)[i][j] = C[j][i] = (-1)^(i+j) * det(minor(j, i))
        const cofactor = this.minor(j, i).determinant();
        row.push((i + j) % 2 === 0 ? cofactor : cofactor.neg());
      }
      rows.push(row);
    }
    return new Matrix(rows);
  }

  /**
   * Exact inverse: inv(A) = adj(A) / det(A).
   * Throws on exact singularity (det === 0 as a rational).
   */
  inverse(): Matrix {
    const det = this.determinant();
    if (det.isZero()) {
      throw new RangeError('Matrix is singular: determinant is exactly zero');
    }
    const adj = this.adjugate();
    const rows: Rational[][] = adj.a.map((row) => row.map((v) => v.div(det)));
    return new Matrix(rows);
  }

  /** True iff det(A) is exactly zero. */
  isSingular(): boolean {
    return this.determinant().isZero();
  }

  toString(): string {
    return this.a.map((row) => `[ ${row.map((v) => v.toString()).join(', ')} ]`).join('\n');
  }
}

/** The n-th order Hilbert matrix H[i][j] = 1 / (i + j + 1), exactly. */
export function hilbert(n: number): Matrix {
  const rows: Rational[][] = [];
  for (let i = 0; i < n; i++) {
    const row: Rational[] = [];
    for (let j = 0; j < n; j++) row.push(Rational.make(1n, BigInt(i + j + 1)));
    rows.push(row);
  }
  return new Matrix(rows);
}
