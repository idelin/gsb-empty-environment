/**
 * Exact integer roots on BigInt.
 *
 * All arithmetic is exact BigInt arithmetic; no Number conversion is ever
 * applied to the values being rooted (Number is only used for loop caps and
 * bit-length bookkeeping, never for the payload).
 */

export interface IntegerRootResult {
  /** floor(x^(1/k)) */
  readonly root: bigint;
  /** exact remainder x - root^k, always >= 0 */
  readonly remainder: bigint;
  /** Newton iterations actually performed (termination audit) */
  readonly iterations: number;
}

/** Number of significant bits of a non-negative integer. */
export function bitLength(x: bigint): number {
  if (x < 0n) throw new RangeError("bitLength: negative input");
  return x === 0n ? 0 : x.toString(2).length;
}

/** Exact integer power, exponentiation by squaring. */
export function pow(base: bigint, exp: number): bigint {
  if (!Number.isInteger(exp) || exp < 0) {
    throw new RangeError("pow: exponent must be a non-negative integer");
  }
  let result = 1n;
  let b = base;
  let e = exp;
  while (e > 0) {
    if (e % 2 === 1) result *= b;
    e = Math.floor(e / 2);
    if (e > 0) b *= b;
  }
  return result;
}

/**
 * floor(sqrt(x)) for x >= 0, by integer Newton iteration.
 *
 * Iteration:  y_{i+1} = floor((y_i + x / y_i) / 2),  y_0 = 2^ceil(bitlen(x)/2).
 *
 * TERMINATION PROOF (does NOT rely on consecutive iterates being equal):
 *   Let s = floor(sqrt(x)). Note y_0 > sqrt(x) since x < 2^bitlen(x).
 *   (1) Lower bound invariant: for any y > 0, AM-GM gives
 *       (y + x/y)/2 >= sqrt(x), hence y_{i+1} >= floor(sqrt(x)) = s.
 *       So every iterate from index 1 on satisfies y_i >= s.
 *   (2) Strict decrease while y_i > s: y_i > s implies y_i >= s + 1, hence
 *       y_i^2 >= (s+1)^2 > x, so x / y_i <= y_i - 1 (integer division),
 *       and y_{i+1} <= floor((y_i + y_i - 1)/2) = y_i - 1 < y_i.
 *   (3) A strictly decreasing sequence of integers bounded below by s
 *       reaches the exit condition in at most y_0 - s steps. QED.
 *   Exit condition: stop when y_{i+1} >= y_i (non-decrease), NOT when
 *   y_{i+1} == y_i. Equality alone is insufficient: for x = k^2 - 1
 *   (e.g. 3, 8, 15, 24, ...) the orbit alternates s, s+1, s, s+1, ...
 *   without ever hitting a fixed point. At exit:
 *       y_{i+1} >= y_i  <=>  x / y_i >= y_i  <=>  y_i^2 <= x
 *       <=>  y_i <= sqrt(x)  <=>  y_i <= s,
 *   which combined with invariant (1) yields y_i = s exactly.
 *
 * The defensive cap `bitLength(x) + 8` is far above the proven bound
 * (convergence is quadratic once y_i < 2s, so O(log bitlen) iterations
 * suffice); exceeding it indicates an implementation bug, not bad luck.
 */
export function isqrtRem(x: bigint): IntegerRootResult {
  if (x < 0n) throw new RangeError("isqrt: negative input rejected");
  if (x < 2n) return { root: x, remainder: 0n, iterations: 0 };
  const bits = bitLength(x);
  let y = 1n << BigInt(Math.ceil(bits / 2));
  const cap = bits + 8;
  let iterations = 0;
  for (;;) {
    if (iterations > cap) {
      throw new Error("isqrt: iteration cap exceeded (proof violated)");
    }
    const next = (y + x / y) >> 1n;
    iterations += 1;
    if (next >= y) break; // stop on non-decrease, never on equality alone
    y = next;
  }
  return { root: y, remainder: x - y * y, iterations };
}

/** floor(sqrt(x)) for x >= 0. */
export function isqrt(x: bigint): bigint {
  return isqrtRem(x).root;
}

/**
 * floor(x^(1/k)) for x >= 0 and integer degree k >= 2.
 *
 * Iteration:  y_{i+1} = floor(((k-1) * y_i + x / y_i^(k-1)) / k),
 *             y_0 = 2^ceil(bitlen(x)/k).
 *
 * TERMINATION PROOF (same structure as isqrt, no equality-based stop):
 *   Let s = floor(x^(1/k)).
 *   (1) Lower bound invariant: weighted AM-GM gives
 *       ((k-1) y + x / y^(k-1)) / k >= (y^(k-1) * x / y^(k-1))^(1/k) = x^(1/k),
 *       hence y_{i+1} >= floor(x^(1/k)) = s for every y > 0.
 *   (2) Strict decrease while y_i > s: y_i > s implies y_i^k > x, hence
 *       x / y_i^(k-1) <= y_i - 1 (integer division), so
 *       y_{i+1} <= floor(((k-1) y_i + y_i - 1) / k) = y_i - 1 < y_i.
 *   (3) Strictly decreasing integers bounded below by s terminate.
 *   Exit when y_{i+1} >= y_i, i.e. x / y_i^(k-1) >= y_i, i.e. y_i^k <= x,
 *   i.e. y_i <= s; with invariant (1) this gives y_i = s exactly.
 */
export function irootRem(x: bigint, degree: number): IntegerRootResult {
  if (x < 0n) throw new RangeError("iroot: negative input rejected");
  if (!Number.isInteger(degree) || degree < 2) {
    throw new RangeError("iroot: degree must be an integer >= 2");
  }
  if (x < 2n) return { root: x, remainder: 0n, iterations: 0 };
  const bits = bitLength(x);
  const k = BigInt(degree);
  let y = 1n << BigInt(Math.ceil(bits / degree));
  const cap = bits + 8;
  let iterations = 0;
  for (;;) {
    if (iterations > cap) {
      throw new Error("iroot: iteration cap exceeded (proof violated)");
    }
    const next = ((k - 1n) * y + x / pow(y, degree - 1)) / k;
    iterations += 1;
    if (next >= y) break; // stop on non-decrease, never on equality alone
    y = next;
  }
  return { root: y, remainder: x - pow(y, degree), iterations };
}

/** floor(x^(1/k)) for x >= 0, integer degree k >= 2. */
export function iroot(x: bigint, degree: number): bigint {
  return irootRem(x, degree).root;
}
