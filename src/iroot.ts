/**
 * Integer nth root (floor) over BigInt, with a certified stopping rule and a
 * provable termination argument. Nothing here ever stops because "two
 * consecutive iterates became equal"; the loop exits only when the candidate
 * is *certified* to be the exact floor root (x^k <= n, while the loop
 * invariant guarantees x >= true root).
 *
 * Termination proof (see README.md for the full write-up):
 *   Let r = floor(n^(1/k)), b = bitLength(n).
 *   x0 = 2^ceil(b/k)  =>  x0^k = 2^(k*ceil(b/k)) >= 2^b > n  =>  x0 > r.
 *   Invariant: x >= r. Newton step x' = ((k-1)x + floor(n/x^(k-1)))/k.
 *     (a) x' >= r: by AM-GM ((k-1)x + n/x^(k-1))/k >= n^(1/k) >= r, and the
 *         outer floor keeps an integer >= r because r is an integer.
 *     (b) While x > r: x^k >= (r+1)^k > n, so n/x^(k-1) < x, hence
 *         x' <= ((k-1)x + x - 1)/k < x. The integer sequence strictly
 *         decreases and is bounded below by r >= 0, so it terminates.
 *   Certified stop: when x^k <= n is observed, invariant (x >= r) plus
 *   x^k <= n < (r+1)^k forces x == r exactly.
 *
 * Sharper bound (used for the defensive limit below): writing d = x - r,
 * one step gives d' <= floor((k-1)d/k) (for k = 2: d' <= floor(d/2)), so the
 * excess shrinks geometrically and the loop needs O(b) iterations in the
 * worst case. `limit` is a generous, provably sufficient hard cap; exceeding
 * it would indicate an implementation bug, not bad luck.
 */
export interface IRootResult {
  /** floor(n^(1/k)) */
  root: bigint;
  /** n - root^k (exact remainder, 0 iff n is a perfect kth power) */
  remainder: bigint;
  /** true iff n is a perfect kth power */
  exact: boolean;
  /** number of certified-check iterations actually used */
  iterations: number;
}

export function bitLength(x: bigint): number {
  if (x <= 0n) throw new RangeError('bitLength: expected a positive integer');
  return x.toString(2).length;
}

/** base^exp for a small non-negative integer exponent. */
export function powSmall(base: bigint, exp: number): bigint {
  if (!Number.isInteger(exp) || exp < 0) {
    throw new RangeError('powSmall: exponent must be a non-negative integer');
  }
  let result = 1n;
  let b = base;
  let e = exp;
  while (e > 0) {
    if (e & 1) result *= b;
    b *= b;
    e >>= 1;
  }
  return result;
}

/** floor(sqrt(n)) — dedicated fast path for k = 2. */
export function isqrt(n: bigint): IRootResult {
  if (n < 0n) throw new RangeError('isqrt: negative input rejected');
  if (n < 2n) return { root: n, remainder: 0n, exact: true, iterations: 0 };
  const b = bitLength(n);
  let x = 1n << BigInt((b + 1) >> 1); // 2^ceil(b/2), strictly above the root
  const limit = 2 * b + 16; // provably sufficient: excess halves each step
  let iterations = 0;
  for (;;) {
    iterations += 1;
    if (iterations > limit) throw new Error('isqrt: provable iteration bound violated');
    const sq = x * x;
    if (sq <= n) {
      // certified: invariant x >= r and x^2 <= n < (r+1)^2 imply x == r
      return { root: x, remainder: n - sq, exact: sq === n, iterations };
    }
    x = (x + n / x) >> 1n;
  }
}

/** floor(n^(1/k)) — generic Newton path (any k >= 1). */
export function irootGeneric(k: number, n: bigint): IRootResult {
  if (!Number.isInteger(k) || k < 1) {
    throw new RangeError('iroot: degree k must be an integer >= 1');
  }
  if (n < 0n) throw new RangeError('iroot: negative input rejected');
  if (k === 1) return { root: n, remainder: 0n, exact: true, iterations: 0 };
  if (n < 2n) return { root: n, remainder: 0n, exact: true, iterations: 0 };
  const b = bitLength(n);
  const kk = BigInt(k);
  let x = 1n << BigInt(Math.ceil(b / k)); // strictly above the root
  const limit = 2 * b + 4 * k + 16;
  let iterations = 0;
  for (;;) {
    iterations += 1;
    if (iterations > limit) throw new Error('iroot: provable iteration bound violated');
    const xk = powSmall(x, k);
    if (xk <= n) {
      return { root: x, remainder: n - xk, exact: xk === n, iterations };
    }
    x = ((kk - 1n) * x + n / powSmall(x, k - 1)) / kk;
  }
}

/** floor(n^(1/k)); dispatches k = 2 to the dedicated isqrt fast path. */
export function iroot(k: number, n: bigint): IRootResult {
  if (k === 2) return isqrt(n);
  return irootGeneric(k, n);
}
