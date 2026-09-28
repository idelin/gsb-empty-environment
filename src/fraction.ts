/**
 * Exact rational numbers over JavaScript BigInt.
 *
 * Canonical representation:
 *   - denominator is always positive (a negative denominator moves to the numerator)
 *   - numerator and denominator are coprime
 *   - every zero is exactly 0/1
 *
 * Values never pass through `number`, so no precision is lost on big inputs.
 */

export type Sign = -1 | 0 | 1;

export interface Fraction {
  readonly n: bigint;
  readonly d: bigint;
}

/** Greatest common divisor of two bigints (sign-insensitive). */
export function gcd(a: bigint, b: bigint): bigint {
  a = a < 0n ? -a : a;
  b = b < 0n ? -b : b;
  while (b !== 0n) {
    [a, b] = [b, a % b];
  }
  return a;
}

function toBigInt(v: bigint | number): bigint {
  if (typeof v === "bigint") return v;
  if (typeof v === "number") {
    if (!Number.isInteger(v)) {
      throw new RangeError(`Only integer numerators/denominators are accepted: ${v}`);
    }
    return BigInt(v);
  }
  throw new TypeError(`Expected bigint or integer number, got ${typeof v}`);
}

/** Construct a canonical fraction. Every zero is normalized to 0/1. */
export function frac(numerator: bigint | number, denominator: bigint | number = 1n): Fraction {
  let n = toBigInt(numerator);
  let d = toBigInt(denominator);
  if (d === 0n) throw new RangeError("Zero denominator");
  if (n === 0n) return { n: 0n, d: 1n };
  if (d < 0n) {
    n = -n;
    d = -d;
  }
  const g = gcd(n, d);
  return { n: n / g, d: d / g };
}

/** Bit length of |x|; zero reports 0. Exact: built from the binary string, never `Number(x)`. */
export function bitLength(x: bigint): number {
  if (x < 0n) x = -x;
  return x === 0n ? 0 : x.toString(2).length;
}

export function add(a: Fraction, b: Fraction): Fraction {
  return frac(a.n * b.d + b.n * a.d, a.d * b.d);
}

export function sub(a: Fraction, b: Fraction): Fraction {
  return frac(a.n * b.d - b.n * a.d, a.d * b.d);
}

export function mul(a: Fraction, b: Fraction): Fraction {
  return frac(a.n * b.n, a.d * b.d);
}

export function div(a: Fraction, b: Fraction): Fraction {
  if (b.n === 0n) throw new RangeError("Division by zero");
  return frac(a.n * b.d, a.d * b.n);
}

export function neg(a: Fraction): Fraction {
  return { n: -a.n, d: a.d };
}

export function reciprocal(a: Fraction): Fraction {
  if (a.n === 0n) throw new RangeError("Reciprocal of zero");
  return frac(a.d, a.n);
}

export function sign(a: Fraction): Sign {
  return a.n === 0n ? 0 : a.n < 0n ? -1 : 1;
}

export function isZero(a: Fraction): boolean {
  return a.n === 0n;
}

export function isInt(a: Fraction): boolean {
  return a.d === 1n;
}

/**
 * Cross-multiplication comparison. No decimal expansion is produced, so
 * operands with arbitrarily large numerators/denominators compare safely.
 *
 * Returns -1, 0, or 1.
 */
export function cmp(a: Fraction, b: Fraction): -1 | 0 | 1 {
  const lhs = a.n * b.d;
  const rhs = b.n * a.d;
  if (lhs === rhs) return 0;
  return lhs < rhs ? -1 : 1;
}

export function eq(a: Fraction, b: Fraction): boolean {
  return a.n === b.n && a.d === b.d;
}

export function max(a: Fraction, b: Fraction): Fraction {
  return cmp(a, b) >= 0 ? a : b;
}

export function min(a: Fraction, b: Fraction): Fraction {
  return cmp(a, b) <= 0 ? a : b;
}

/** floor(a / b) for a positive divisor b. */
function floorDiv(a: bigint, b: bigint): bigint {
  const q = a / b;
  return a % b < 0n ? q - 1n : q;
}

/**
 * Explicit rounding rules for {@link roundToDecimal}.
 * There is no implicit float rounding anywhere in the library.
 */
export type RoundingMode =
  | "floor" // towards -Infinity
  | "ceil" // towards +Infinity
  | "trunc" // towards zero
  | "half-up"; // ties away from zero

/**
 * Round an exact fraction to `digits` places after the decimal point.
 * The result stays an exact Fraction (denominator 10^digits before reduction);
 * callers must not mistake the rounded value for the original exact value.
 */
export function roundToDecimal(a: Fraction, digits: number, mode: RoundingMode = "half-up"): Fraction {
  if (!Number.isInteger(digits) || digits < 0) {
    throw new RangeError(`digits must be a non-negative integer, got ${digits}`);
  }
  const scale = 10n ** BigInt(digits);
  const negative = a.n < 0n;
  const absN = negative ? -a.n : a.n;

  const floorScaled = floorDiv(absN * scale, a.d);
  const remainder = absN * scale - floorScaled * a.d;
  const exact = remainder === 0n;

  let magnitude: bigint;
  switch (mode) {
    case "floor":
      magnitude = negative && !exact ? floorScaled + 1n : floorScaled;
      break;
    case "ceil":
      magnitude = !negative && !exact ? floorScaled + 1n : floorScaled;
      break;
    case "trunc":
      magnitude = floorScaled;
      break;
    case "half-up":
      magnitude = floorScaled + (remainder * 2n >= a.d ? 1n : 0n);
      break;
    default: {
      const exhaustive: never = mode;
      throw new Error(`Unknown rounding mode: ${String(exhaustive)}`);
    }
  }
  return frac(negative ? -magnitude : magnitude, scale);
}

/** Canonical "n/d" rendering (or just "n" for integers). */
export function toFractionString(a: Fraction): string {
  return a.d === 1n ? a.n.toString() : `${a.n.toString()}/${a.d.toString()}`;
}
