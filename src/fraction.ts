import type { FractionView, RationalInput } from "./types.ts";

const MAX_INTEGER_DIGITS = 160;

export class ExactFraction {
  readonly numerator: bigint;
  readonly denominator: bigint;

  private constructor(numerator: bigint, denominator: bigint) {
    if (denominator <= 0n) {
      throw new Error("fraction denominator must be positive");
    }
    const divisor = gcd(abs(numerator), denominator);
    this.numerator = numerator / divisor;
    this.denominator = denominator / divisor;
  }

  static from(numerator: bigint | number, denominator: bigint | number = 1n): ExactFraction {
    return new ExactFraction(BigInt(numerator), BigInt(denominator));
  }

  static zero(): ExactFraction {
    return new ExactFraction(0n, 1n);
  }

  static one(): ExactFraction {
    return new ExactFraction(1n, 1n);
  }

  add(other: ExactFraction): ExactFraction {
    return new ExactFraction(
      this.numerator * other.denominator + other.numerator * this.denominator,
      this.denominator * other.denominator,
    );
  }

  multiply(other: ExactFraction): ExactFraction {
    return new ExactFraction(
      this.numerator * other.numerator,
      this.denominator * other.denominator,
    );
  }

  divide(other: ExactFraction): ExactFraction {
    if (other.numerator === 0n) {
      throw new Error("cannot divide by zero fraction");
    }
    return new ExactFraction(
      this.numerator * other.denominator,
      this.denominator * other.numerator,
    );
  }

  equals(other: ExactFraction): boolean {
    return this.numerator === other.numerator && this.denominator === other.denominator;
  }

  isZero(): boolean {
    return this.numerator === 0n;
  }

  greaterThan(other: ExactFraction): boolean {
    return this.numerator * other.denominator > other.numerator * this.denominator;
  }

  greaterThanOrEqual(other: ExactFraction): boolean {
    return this.numerator * other.denominator >= other.numerator * this.denominator;
  }

  toView(): FractionView {
    return {
      numerator: this.numerator.toString(),
      denominator: this.denominator.toString(),
      text:
        this.denominator === 1n
          ? this.numerator.toString()
          : `${this.numerator.toString()}/${this.denominator.toString()}`,
    };
  }
}

export function abs(value: bigint): bigint {
  return value < 0n ? -value : value;
}

export function gcd(left: bigint, right: bigint): bigint {
  let a = abs(left);
  let b = abs(right);
  while (b !== 0n) {
    const remainder = a % b;
    a = b;
    b = remainder;
  }
  return a === 0n ? 1n : a;
}

export function lcm(left: bigint, right: bigint): bigint {
  if (left === 0n || right === 0n) {
    return 0n;
  }
  return abs(left / gcd(left, right) * right);
}

function parseDecimal(text: string): ExactFraction {
  const match = /^([+-]?)(\d+)(?:\.(\d+))?(?:[eE]([+-]?\d+))?$/.exec(text);
  if (match === null) {
    throw new Error(`invalid decimal number: ${text}`);
  }

  const sign = match[1] === "-" ? -1n : 1n;
  const integerPart = match[2];
  const fractionPart = match[3] ?? "";
  const exponent = Number(match[4] ?? "0");

  if (!Number.isSafeInteger(exponent) || Math.abs(exponent) > 1000) {
    throw new Error(`decimal exponent is too large: ${text}`);
  }

  let numerator = BigInt(integerPart + fractionPart) * sign;
  let denominator = 10n ** BigInt(fractionPart.length);

  if (exponent > 0) {
    numerator *= 10n ** BigInt(exponent);
  } else if (exponent < 0) {
    denominator *= 10n ** BigInt(-exponent);
  }

  assertReasonableDigits(numerator, denominator, text);
  return ExactFraction.from(numerator, denominator);
}

function assertReasonableDigits(
  numerator: bigint,
  denominator: bigint,
  source: string,
): void {
  const numDigits = abs(numerator).toString().length;
  const denDigits = denominator.toString().length;
  if (numDigits > MAX_INTEGER_DIGITS || denDigits > MAX_INTEGER_DIGITS) {
    throw new Error(`weight is too large for exact arithmetic: ${source}`);
  }
}

export function parseRational(value: RationalInput, label = "weight"): ExactFraction {
  if (typeof value === "number") {
    if (!Number.isFinite(value) || value < 0) {
      throw new Error(`${label} must be a finite non-negative number`);
    }
    return parseDecimal(value.toString());
  }

  if (typeof value === "string") {
    const trimmed = value.trim();
    if (trimmed.length === 0) {
      throw new Error(`${label} must not be empty`);
    }

    if (trimmed.endsWith("%")) {
      return parseRational(trimmed.slice(0, -1), label).divide(ExactFraction.from(100));
    }

    const slash = trimmed.indexOf("/");
    if (slash >= 0) {
      const numeratorText = trimmed.slice(0, slash).trim();
      const denominatorText = trimmed.slice(slash + 1).trim();
      if (!/^\d+$/.test(numeratorText) || !/^\d+$/.test(denominatorText)) {
        throw new Error(`invalid fraction ${label}: ${value}`);
      }
      const fraction = ExactFraction.from(
        BigInt(numeratorText),
        BigInt(denominatorText),
      );
      assertReasonableDigits(fraction.numerator, fraction.denominator, value);
      return fraction;
    }

    return parseDecimal(trimmed);
  }

  if (
    typeof value === "object" &&
    value !== null &&
    "numerator" in value &&
    "denominator" in value
  ) {
    const numerator = BigInt(value.numerator);
    const denominator = BigInt(value.denominator);
    const fraction = ExactFraction.from(numerator, denominator);
    assertReasonableDigits(fraction.numerator, fraction.denominator, label);
    return fraction;
  }

  if (
    typeof value === "object" &&
    value !== null &&
    "num" in value &&
    "den" in value
  ) {
    const numerator = BigInt(value.num);
    const denominator = BigInt(value.den);
    const fraction = ExactFraction.from(numerator, denominator);
    assertReasonableDigits(fraction.numerator, fraction.denominator, label);
    return fraction;
  }

  throw new Error(`invalid ${label}`);
}

export interface NormalizedFractions {
  fractions: ExactFraction[];
  weights: bigint[];
  total: bigint;
  scale: bigint;
}

/**
 * Converts exact fractions into proportional integers using the least common
 * multiple of every denominator. No floating point rounding is involved.
 */
export function normalizeFractions(fractions: readonly ExactFraction[]): NormalizedFractions {
  if (fractions.length === 0) {
    throw new Error("cannot normalize an empty fraction list");
  }

  let scale = 1n;
  for (const fraction of fractions) {
    if (fraction.numerator < 0n) {
      throw new Error("weights must not be negative");
    }
    scale = lcm(scale, fraction.denominator);
    if (scale.toString().length > MAX_INTEGER_DIGITS * 2) {
      throw new Error("normalization scale is too large");
    }
  }

  const weights = fractions.map((fraction) => fraction.numerator * (scale / fraction.denominator));
  const total = weights.reduce((sum, weight) => sum + weight, 0n);

  if (total === 0n) {
    throw new Error("at least one weight must be positive");
  }

  return { fractions: [...fractions], weights, total, scale };
}
