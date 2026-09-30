/**
 * Exact rational arithmetic on BigInt.
 *
 * Rules (explicit, no silent rounding anywhere):
 *  - All values are stored as { num, den } with den > 0 and gcd(|num|, den) = 1.
 *  - Parsing accepts exact decimal strings ("-12.345") or exact fractions
 *    ("-12.345/10", "7/3"). Parsing is exact: no Number, no floating point,
 *    no rounding. Big integers are never routed through Number.
 *  - add/sub/mul/div are exact.
 *  - Rounding happens ONLY in toDecimal(), which renders for display with an
 *    explicit rounding mode (default: round-half-even at the requested scale).
 */

export interface Rational {
  readonly num: bigint;
  readonly den: bigint;
}

function gcd(a: bigint, b: bigint): bigint {
  a = a < 0n ? -a : a;
  b = b < 0n ? -b : b;
  while (b !== 0n) {
    const t = a % b;
    a = b;
    b = t;
  }
  return a === 0n ? 1n : a;
}

export function make(num: bigint, den: bigint = 1n): Rational {
  if (den === 0n) throw new Error("rational: zero denominator");
  if (den < 0n) {
    num = -num;
    den = -den;
  }
  const g = gcd(num, den);
  return { num: num / g, den: den / g };
}

export const ZERO: Rational = make(0n);
export const ONE: Rational = make(1n);

const PARSE_RE = /^([+-]?)(\d+)(?:\.(\d+))?(?:\/(\d+))?$/;

/** Exact parse. Never touches Number. */
export function parse(text: string): Rational {
  const m = PARSE_RE.exec(text.trim());
  if (!m) throw new Error(`rational: cannot parse ${JSON.stringify(text)}`);
  const [, sign, intPart, fracPart, divPart] = m;
  const digits = intPart + (fracPart ?? "");
  let num = BigInt(digits);
  if (sign === "-") num = -num;
  let den = 10n ** BigInt(fracPart?.length ?? 0);
  if (divPart !== undefined) {
    const d = BigInt(divPart);
    if (d === 0n) throw new Error(`rational: zero divisor in ${JSON.stringify(text)}`);
    den *= d;
  }
  return make(num, den);
}

export function add(a: Rational, b: Rational): Rational {
  return make(a.num * b.den + b.num * a.den, a.den * b.den);
}
export function sub(a: Rational, b: Rational): Rational {
  return make(a.num * b.den - b.num * a.den, a.den * b.den);
}
export function mul(a: Rational, b: Rational): Rational {
  return make(a.num * b.num, a.den * b.den);
}
export function div(a: Rational, b: Rational): Rational {
  if (b.num === 0n) throw new Error("rational: division by zero");
  return make(a.num * b.den, a.den * b.num);
}
export function neg(a: Rational): Rational {
  return make(-a.num, a.den);
}
export function abs(a: Rational): Rational {
  return a.num < 0n ? make(-a.num, a.den) : a;
}

/** -1, 0, 1 */
export function cmp(a: Rational, b: Rational): number {
  const lhs = a.num * b.den;
  const rhs = b.num * a.den;
  return lhs < rhs ? -1 : lhs > rhs ? 1 : 0;
}
export function eq(a: Rational, b: Rational): boolean {
  return a.num === b.num && a.den === b.den;
}
export function isZero(a: Rational): boolean {
  return a.num === 0n;
}
export function min(a: Rational, b: Rational): Rational {
  return cmp(a, b) <= 0 ? a : b;
}
export function max(a: Rational, b: Rational): Rational {
  return cmp(a, b) >= 0 ? a : b;
}

export type RoundingMode = "half-even" | "half-up" | "down" | "up";

/**
 * Render with EXPLICIT rounding at 10^-places. This is the only place any
 * rounding exists, and it is display-only; it never feeds back into math.
 */
export function toDecimal(a: Rational, places = 6, mode: RoundingMode = "half-even"): string {
  if (!Number.isSafeInteger(places) || places < 0) throw new Error("rational: bad places");
  const negative = a.num < 0n;
  const numerator = negative ? -a.num : a.num;
  const scale = 10n ** BigInt(places);
  const scaled = numerator * scale;
  let q = scaled / a.den;
  const r = scaled % a.den;
  let roundUp = false;
  if (r !== 0n) {
    switch (mode) {
      case "up":
        roundUp = true;
        break;
      case "down":
        roundUp = false;
        break;
      case "half-up":
        roundUp = 2n * r >= a.den;
        break;
      case "half-even": {
        const twice = 2n * r;
        roundUp = twice > a.den || (twice === a.den && q % 2n === 1n);
        break;
      }
    }
  }
  if (roundUp) q += 1n;
  const intPart = q / scale;
  const fracPart = q % scale;
  const frac = fracPart.toString().padStart(places, "0");
  const body = places === 0 ? intPart.toString() : `${intPart}.${frac}`;
  return negative && q !== 0n ? `-${body}` : body;
}

/** Canonical exact string, e.g. "3/4" or "-2". Used in certificates/hashes. */
export function toCanonical(a: Rational): string {
  return a.den === 1n ? a.num.toString() : `${a.num}/${a.den}`;
}
