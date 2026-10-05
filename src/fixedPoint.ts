/**
 * Fixed-point kernel for the gain interpolation library.
 *
 * All arithmetic is exact integer math on `bigint`, so results are fully
 * deterministic and independent of how samples are partitioned into blocks.
 * Values are conceptually Q-format fixed-point numbers: a real value x is
 * stored as round(x * 2^fracBits).
 */

/** Rounding division: round(numerator / denominator), ties away from zero. */
export function roundDiv(numerator: bigint, denominator: bigint): bigint {
  if (denominator <= 0n) {
    throw new Error("roundDiv: denominator must be positive");
  }
  if (numerator >= 0n) {
    return (2n * numerator + denominator) / (2n * denominator);
  }
  return -((-2n * numerator + denominator) / (2n * denominator));
}

/** Truncating division toward zero. */
export function truncDiv(numerator: bigint, denominator: bigint): bigint {
  if (denominator <= 0n) {
    throw new Error("truncDiv: denominator must be positive");
  }
  return numerator >= 0n ? numerator / denominator : -((-numerator) / denominator);
}

/** Exact integer floor square root via Newton's method. */
export function isqrt(n: bigint): bigint {
  if (n < 0n) {
    throw new Error("isqrt: negative input");
  }
  if (n < 2n) {
    return n;
  }
  // Initial guess: 2^ceil(bitlen/2).
  let x = 1n << ((bitLength(n) + 1n) >> 1n);
  // Newton iteration converges monotonically to floor(sqrt(n)) from above.
  for (;;) {
    const y = (x + n / x) >> 1n;
    if (y >= x) {
      return x;
    }
    x = y;
  }
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

/** 2^k as bigint, k >= 0. */
export function pow2(k: number): bigint {
  if (k < 0) {
    throw new Error("pow2: negative exponent");
  }
  return 1n << BigInt(k);
}
