/**
 * Resultants over Q[x][y] via the Sylvester matrix and the fraction-free
 * Bareiss determinant algorithm. Used to build annihilating polynomials
 * for sums, products and radicals of algebraic numbers — all exact.
 */
import { Rational } from './rational.ts';
import { Poly } from './poly.ts';

function ydeg(cs: Poly[]): number {
  let d = cs.length - 1;
  while (d >= 0 && cs[d].isZero()) d--;
  return d;
}

/** exact determinant of a matrix of polynomials (Bareiss fraction-free) */
function bareissDet(mat: Poly[][]): Poly {
  const n = mat.length;
  if (n === 0) return Poly.one();
  const a = mat.map((row) => row.slice());
  let sign = 1;
  let prev = Poly.one();
  for (let k = 0; k < n - 1; k++) {
    if (a[k][k].isZero()) {
      let piv = -1;
      for (let i = k + 1; i < n; i++) {
        if (!a[i][k].isZero()) {
          piv = i;
          break;
        }
      }
      if (piv === -1) return Poly.zero();
      const t = a[k];
      a[k] = a[piv];
      a[piv] = t;
      sign = -sign;
    }
    const pivot = a[k][k];
    for (let i = k + 1; i < n; i++) {
      for (let j = k + 1; j < n; j++) {
        a[i][j] = pivot.mul(a[i][j]).sub(a[i][k].mul(a[k][j])).div(prev);
      }
    }
    prev = pivot;
  }
  const d = a[n - 1][n - 1];
  return sign === 1 ? d : d.neg();
}

/**
 * Resultant with respect to y of two polynomials in y whose
 * coefficients are polynomials in x. Returns a polynomial in x.
 */
export function resultantY(p: Poly[], q: Poly[]): Poly {
  const m = ydeg(p);
  const n = ydeg(q);
  if (m < 0 || n < 0) return Poly.zero();
  const s = m + n;
  if (s === 0) return Poly.one();
  const zero = Poly.zero();
  const mat: Poly[][] = [];
  for (let k = 0; k < n; k++) {
    const row: Poly[] = new Array(s).fill(zero);
    for (let j = 0; j <= m; j++) row[k + j] = p[m - j];
    mat.push(row);
  }
  for (let k = 0; k < m; k++) {
    const row: Poly[] = new Array(s).fill(zero);
    for (let j = 0; j <= n; j++) row[k + j] = q[n - j];
    mat.push(row);
  }
  return bareissDet(mat);
}

function binomial(n: number, k: number): bigint {
  let r = 1n;
  for (let i = 0; i < k; i++) r = (r * BigInt(n - i)) / BigInt(i + 1);
  return r;
}

const asConstPoly = (c: Rational): Poly => Poly.constant(c);

/**
 * Polynomial vanishing at every r_i + s_j where p(r_i) = 0, q(s_j) = 0.
 * Computed as Res_y( p(y), q(x - y) ).
 */
export function polyAdd(p: Poly, q: Poly): Poly {
  if (p.isZero() || q.isZero()) throw new Error('polyAdd: zero polynomial');
  const n = q.deg;
  // q(x - y) = sum_j s_j y^j with  s_j = (-1)^j * sum_{k>=j} q_k * C(k,j) * x^(k-j)
  const s: Poly[] = [];
  for (let j = 0; j <= n; j++) {
    let acc = Poly.zero();
    for (let k = j; k <= n; k++) {
      const c = q.coeffs[k].mul(Rational.make(binomial(k, j), 1n));
      acc = acc.add(asConstPoly(c).shift(k - j));
    }
    if (j % 2 === 1) acc = acc.neg();
    s.push(acc);
  }
  return resultantY(p.coeffs.map(asConstPoly), s);
}

/**
 * Polynomial vanishing at every r_i * s_j where p(r_i) = 0, q(s_j) = 0.
 * Computed as Res_y( p(y), y^deg(q) * q(x / y) ).
 */
export function polyMul(p: Poly, q: Poly): Poly {
  if (p.isZero() || q.isZero()) throw new Error('polyMul: zero polynomial');
  const n = q.deg;
  // y^n q(x/y) = sum_j s_j y^j with  s_j = q_{n-j} * x^(n-j)
  const s: Poly[] = [];
  for (let j = 0; j <= n; j++) {
    s.push(asConstPoly(q.coeffs[n - j]).shift(n - j));
  }
  return resultantY(p.coeffs.map(asConstPoly), s);
}
