/**
 * Exact decimal arithmetic on string literals.
 *
 * A Decimal is stored as `sign * coeff * 10^(-scale)` where `coeff` is a
 * non-negative BigInt with no trailing zeros (unless the value is zero).
 * No binary floating point is ever involved: exponents are parsed as BigInt,
 * and the magnitude budget is checked BEFORE any power-of-ten allocation,
 * so a literal like `1e999999999999` is rejected without materializing zeros.
 */

export const DEFAULT_MAX_SCALE = 4096;

export interface DecimalOptions {
  /** Maximum allowed |scale| (i.e. |exponent| after normalization). */
  maxScale?: number;
}

export class DecimalParseError extends Error {
  readonly text: string;
  constructor(text: string, reason: string) {
    super(`invalid decimal literal ${JSON.stringify(text)}: ${reason}`);
    this.name = "DecimalParseError";
    this.text = text;
  }
}

export class DecimalBudgetError extends Error {
  readonly requestedScale: string;
  readonly budget: number;
  constructor(requested: bigint, budget: number) {
    super(
      `decimal literal needs scale ${requested}, which exceeds the configured budget +/-${budget}; ` +
        `rejected before allocating any exponential zero run`,
    );
    this.name = "DecimalBudgetError";
    this.requestedScale = requested.toString();
    this.budget = budget;
  }
}

export interface Decimal {
  /** -1, 0 or 1. Zero always has sign 0, so -0 equals +0 numerically. */
  readonly sign: -1 | 0 | 1;
  /** Non-negative significand, trailing zeros stripped. */
  readonly coeff: bigint;
  /** Value = sign * coeff * 10^(-scale). May be negative (e.g. 1e3 -> scale -3). */
  readonly scale: number;
  /** Original literal, preserved verbatim (e.g. "-0" stays "-0"). */
  readonly text?: string;
}

export const ZERO: Decimal = { sign: 0, coeff: 0n, scale: 0 };

const LITERAL_RE = /^([+-]?)(\d+)(?:\.(\d+))?(?:[eE]([+-]?\d+))?$/;

const pow10cache: bigint[] = [1n];

/** 10^n as BigInt. Callers must keep n within the configured budget. */
export function pow10(n: number): bigint {
  if (!Number.isInteger(n) || n < 0) {
    throw new RangeError(`pow10 expects a non-negative integer, got ${n}`);
  }
  for (let i = pow10cache.length; i <= n; i += 1) {
    pow10cache[i] = pow10cache[i - 1]! * 10n;
  }
  return pow10cache[n]!;
}

/**
 * Parses a decimal literal, normalizing scientific notation by definition:
 * the exponent shifts the scale exactly, leading zeros are dropped and
 * trailing zeros of the significand are folded into the scale.
 */
export function parseDecimal(text: string, options: DecimalOptions = {}): Decimal {
  if (typeof text !== "string") {
    throw new DecimalParseError(String(text), "expected a string");
  }
  const match = LITERAL_RE.exec(text);
  if (!match) {
    throw new DecimalParseError(text, "expected [-]digits[.digits][(e|E)[+|-]digits]");
  }
  const [, signChar, intPart, fracPart = "", expPart] = match;
  const maxScale = options.maxScale ?? DEFAULT_MAX_SCALE;
  // Exponent arithmetic stays in BigInt; nothing exponential is allocated
  // before this budget check.
  const exponent = expPart === undefined ? 0n : BigInt(expPart);
  const scaleBig = BigInt(fracPart.length) - exponent;
  const limit = BigInt(maxScale);
  if (scaleBig > limit || scaleBig < -limit) {
    throw new DecimalBudgetError(scaleBig, maxScale);
  }
  const digits = (intPart! + fracPart).replace(/^0+/, "");
  if (digits === "") {
    // Zero (including "-0", "0.00e9"): numerically zero, original text kept.
    return { sign: 0, coeff: 0n, scale: 0, text };
  }
  const trimmed = digits.replace(/0+$/, "");
  const scale = Number(scaleBig) - (digits.length - trimmed.length);
  return {
    sign: signChar === "-" ? -1 : 1,
    coeff: BigInt(trimmed),
    scale,
    text,
  };
}

/** Number of decimal digits of a non-negative BigInt. */
export function numDigits(coeff: bigint): number {
  return coeff.toString().length;
}

/**
 * Total positional digits of the normalized value (XML-Schema-style
 * totalDigits): digits of the significand plus zeros appended by a negative
 * scale. Numeric, not textual: 1e3 -> "1000" -> 4 digits; 0.30 -> 1 digit.
 */
export function totalDigits(d: Decimal): number {
  if (d.sign === 0) return 1;
  return numDigits(d.coeff) + Math.max(0, -d.scale);
}

/** Fraction digits of the normalized value (digits after the decimal point). */
export function fractionDigits(d: Decimal): number {
  return Math.max(0, d.scale);
}

/** Exact three-way comparison. */
export function compare(a: Decimal, b: Decimal): number {
  if (a.sign !== b.sign) return a.sign < b.sign ? -1 : 1;
  if (a.sign === 0) return 0;
  const s = Math.max(a.scale, b.scale);
  const ac = a.coeff * pow10(s - a.scale);
  const bc = b.coeff * pow10(s - b.scale);
  const cmp = ac < bc ? -1 : ac > bc ? 1 : 0;
  return a.sign === 1 ? cmp : -cmp;
}

/** Comparison by absolute value. */
export function compareAbs(a: Decimal, b: Decimal): number {
  if (a.sign === 0) return b.sign === 0 ? 0 : -1;
  if (b.sign === 0) return 1;
  const s = Math.max(a.scale, b.scale);
  const ac = a.coeff * pow10(s - a.scale);
  const bc = b.coeff * pow10(s - b.scale);
  return ac < bc ? -1 : ac > bc ? 1 : 0;
}

/**
 * Exact multiple test: is `value` an integer multiple of `multiplier`?
 * 0 is a multiple of every non-zero multiplier. Works on any sign
 * combination because coefficients are compared by magnitude.
 */
export function isMultipleOf(value: Decimal, multiplier: Decimal): boolean {
  if (multiplier.sign === 0) {
    throw new RangeError("multipleOf must be non-zero");
  }
  if (value.sign === 0) return true;
  const v = value.coeff;
  const m = multiplier.coeff;
  if (multiplier.scale >= value.scale) {
    return (v * pow10(multiplier.scale - value.scale)) % m === 0n;
  }
  return v % (m * pow10(value.scale - multiplier.scale)) === 0n;
}

/** Signed coefficient of `d` re-expressed at the given (finer-or-equal) scale. */
export function signedCoeffAt(d: Decimal, scale: number): bigint {
  if (scale < d.scale) {
    throw new RangeError(`cannot re-express scale ${d.scale} at coarser scale ${scale}`);
  }
  const c = d.coeff * pow10(scale - d.scale);
  return d.sign < 0 ? -c : c;
}

/** Builds a normalized Decimal from a signed coefficient at a given scale. */
export function fromScaledInt(coeff: bigint, scale: number): Decimal {
  if (coeff === 0n) return { sign: 0, coeff: 0n, scale: 0 };
  const sign = coeff < 0n ? -1 : 1;
  const digits = (coeff < 0n ? -coeff : coeff).toString();
  const trimmed = digits.replace(/0+$/, "");
  return {
    sign,
    coeff: BigInt(trimmed),
    scale: scale - (digits.length - trimmed.length),
  };
}

/** Exact addition of an integer to a decimal. */
export function addInteger(d: Decimal, n: bigint): Decimal {
  const s = Math.max(d.scale, 0);
  return fromScaledInt(signedCoeffAt(d, s) + n * pow10(s), s);
}

/** Plain positional notation (no exponent). */
export function toPlainString(d: Decimal): string {
  if (d.sign === 0) return "0";
  const digits = d.coeff.toString();
  const sign = d.sign < 0 ? "-" : "";
  if (d.scale <= 0) return sign + digits + "0".repeat(-d.scale);
  if (digits.length > d.scale) {
    const i = digits.length - d.scale;
    return sign + digits.slice(0, i) + "." + digits.slice(i);
  }
  return sign + "0." + "0".repeat(d.scale - digits.length) + digits;
}

/**
 * Normalized scientific notation by definition: exactly one non-zero digit
 * before the point, exponent as an exact integer (never a float).
 */
export function toScientificString(d: Decimal): string {
  if (d.sign === 0) return "0e0";
  const digits = d.coeff.toString();
  const exponent = BigInt(digits.length - 1 - d.scale);
  const mantissa = digits.length === 1 ? digits : `${digits[0]}.${digits.slice(1)}`;
  const sign = d.sign < 0 ? "-" : "";
  return `${sign}${mantissa}e${exponent >= 0n ? "+" : ""}${exponent.toString()}`;
}

export function floorDiv(a: bigint, b: bigint): bigint {
  const q = a / b;
  return a % b !== 0n && a < 0n !== b < 0n ? q - 1n : q;
}

export function ceilDiv(a: bigint, b: bigint): bigint {
  return -floorDiv(-a, b);
}

export function gcdBigInt(a: bigint, b: bigint): bigint {
  let x = a < 0n ? -a : a;
  let y = b < 0n ? -b : b;
  while (y !== 0n) {
    const t = x % y;
    x = y;
    y = t;
  }
  return x;
}

export function lcmBigInt(a: bigint, b: bigint): bigint {
  if (a === 0n || b === 0n) return 0n;
  return (a / gcdBigInt(a, b)) * b;
}
