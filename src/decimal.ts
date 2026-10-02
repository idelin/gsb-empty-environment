/**
 * Exact decimal arithmetic on string literals.
 *
 * A Decimal is  (-1)^neg * coeff * 10^exp  where `coeff` is a BigInt with no
 * leading or trailing zeros (except zero itself: coeff=0n, exp=0, neg=false).
 * No binary floating point is ever involved: parsing, normalization,
 * comparison and multiple-of checks are pure string/BigInt operations.
 *
 * Huge exponents are rejected against a budget *before* any zero-string or
 * power-of-ten allocation can happen.
 */

export const DEFAULT_MAX_EXPONENT = 100_000;

export interface DecimalOptions {
  /** Max |decimal exponent| and max integer-digit count accepted. */
  maxExponent?: number;
}

export class DecimalFormatError extends Error {
  readonly input: string;
  constructor(input: string, reason: string) {
    super(`invalid decimal literal ${JSON.stringify(input)}: ${reason}`);
    this.name = "DecimalFormatError";
    this.input = input;
  }
}

export class ExponentBudgetError extends Error {
  readonly exponent: bigint;
  readonly budget: number;
  constructor(exponent: bigint, budget: number) {
    super(
      `decimal exponent ${exponent} exceeds budget ${budget}; ` +
        `rejected before allocating any exponent-length digit run`,
    );
    this.name = "ExponentBudgetError";
    this.exponent = exponent;
    this.budget = budget;
  }
}

export interface Decimal {
  /** Sign flag. Always false for zero (negative zero normalizes to zero). */
  readonly neg: boolean;
  /** Significant coefficient, positive BigInt, trailing zeros stripped. */
  readonly coeff: bigint;
  /** Base-10 exponent: value = (-1)^neg * coeff * 10^exp. */
  readonly exp: number;
  /** Original literal text, preserved verbatim (e.g. "-0", "1e3"). */
  readonly raw: string;
}

const ZERO: Decimal = Object.freeze({ neg: false, coeff: 0n, exp: 0, raw: "0" });

export function zeroDecimal(): Decimal {
  return ZERO;
}

const LITERAL_RE = /^([+-]?)(\d*)(?:\.(\d*))?(?:[eE]([+-]?\d+))?$/;

function checkBudget(magnitude: bigint, budget: number, what: string): void {
  const abs = magnitude < 0n ? -magnitude : magnitude;
  if (abs > BigInt(budget)) {
    throw new ExponentBudgetError(magnitude, budget);
  }
}

/**
 * Parse a decimal literal (optionally scientific notation) exactly.
 * Accepts: "12", "-0", "1.25", ".5", "5.", "1e3", "1.2300E-2", "+0.0e+9".
 * Number inputs go through String(n) which is exact for finite doubles
 * (e.g. String(0.1) === "0.1"); -0 is preserved via Object.is.
 */
export function parseDecimal(
  input: string | number | bigint,
  opts: DecimalOptions = {},
): Decimal {
  const budget = opts.maxExponent ?? DEFAULT_MAX_EXPONENT;
  let text: string;
  if (typeof input === "number") {
    if (!Number.isFinite(input)) {
      throw new DecimalFormatError(String(input), "not a finite number");
    }
    text = Object.is(input, -0) ? "-0" : String(input);
  } else if (typeof input === "bigint") {
    text = input.toString();
  } else {
    text = input;
  }
  const m = LITERAL_RE.exec(text);
  if (m === null || (m[2] === "" && (m[3] === undefined || m[3] === ""))) {
    throw new DecimalFormatError(text, "expected [+-]digits[.digits][(e|E)[+-]digits]");
  }
  const neg = m[1] === "-";
  const intPart = m[2] ?? "";
  const fracPart = m[3] ?? "";
  const expText = m[4] ?? "0";

  // Exponent parsed as BigInt so absurd exponents are rejected by budget
  // before any Number conversion or allocation of digit runs.
  const parsedExp = BigInt(expText);
  const fracLen = BigInt(fracPart.length);
  const combined = parsedExp - fracLen; // exponent of the raw digit string
  checkBudget(combined, budget, "exponent");

  // Strip leading zeros, then trailing zeros (adjusting the exponent).
  let digits = (intPart + fracPart).replace(/^0+/, "");
  let trailing = 0;
  const tzMatch = /0+$/.exec(digits);
  if (tzMatch !== null && tzMatch[0].length < digits.length) {
    trailing = tzMatch[0].length;
    digits = digits.slice(0, digits.length - trailing);
  } else if (tzMatch !== null) {
    digits = ""; // all zeros
  }
  if (digits === "") {
    // Zero (possibly "-0"): numerically equal to +0; raw text preserved.
    return Object.freeze({ neg: false, coeff: 0n, exp: 0, raw: text });
  }
  const finalExp = combined + BigInt(trailing);
  checkBudget(finalExp, budget, "exponent");
  const exp = Number(finalExp);
  // Integer-digit magnitude also bounded so canonical printing stays sane.
  checkBudget(BigInt(digits.length) + finalExp, budget, "magnitude");
  const coeff = BigInt(digits);
  return Object.freeze({ neg, coeff, exp, raw: text });
}

/** Build a Decimal from an explicit coefficient/exponent, normalizing. */
export function fromScaledInt(coeff: bigint, exp: number, raw?: string): Decimal {
  if (coeff === 0n) {
    return Object.freeze({ neg: false, coeff: 0n, exp: 0, raw: raw ?? "0" });
  }
  const neg = coeff < 0n;
  let c = neg ? -coeff : coeff;
  let e = exp;
  while (c % 10n === 0n) {
    c /= 10n;
    e += 1;
  }
  return Object.freeze({ neg, coeff: c, exp: e, raw: raw ?? canonical({ neg, coeff: c, exp: e, raw: "" }) });
}

export function isZero(d: Decimal): boolean {
  return d.coeff === 0n;
}

export function absDecimal(d: Decimal): Decimal {
  return d.neg ? { ...d, neg: false } : d;
}

export function negateDecimal(d: Decimal): Decimal {
  return d.coeff === 0n ? d : { ...d, neg: !d.neg };
}

function digitCount(coeff: bigint): number {
  return coeff.toString().length;
}

/** Number of integer digits of |d| (0 for |d| < 1). Zero counts as 1. */
export function integerDigits(d: Decimal): number {
  if (d.coeff === 0n) return 1;
  return Math.max(1, digitCount(d.coeff) + d.exp) > 0
    ? Math.max(0, digitCount(d.coeff) + d.exp)
    : 0;
}

/** Decimal places of the normalized value (0.10 -> 1, 1e3 -> 0). */
export function scaleOf(d: Decimal): number {
  return Math.max(0, -d.exp);
}

/**
 * Total digit count of the numeric value (SQL-style precision):
 * integer digits + fractional digits of the normalized value.
 * 1e3 -> 1000 -> 4 (numeric value, not the 3-char literal); 0.1 -> 1.
 */
export function precisionOf(d: Decimal): number {
  if (d.coeff === 0n) return 1;
  return integerDigits(d) + scaleOf(d);
}

/** True when the numeric value is an integer (1e3 and 2.0 both qualify). */
export function isIntegerValue(d: Decimal): boolean {
  return d.exp >= 0;
}

/**
 * Exact three-way comparison. Negative zero equals positive zero.
 * Compares magnitude via integer-digit counts, then aligned digit strings,
 * so no power-of-ten is ever materialized.
 */
export function compareDecimal(a: Decimal, b: Decimal): -1 | 0 | 1 {
  const az = a.coeff === 0n;
  const bz = b.coeff === 0n;
  if (az && bz) return 0;
  if (az) return b.neg ? 1 : -1;
  if (bz) return a.neg ? -1 : 1;
  if (a.neg !== b.neg) return a.neg ? -1 : 1;
  const mag = compareAbs(a, b);
  return a.neg ? ((-mag) as -1 | 0 | 1) : mag;
}

function compareAbs(a: Decimal, b: Decimal): -1 | 0 | 1 {
  const ia = digitCount(a.coeff) + a.exp;
  const ib = digitCount(b.coeff) + b.exp;
  if (ia !== ib) return ia < ib ? -1 : 1;
  let da = a.coeff.toString();
  let db = b.coeff.toString();
  if (da.length < db.length) da = da.padEnd(db.length, "0");
  else if (db.length < da.length) db = db.padEnd(da.length, "0");
  if (da === db) return 0;
  return da < db ? -1 : 1;
}

export function equalDecimal(a: Decimal, b: Decimal): boolean {
  return compareDecimal(a, b) === 0;
}

/**
 * Exact multiple-of test: is `value` an integer multiple of `m`?
 * 0.3 is a multiple of 0.1 (binary floating point cannot say that).
 * m must be non-zero.
 */
export function isMultipleOf(value: Decimal, m: Decimal): boolean {
  if (m.coeff === 0n) {
    throw new DecimalFormatError(m.raw, "multipleOf base must be non-zero");
  }
  if (value.coeff === 0n) return true;
  const e = value.exp - m.exp;
  if (e >= 0) {
    return (value.coeff * 10n ** BigInt(e)) % m.coeff === 0n;
  }
  return value.coeff % (m.coeff * 10n ** BigInt(-e)) === 0n;
}

/** Canonical plain-decimal string of the normalized value. */
export function canonical(d: Decimal): string {
  if (d.coeff === 0n) return "0";
  const digits = d.coeff.toString();
  let body: string;
  if (d.exp >= 0) {
    body = digits + "0".repeat(d.exp);
  } else {
    const point = digits.length + d.exp;
    body =
      point > 0
        ? digits.slice(0, point) + "." + digits.slice(point)
        : "0." + "0".repeat(-point) + digits;
  }
  return d.neg ? "-" + body : body;
}

/** Scientific string, for compact logging of large values. */
export function scientific(d: Decimal): string {
  if (d.coeff === 0n) return "0";
  const digits = d.coeff.toString();
  const mantissa = digits.length === 1 ? digits : digits[0] + "." + digits.slice(1);
  const e = digits.length - 1 + d.exp;
  return (d.neg ? "-" : "") + mantissa + "e" + (e >= 0 ? "+" : "") + String(e);
}
