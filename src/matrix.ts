// Exact rational matrix algebra and an exact PSD (positive semidefinite)
// test. The PSD check runs an LDL^T factorization with symmetric pivoting
// over exact rationals; on failure it returns a witness vector v with
// v^T S v < 0 ("negative-direction evidence"), computed exactly.

import { Rational, type RationalInput } from './rational.ts';

export type Matrix = Rational[][];

export function toMatrix(rows: RationalInput[][]): Matrix {
  return rows.map((row) => row.map((x) => Rational.of(x)));
}

export function zeros(r: number, c: number): Matrix {
  return Array.from({ length: r }, () =>
    Array.from({ length: c }, () => Rational.zero),
  );
}

export function basisVec(n: number, i: number): Rational[] {
  return Array.from({ length: n }, (_, j) => (j === i ? Rational.one : Rational.zero));
}

export function assertSquare(A: Matrix, name = 'matrix'): void {
  for (const row of A) {
    if (row.length !== A.length) {
      throw new Error(`${name} must be square (got ${A.length}x${row.length})`);
    }
  }
}

export function isSymmetric(A: Matrix): boolean {
  for (let i = 0; i < A.length; i++) {
    for (let j = i + 1; j < A.length; j++) {
      if (A[i][j].cmp(A[j][i]) !== 0) return false;
    }
  }
  return true;
}

export function transpose(A: Matrix): Matrix {
  const n = A.length;
  const m = A[0]?.length ?? 0;
  const T = zeros(m, n);
  for (let i = 0; i < n; i++) {
    for (let j = 0; j < m; j++) T[j][i] = A[i][j];
  }
  return T;
}

export function matMul(A: Matrix, B: Matrix): Matrix {
  const n = A.length;
  const k = A[0]?.length ?? 0;
  const m = B[0]?.length ?? 0;
  if (B.length !== k) throw new Error(`matMul: dimension mismatch ${n}x${k} * ${B.length}x${m}`);
  const C = zeros(n, m);
  for (let i = 0; i < n; i++) {
    for (let j = 0; j < m; j++) {
      let acc = Rational.zero;
      for (let t = 0; t < k; t++) acc = acc.add(A[i][t].mul(B[t][j]));
      C[i][j] = acc;
    }
  }
  return C;
}

export function matVec(A: Matrix, v: Rational[]): Rational[] {
  return A.map((row) => {
    if (row.length !== v.length) throw new Error('matVec: dimension mismatch');
    let acc = Rational.zero;
    for (let j = 0; j < v.length; j++) acc = acc.add(row[j].mul(v[j]));
    return acc;
  });
}

// v^T S v, exact.
export function quadForm(v: Rational[], S: Matrix): Rational {
  let acc = Rational.zero;
  for (let i = 0; i < v.length; i++) {
    if (v[i].isZero()) continue;
    for (let j = 0; j < v.length; j++) {
      if (v[j].isZero() || S[i][j].isZero()) continue;
      acc = acc.add(v[i].mul(S[i][j]).mul(v[j]));
    }
  }
  return acc;
}

export interface PsdFailure {
  reason: 'negative-pivot' | 'indefinite-zero-diagonal';
  // Witness direction in the ORIGINAL coordinate order, exact rationals.
  vector: Rational[];
  // Exact value of vector^T S vector, guaranteed < 0.
  quadraticForm: Rational;
}

export type PsdResult =
  | { psd: true; rank: number }
  | { psd: false; rank: number; failure: PsdFailure };

// Exact PSD test for a symmetric rational matrix via LDL^T with symmetric
// pivoting on the Schur complement. Zero-variance dimensions are fine as
// long as their whole row/column is zero; a zero diagonal coupled to a
// nonzero off-diagonal is indefinite and reported with a witness.
export function checkPSD(input: Matrix): PsdResult {
  assertSquare(input, 'covariance');
  const n = input.length;
  // Sp: permuted original (for witness lifting); M: running Schur complement.
  const Sp: Matrix = input.map((row) => row.slice());
  const M: Matrix = input.map((row) => row.slice());
  const L: Matrix = zeros(n, n);
  const d: Rational[] = [];
  const perm: number[] = Array.from({ length: n }, (_, i) => i);
  let rank = 0;

  const swapIdx = (i: number, j: number): void => {
    if (i === j) return;
    for (const A of [Sp, M]) {
      [A[i], A[j]] = [A[j], A[i]];
      for (let r = 0; r < n; r++) [A[r][i], A[r][j]] = [A[r][j], A[r][i]];
    }
    [L[i], L[j]] = [L[j], L[i]];
    [perm[i], perm[j]] = [perm[j], perm[i]];
  };

  const fail = (
    reason: PsdFailure['reason'],
    w: Rational[],
    k: number,
  ): { psd: false; rank: number; failure: PsdFailure } => {
    // w has support on indices >= k (permuted coordinates) and certifies
    // w^T C w < 0 for the current Schur complement C. Lift it to a vector
    // v~ = [-A^{-1} B w; w] against the leading k x k block A = L1 D1 L1^T
    // of Sp, so that v~^T Sp v~ = w^T C w < 0, then unpermute.
    let v: Rational[];
    if (k === 0) {
      v = w;
    } else {
      const wrem = w.slice(k);
      const bw: Rational[] = [];
      for (let i = 0; i < k; i++) {
        let acc = Rational.zero;
        for (let j = 0; j < n - k; j++) acc = acc.add(Sp[i][k + j].mul(wrem[j]));
        bw.push(acc);
      }
      // Solve L1 D1 L1^T z = bw.
      const f: Rational[] = [];
      for (let i = 0; i < k; i++) {
        let acc = bw[i];
        for (let j = 0; j < i; j++) acc = acc.sub(L[i][j].mul(f[j]));
        f.push(acc);
      }
      const g = f.map((fi, i) => fi.div(d[i]));
      const z: Rational[] = new Array(k).fill(Rational.zero);
      for (let i = k - 1; i >= 0; i--) {
        let acc = g[i];
        for (let j = i + 1; j < k; j++) acc = acc.sub(L[j][i].mul(z[j]));
        z[i] = acc;
      }
      v = z.map((zi) => zi.neg()).concat(wrem);
    }
    const vOrig: Rational[] = new Array(n).fill(Rational.zero);
    for (let i = 0; i < n; i++) vOrig[perm[i]] = v[i];
    const qf = quadForm(vOrig, input);
    return { psd: false, rank, failure: { reason, vector: vOrig, quadraticForm: qf } };
  };

  for (let k = 0; k < n; k++) {
    let pos = -1;
    let neg = -1;
    for (let j = k; j < n; j++) {
      const s = M[j][j].sign();
      if (s > 0 && pos < 0) pos = j;
      else if (s < 0 && neg < 0) neg = j;
    }
    if (pos < 0) {
      if (neg >= 0) {
        return fail('negative-pivot', basisVec(n, neg), k);
      }
      // All remaining diagonals are zero: any nonzero off-diagonal makes
      // the block indefinite; otherwise the remaining block is exactly zero.
      let ji = -1;
      let mi = -1;
      outer: for (let j = k; j < n; j++) {
        for (let m = j + 1; m < n; m++) {
          if (!M[j][m].isZero()) {
            ji = j;
            mi = m;
            break outer;
          }
        }
      }
      if (ji < 0) return { psd: true, rank };
      // w = e_ji - sign(M[ji][mi]) e_mi  =>  w^T M w = -2 |M[ji][mi]| < 0
      const w = basisVec(n, ji);
      w[mi] = M[ji][mi].sign() > 0 ? Rational.one.neg() : Rational.one;
      return fail('indefinite-zero-diagonal', w, k);
    }
    swapIdx(k, pos);
    const dk = M[k][k];
    d.push(dk);
    L[k][k] = Rational.one;
    for (let j = k + 1; j < n; j++) L[j][k] = M[j][k].div(dk);
    for (let j = k + 1; j < n; j++) {
      for (let m = j; m < n; m++) {
        const v = M[j][m].sub(L[j][k].mul(L[m][k]).mul(dk));
        M[j][m] = v;
        M[m][j] = v;
      }
    }
    rank++;
  }
  return { psd: true, rank };
}
