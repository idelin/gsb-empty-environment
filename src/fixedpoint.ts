/**
 * Exact fixed-point primitives backed by BigInt.
 *
 * All values are integers in Q(F) format (value / 2^F) unless noted.
 * Every routine is a pure function of its arguments, which is what makes
 * block-partitioned processing bit-identical to one-shot processing.
 */

/** Round-to-nearest (ties away from zero) division of two bigints. */
export function divRound(numerator: bigint, denominator: bigint): bigint {
  if (denominator === 0n) throw new RangeError("divRound: division by zero");
  const negative = (numerator < 0n) !== (denominator < 0n);
  const a = numerator < 0n ? -numerator : numerator;
  const b = denominator < 0n ? -denominator : denominator;
  const q = a / b;
  const r = a - q * b;
  const rounded = 2n * r >= b ? q + 1n : q;
  return negative ? -rounded : rounded;
}

/** Ceiling division for non-negative `a` and positive `b`. */
export function ceilDiv(a: bigint, b: bigint): bigint {
  if (a < 0n || b <= 0n) throw new RangeError("ceilDiv: expects a >= 0, b > 0");
  return a === 0n ? 0n : (a - 1n) / b + 1n;
}

function bitLength(n: bigint): bigint {
  let bits = 0n;
  let v = n;
  while (v > 0n) {
    v >>= 1n;
    bits += 1n;
  }
  return bits;
}

/** Floor of the integer square root (Newton's method). */
export function isqrt(n: bigint): bigint {
  if (n < 0n) throw new RangeError("isqrt: negative input");
  if (n < 2n) return n;
  // Initial guess: 2^ceil(bitlen/2), an overestimate, so Newton converges downward.
  let x = 1n << ((bitLength(n) + 1n) >> 1n);
  let y = (x + n / x) >> 1n;
  while (y < x) {
    x = y;
    y = (x + n / x) >> 1n;
  }
  return x;
}
