// Exact rational arithmetic on BigInt. Values never pass through Number,
// so magnitudes beyond 2^53 stay exact.

export interface Rational {
  readonly num: bigint;
  readonly den: bigint; // always > 0, always reduced
}

export function gcd(a: bigint, b: bigint): bigint {
  a = a < 0n ? -a : a;
  b = b < 0n ? -b : b;
  while (b !== 0n) {
    const t = a % b;
    a = b;
    b = t;
  }
  return a;
}

export function rat(num: bigint, den: bigint = 1n): Rational {
  if (den === 0n) throw new RangeError("rational denominator must not be zero");
  if (den < 0n) {
    num = -num;
    den = -den;
  }
  const g = gcd(num, den);
  return { num: num / g, den: den / g };
}

export function add(a: Rational, b: Rational): Rational {
  return rat(a.num * b.den + b.num * a.den, a.den * b.den);
}

export function sub(a: Rational, b: Rational): Rational {
  return rat(a.num * b.den - b.num * a.den, a.den * b.den);
}

export function mul(a: Rational, b: Rational): Rational {
  return rat(a.num * b.num, a.den * b.den);
}

export function div(a: Rational, b: Rational): Rational {
  if (b.num === 0n) throw new RangeError("division by zero rational");
  return rat(a.num * b.den, a.den * b.num);
}

export function neg(a: Rational): Rational {
  return { num: -a.num, den: a.den };
}

export function abs(a: Rational): Rational {
  return a.num < 0n ? { num: -a.num, den: a.den } : a;
}

export function cmp(a: Rational, b: Rational): number {
  const lhs = a.num * b.den;
  const rhs = b.num * a.den;
  return lhs < rhs ? -1 : lhs > rhs ? 1 : 0;
}

// Explicit rounding rules. No implicit "nearest" anywhere in the library:
// every call site must name one of these modes.
//   floor     -> toward -infinity
//   ceil      -> toward +infinity
//   trunc     -> toward zero
//   half-up   -> nearest, exact halves round away from zero
//   half-even -> nearest, exact halves round to the even neighbour
export type RoundingMode = "floor" | "ceil" | "trunc" | "half-up" | "half-even";

export function roundToBigInt(value: Rational, mode: RoundingMode): bigint {
  const { num, den } = value; // den > 0
  const q = num / den; // truncates toward zero
  const r = num % den; // sign follows num
  switch (mode) {
    case "trunc":
      return q;
    case "floor":
      return r !== 0n && num < 0n ? q - 1n : q;
    case "ceil":
      return r !== 0n && num > 0n ? q + 1n : q;
    case "half-up": {
      const twice = (r < 0n ? -r : r) * 2n;
      if (twice < den) return q;
      return num >= 0n ? q + 1n : q - 1n;
    }
    case "half-even": {
      const twice = (r < 0n ? -r : r) * 2n;
      if (twice < den) return q;
      if (twice > den) return num >= 0n ? q + 1n : q - 1n;
      const down = q;
      const up = num >= 0n ? q + 1n : q - 1n;
      return down % 2n === 0n ? down : up;
    }
  }
}

export function toString(value: Rational): string {
  return value.den === 1n ? `${value.num}` : `${value.num}/${value.den}`;
}

// Decimal rendering for humans only. Rounds at `places` with an explicit
// mode; the Rational itself stays the source of truth.
export function toDecimalString(
  value: Rational,
  places: number,
  mode: RoundingMode = "trunc",
): string {
  const scale = 10n ** BigInt(places);
  const scaled = roundToBigInt(mul(value, rat(scale)), mode);
  const negative = scaled < 0n;
  const absScaled = negative ? -scaled : scaled;
  const intPart = absScaled / scale;
  const fracPart = (absScaled % scale).toString().padStart(places, "0");
  return `${negative ? "-" : ""}${intPart}.${fracPart}`;
}
