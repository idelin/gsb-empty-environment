/**
 * Exact rational arithmetic on BigInt. No value ever passes through Number,
 * so arbitrarily large numerators/denominators stay exact.
 *
 * Invariant: den > 0 and gcd(|num|, den) = 1.
 */

export interface Rational {
  readonly num: bigint;
  readonly den: bigint;
}

export function absBigInt(x: bigint): bigint {
  return x < 0n ? -x : x;
}

export function gcd(a: bigint, b: bigint): bigint {
  let x = absBigInt(a);
  let y = absBigInt(b);
  while (y !== 0n) {
    const t = x % y;
    x = y;
    y = t;
  }
  return x;
}

export function makeRational(num: bigint, den: bigint): Rational {
  if (den === 0n) throw new RangeError("denominator must not be zero");
  let n = num;
  let d = den;
  if (d < 0n) {
    n = -n;
    d = -d;
  }
  const g = gcd(n, d);
  return { num: n / g, den: d / g };
}

/** Division rounding toward -infinity (BigInt `/` truncates toward zero). */
export function floorDiv(a: bigint, b: bigint): bigint {
  if (b === 0n) throw new RangeError("division by zero");
  const q = a / b;
  const r = a - q * b;
  return r !== 0n && r < 0n !== b < 0n ? q - 1n : q;
}

export function cmpRational(a: Rational, b: Rational): number {
  const lhs = a.num * b.den;
  const rhs = b.num * a.den;
  return lhs < rhs ? -1 : lhs > rhs ? 1 : 0;
}

export function eqRational(a: Rational, b: Rational): boolean {
  return a.num === b.num && a.den === b.den;
}

export function negateRational(a: Rational): Rational {
  return { num: -a.num, den: a.den };
}

export function addRational(a: Rational, b: Rational): Rational {
  return makeRational(a.num * b.den + b.num * a.den, a.den * b.den);
}

export function subRational(a: Rational, b: Rational): Rational {
  return addRational(a, negateRational(b));
}

export function absDiffRational(a: Rational, b: Rational): Rational {
  const d = subRational(a, b);
  return { num: absBigInt(d.num), den: d.den };
}

export function rationalToString(r: Rational): string {
  return `${r.num}/${r.den}`;
}

const DECIMAL_RE = /^([+-]?)(\d*)(?:\.(\d*))?(?:[eE]([+-]?\d+))?$/;

/**
 * Parse an exact decimal or "p/q" fraction into a Rational.
 *
 * Rounding rule: NONE. The textual decimal is the exact target; it is never
 * rounded or floated through Number. "3.14" means exactly 157/50.
 */
export function parseDecimal(input: string): Rational {
  const s = input.trim();
  const parts = s.split("/");
  if (parts.length === 2) {
    return makeRational(BigInt(parts[0].trim()), BigInt(parts[1].trim()));
  }
  const m = DECIMAL_RE.exec(s);
  if (m === null || (m[2] === "" && (m[3] === undefined || m[3] === ""))) {
    throw new SyntaxError(`invalid decimal literal: ${JSON.stringify(input)}`);
  }
  const sign = m[1] === "-" ? -1n : 1n;
  const intPart = m[2];
  const fracPart = m[3] ?? "";
  const mantissa = BigInt(intPart + fracPart);
  const exp = m[4] === undefined ? 0n : BigInt(m[4]);
  const scale = BigInt(fracPart.length) - exp;
  if (scale >= 0n) {
    return makeRational(sign * mantissa, 10n ** scale);
  }
  return makeRational(sign * mantissa * 10n ** -scale, 1n);
}

/**
 * Explicit decimal rounding modes used when *displaying* a rational.
 * (Approximation itself never rounds; only formatting does.)
 *
 * - "half-up":   round half away from zero
 * - "half-even": banker's rounding
 * - "truncate":  toward zero
 */
export type RoundingMode = "half-up" | "half-even" | "truncate";

export function formatDecimal(
  value: Rational,
  places: number,
  mode: RoundingMode = "half-up",
): string {
  if (!Number.isInteger(places) || places < 0) {
    throw new RangeError("places must be a non-negative integer");
  }
  const scale = 10n ** BigInt(places);
  const scaled = value.num * scale;
  let q = scaled / value.den; // truncates toward zero
  const rem = scaled - q * value.den;
  const twice = 2n * absBigInt(rem);
  const step = scaled < 0n ? -1n : 1n;
  if (mode === "half-up") {
    if (twice >= value.den) q += step;
  } else if (mode === "half-even") {
    if (twice > value.den || (twice === value.den && q % 2n !== 0n)) q += step;
  } else if (mode !== "truncate") {
    throw new RangeError(`unknown rounding mode: ${String(mode)}`);
  }
  const neg = q < 0n;
  const aq = absBigInt(q);
  const intPart = aq / scale;
  const fracPart = (aq % scale).toString().padStart(places, "0");
  return (neg ? "-" : "") + intPart.toString() + (places > 0 ? "." + fracPart : "");
}
