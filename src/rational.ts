/**
 * Exact rational arithmetic.  Every time and duration in this package is a
 * rational; no IEEE-754 value is used internally.
 */

export type RationalInput =
  | Rational
  | bigint
  | number
  | string
  | { n: bigint | number | string; d?: bigint | number | string }
  | { numerator: bigint | number | string; denominator?: bigint | number | string };

function pow10(exponent: number): bigint {
  if (!Number.isInteger(exponent) || exponent < 0) {
    throw new TypeError(`invalid decimal exponent: ${exponent}`);
  }
  let result = 1n;
  for (let i = 0; i < exponent; i++) result *= 10n;
  return result;
}

function gcd(a: bigint, b: bigint): bigint {
  let x = a < 0n ? -a : a;
  let y = b < 0n ? -b : b;
  while (y !== 0n) {
    const r = x % y;
    x = y;
    y = r;
  }
  return x;
}

function parseInteger(value: bigint | number | string): bigint {
  if (typeof value === "bigint") return value;
  if (typeof value === "number") {
    if (!Number.isFinite(value) || !Number.isInteger(value)) {
      throw new TypeError(`not an integer: ${value}`);
    }
    return BigInt(value);
  }
  if (!/^[+-]?\d+$/.test(value)) {
    throw new TypeError(`not an integer: ${value}`);
  }
  return BigInt(value);
}

function parseDecimal(value: string): Rational {
  const text = value.trim();
  const match = /^([+-]?)(\d*)(?:\.(\d*))?(?:[eE]([+-]?\d+))?$/.exec(text);
  if (!match || (match[2] === "" && (match[3] ?? "") === "")) {
    throw new TypeError(`not a rational literal: ${value}`);
  }
  const sign = match[1] === "-" ? -1n : 1n;
  const whole = match[2] === "" ? "0" : match[2];
  const fractional = match[3] ?? "";
  const exponent = Number(match[4] ?? "0");
  if (!Number.isSafeInteger(exponent)) {
    throw new TypeError(`decimal exponent is out of range: ${value}`);
  }
  let numerator = BigInt(whole + fractional);
  let denominator = pow10(fractional.length);
  if (exponent >= 0) numerator *= pow10(exponent);
  else denominator *= pow10(-exponent);
  return new Rational(sign * numerator, denominator);
}

function parseString(value: string): Rational {
  const text = value.trim();
  const slash = /^([+-]?\d+)\s*\/\s*([+-]?\d+)$/.exec(text);
  if (slash) return new Rational(BigInt(slash[1]), BigInt(slash[2]));
  return parseDecimal(text);
}

export class Rational {
  readonly n: bigint;
  readonly d: bigint;

  constructor(n: bigint | number | string, d: bigint | number | string = 1n) {
    let numerator = parseInteger(n);
    let denominator = parseInteger(d);
    if (denominator === 0n) throw new RangeError("rational denominator is zero");
    if (denominator < 0n) {
      numerator = -numerator;
      denominator = -denominator;
    }
    const divisor = gcd(numerator, denominator);
    this.n = numerator / divisor;
    this.d = denominator / divisor;
    Object.freeze(this);
  }

  static from(value: RationalInput): Rational {
    return value instanceof Rational ? value : rat(value);
  }

  static zero(): Rational {
    return ZERO;
  }

  add(other: RationalInput): Rational {
    const rhs = Rational.from(other);
    return new Rational(this.n * rhs.d + rhs.n * this.d, this.d * rhs.d);
  }

  sub(other: RationalInput): Rational {
    const rhs = Rational.from(other);
    return new Rational(this.n * rhs.d - rhs.n * this.d, this.d * rhs.d);
  }

  mul(other: RationalInput): Rational {
    const rhs = Rational.from(other);
    return new Rational(this.n * rhs.n, this.d * rhs.d);
  }

  div(other: RationalInput): Rational {
    const rhs = Rational.from(other);
    if (rhs.n === 0n) throw new RangeError("division by zero");
    return new Rational(this.n * rhs.d, this.d * rhs.n);
  }

  neg(): Rational {
    return new Rational(-this.n, this.d);
  }

  abs(): Rational {
    return this.n < 0n ? this.neg() : this;
  }

  cmp(other: RationalInput): -1 | 0 | 1 {
    const rhs = Rational.from(other);
    const diff = this.n * rhs.d - rhs.n * this.d;
    return diff < 0n ? -1 : diff > 0n ? 1 : 0;
  }

  eq(other: RationalInput): boolean {
    return this.cmp(other) === 0;
  }

  lt(other: RationalInput): boolean {
    return this.cmp(other) < 0;
  }

  lte(other: RationalInput): boolean {
    return this.cmp(other) <= 0;
  }

  gt(other: RationalInput): boolean {
    return this.cmp(other) > 0;
  }

  gte(other: RationalInput): boolean {
    return this.cmp(other) >= 0;
  }

  min(other: RationalInput): Rational {
    return this.lte(other) ? this : Rational.from(other);
  }

  max(other: RationalInput): Rational {
    return this.gte(other) ? this : Rational.from(other);
  }

  isZero(): boolean {
    return this.n === 0n;
  }

  isPositive(): boolean {
    return this.n > 0n;
  }

  isNegative(): boolean {
    return this.n < 0n;
  }

  toNumber(): number {
    return Number(this.n) / Number(this.d);
  }

  toString(): string {
    return this.d === 1n ? this.n.toString() : `${this.n}/${this.d}`;
  }

  toJSON(): { numerator: string; denominator: string } {
    return { numerator: this.n.toString(), denominator: this.d.toString() };
  }
}

const ZERO = new Rational(0n, 1n);

export function rat(value: RationalInput): Rational {
  if (value instanceof Rational) return value;
  if (typeof value === "bigint") return new Rational(value, 1n);
  if (typeof value === "number") {
    if (!Number.isFinite(value)) {
      throw new TypeError(`not a finite rational: ${value}`);
    }
    if (Number.isInteger(value)) return new Rational(value, 1n);
    return parseDecimal(value.toString());
  }
  if (typeof value === "string") return parseString(value);
  if ("n" in value) return new Rational(value.n, value.d ?? 1n);
  return new Rational(value.numerator, value.denominator ?? 1n);
}

export function ratMin(a: RationalInput, b: RationalInput): Rational {
  const x = rat(a);
  const y = rat(b);
  return x.lte(y) ? x : y;
}

export function ratMax(a: RationalInput, b: RationalInput): Rational {
  const x = rat(a);
  const y = rat(b);
  return x.gte(y) ? x : y;
}

export function ratAbs(value: RationalInput): Rational {
  return rat(value).abs();
}

export function ratToString(value: RationalInput): string {
  return rat(value).toString();
}

/** Parse a serialized rational (either "n/d", a decimal, or toJSON shape). */
export function ratFromJSON(value: unknown): Rational {
  if (typeof value === "string" || typeof value === "number" || typeof value === "bigint") {
    return rat(value);
  }
  if (typeof value === "object" && value !== null) {
    if ("numerator" in value) {
      const v = value as { numerator: unknown; denominator?: unknown };
      return rationalFromUnknownParts(v.numerator, v.denominator ?? "1");
    }
    if ("n" in value) {
      const v = value as { n: unknown; d?: unknown };
      return rationalFromUnknownParts(v.n, v.d ?? "1");
    }
  }
  throw new TypeError("invalid serialized rational");
}

function rationalFromUnknownParts(n: unknown, d: unknown): Rational {
  if (
    (typeof n !== "string" && typeof n !== "number" && typeof n !== "bigint") ||
    (typeof d !== "string" && typeof d !== "number" && typeof d !== "bigint")
  ) {
    throw new TypeError("invalid rational parts");
  }
  return new Rational(n, d);
}

export const RAT_ZERO = ZERO;
export const RAT_ONE = new Rational(1n, 1n);
