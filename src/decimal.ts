import { RoundingMode, roundIncrement, isRoundingMode } from './rounding.ts';
import { Context, Flag, defaultContext } from './context.ts';
import { DivisionByZeroError, InvalidOperationError } from './errors.ts';

export type DecimalLike = Decimal | string | bigint | number;

/** Number of decimal digits of a positive BigInt, via exact string form. */
function numDigits(value: bigint): number {
  return value.toString().length;
}

function pow10(n: number): bigint {
  return 10n ** BigInt(n);
}

interface RoundedParts {
  coef: bigint;
  exp: number;
  rounded: boolean;
  inexact: boolean;
}

/**
 * Round (sign, coef, exp) to `precision` significant digits using `mode`.
 * Pure BigInt arithmetic; no Number ever holds the value.
 */
function roundToPrecision(
  sign: 1 | -1,
  coef: bigint,
  exp: number,
  precision: number,
  mode: RoundingMode,
): RoundedParts {
  const digits = numDigits(coef);
  if (digits <= precision) return { coef, exp, rounded: false, inexact: false };
  const drop = digits - precision;
  const divisor = pow10(drop);
  const quotient = coef / divisor;
  const remainder = coef % divisor;
  const kept = quotient + roundIncrement(mode, sign, quotient, remainder, divisor);
  let newCoef = kept;
  let newExp = exp + drop;
  // Carry can add a digit (e.g. 9.99 -> 10 with precision 2).
  if (numDigits(newCoef) > precision) {
    newCoef /= 10n;
    newExp += 1;
  }
  return { coef: newCoef, exp: newExp, rounded: true, inexact: remainder !== 0n };
}

const PARSE_RE = /^([+-]?)(\d*)(?:\.(\d*))?(?:[eE]([+-]?\d+))?$/;

/**
 * Exact decimal number: value = sign * coef * 10^exp.
 * coef is a non-negative BigInt; zero always has sign +1.
 */
export class Decimal {
  readonly sign: 1 | -1;
  readonly coef: bigint;
  readonly exp: number;

  private constructor(sign: 1 | -1, coef: bigint, exp: number) {
    this.sign = coef === 0n ? 1 : sign;
    this.coef = coef;
    this.exp = exp;
    Object.freeze(this);
  }

  static fromParts(sign: 1 | -1, coef: bigint, exp: number): Decimal {
    if (coef < 0n) throw new InvalidOperationError('coefficient must be non-negative');
    return new Decimal(sign, coef, exp);
  }

  /**
   * Build from a string ("-12.34e-5"), a bigint, a number (converted through
   * its exact decimal string — fine for small values, but pass a string for
   * anything beyond 2^53), or another Decimal.
   */
  static of(value: DecimalLike): Decimal {
    if (value instanceof Decimal) return value;
    if (typeof value === 'bigint') {
      return value < 0n ? new Decimal(-1, -value, 0) : new Decimal(1, value, 0);
    }
    if (typeof value === 'number') {
      if (!Number.isFinite(value)) {
        throw new InvalidOperationError(`cannot represent ${value} as a Decimal`);
      }
      return Decimal.parse(String(value));
    }
    return Decimal.parse(value);
  }

  static parse(text: string): Decimal {
    const m = PARSE_RE.exec(text.trim());
    if (!m || (m[2] === '' && (m[3] === undefined || m[3] === ''))) {
      throw new InvalidOperationError(`invalid decimal literal: ${JSON.stringify(text)}`);
    }
    const [, signText, intPart, fracPart = '', expPart] = m;
    const digits = (intPart + fracPart).replace(/^0+(?=\d)/, '');
    const coef = BigInt(digits === '' ? '0' : digits);
    const exp = (expPart === undefined ? 0 : parseInt(expPart, 10)) - fracPart.length;
    return new Decimal(signText === '-' ? -1 : 1, coef, exp);
  }

  static zero(): Decimal {
    return new Decimal(1, 0n, 0);
  }

  isZero(): boolean {
    return this.coef === 0n;
  }

  negate(): Decimal {
    return new Decimal(this.sign === 1 ? -1 : 1, this.coef, this.exp);
  }

  abs(): Decimal {
    return new Decimal(1, this.coef, this.exp);
  }

  /** Exact decimal string, plain notation (no exponent). */
  toString(): string {
    if (this.coef === 0n) return '0';
    const digits = this.coef.toString();
    const signText = this.sign < 0 ? '-' : '';
    if (this.exp >= 0) return signText + digits + '0'.repeat(this.exp);
    const point = digits.length + this.exp;
    if (point > 0) return signText + digits.slice(0, point) + '.' + digits.slice(point);
    return signText + '0.' + '0'.repeat(-point) + digits;
  }

  /** Scientific notation, e.g. -1.2345e+17. */
  toExponential(): string {
    if (this.coef === 0n) return '0e+0';
    const digits = this.coef.toString();
    const adjusted = digits.length - 1 + this.exp;
    const mantissa = digits.length === 1 ? digits : digits[0] + '.' + digits.slice(1);
    return `${this.sign < 0 ? '-' : ''}${mantissa}e${adjusted >= 0 ? '+' : ''}${adjusted}`;
  }

  /** -1, 0 or 1. Exponent alignment is exact BigInt scaling. */
  compare(other: DecimalLike): number {
    const that = Decimal.of(other);
    if (this.coef === 0n && that.coef === 0n) return 0;
    if (this.sign !== that.sign) return this.sign < that.sign ? -1 : 1;
    const exp = Math.min(this.exp, that.exp);
    const a = this.coef * pow10(this.exp - exp);
    const b = that.coef * pow10(that.exp - exp);
    const cmp = a < b ? -1 : a > b ? 1 : 0;
    return this.sign * cmp;
  }

  equals(other: DecimalLike): boolean {
    return this.compare(other) === 0;
  }

  /**
   * Addition (exact). With a Context, the result is rounded to the context
   * precision and ROUNDED / INEXACT flags are recorded on it.
   */
  add(other: DecimalLike, ctx?: Context): Decimal {
    const that = Decimal.of(other);
    const exp = Math.min(this.exp, that.exp);
    const sum =
      BigInt(this.sign) * this.coef * pow10(this.exp - exp) +
      BigInt(that.sign) * that.coef * pow10(that.exp - exp);
    const sign: 1 | -1 = sum < 0n ? -1 : 1;
    const coef = sum < 0n ? -sum : sum;
    return finish(sign, coef, exp, ctx);
  }

  subtract(other: DecimalLike, ctx?: Context): Decimal {
    return this.add(Decimal.of(other).negate(), ctx);
  }

  /** Multiplication (exact, unless a Context rounds it). */
  multiply(other: DecimalLike, ctx?: Context): Decimal {
    const that = Decimal.of(other);
    const sign = (this.sign * that.sign) as 1 | -1;
    return finish(sign, this.coef * that.coef, this.exp + that.exp, ctx);
  }

  /**
   * Division. Always context-aware (defaults to `defaultContext`, 28 digits,
   * HALF_EVEN) because quotients like 1/3 have no finite decimal form.
   *
   * - Divisor zero  -> sets Flag.DIVISION_BY_ZERO on the context and throws
   *   DivisionByZeroError. Flag.INEXACT is NOT set: the two conditions are
   *   always distinguishable.
   * - Non-terminating quotient -> result rounded to ctx.precision significant
   *   digits, flags INEXACT + ROUNDED set on the context.
   * - Terminating quotient -> exact result, no flags.
   */
  divide(other: DecimalLike, ctx: Context = defaultContext): Decimal {
    const that = Decimal.of(other);
    if (that.coef === 0n) {
      ctx.flags.add(Flag.DIVISION_BY_ZERO);
      throw new DivisionByZeroError(`division by zero: ${this.toString()} / 0`);
    }
    if (this.coef === 0n) return Decimal.zero();

    const sign = (this.sign * that.sign) as 1 | -1;
    let num = this.coef;
    let den = that.coef;
    let adjust = 0;

    // Scale so that num and den have the same number of digits...
    const shift = numDigits(den) - numDigits(num);
    if (shift > 0) {
      num *= pow10(shift);
      adjust -= shift;
    } else if (shift < 0) {
      den *= pow10(-shift);
      adjust += -shift;
    }
    // ...then force 1 <= num/den < 10.
    if (num < den) {
      num *= 10n;
      adjust -= 1;
    }

    const scale = ctx.precision - 1;
    const scaledNum = num * pow10(scale);
    let quotient = scaledNum / den;
    const remainder = scaledNum % den;
    let exp = this.exp - that.exp + adjust - scale;

    if (remainder === 0n) {
      // Exact: strip trailing zeros for a canonical short form.
      while (quotient % 10n === 0n) {
        quotient /= 10n;
        exp += 1;
      }
      return new Decimal(sign, quotient, exp);
    }

    ctx.flags.add(Flag.INEXACT);
    ctx.flags.add(Flag.ROUNDED);
    quotient += roundIncrement(ctx.rounding, sign, quotient, remainder, den);
    if (numDigits(quotient) > ctx.precision) {
      quotient /= 10n;
      exp += 1;
    }
    return new Decimal(sign, quotient, exp);
  }

  /**
   * Quantize: round to a specific exponent (number of fractional digits),
   * e.g. exp = -2 rounds to cents. Rounding mode is explicit — pass it
   * directly or via a Context (whose flags are then updated).
   */
  quantize(exp: number, rounding?: RoundingMode, ctx?: Context): Decimal {
    let mode: RoundingMode;
    if (ctx !== undefined) {
      mode = rounding ?? ctx.rounding;
    } else if (rounding !== undefined) {
      mode = rounding;
    } else {
      throw new InvalidOperationError('quantize requires an explicit rounding mode or a Context');
    }
    if (!isRoundingMode(mode)) {
      throw new InvalidOperationError(`unknown rounding mode: ${String(mode)}`);
    }
    if (this.coef === 0n) return new Decimal(1, 0n, exp);
    if (this.exp >= exp) {
      // Already at (or coarser than) the target exponent: exact rescale.
      return new Decimal(this.sign, this.coef * pow10(this.exp - exp), exp);
    }
    const drop = exp - this.exp;
    const divisor = pow10(drop);
    const quotient = this.coef / divisor;
    const remainder = this.coef % divisor;
    const kept = quotient + roundIncrement(mode, this.sign, quotient, remainder, divisor);
    if (ctx !== undefined) {
      ctx.flags.add(Flag.ROUNDED);
      if (remainder !== 0n) ctx.flags.add(Flag.INEXACT);
    }
    return new Decimal(this.sign, kept, exp);
  }

  /** Round to `precision` significant digits under an explicit mode/context. */
  toSignificantDigits(precision: number, rounding: RoundingMode = RoundingMode.HALF_EVEN, ctx?: Context): Decimal {
    if (this.coef === 0n) return this;
    const parts = roundToPrecision(this.sign, this.coef, this.exp, precision, rounding);
    if (ctx !== undefined && parts.rounded) {
      ctx.flags.add(Flag.ROUNDED);
      if (parts.inexact) ctx.flags.add(Flag.INEXACT);
    }
    return new Decimal(this.sign, parts.coef, parts.exp);
  }
}

/** Apply an optional context to an exact (sign, coef, exp) result. */
function finish(sign: 1 | -1, coef: bigint, exp: number, ctx?: Context): Decimal {
  if (coef === 0n) return Decimal.zero();
  if (ctx === undefined) return Decimal.fromParts(sign, coef, exp);
  const parts = roundToPrecision(sign, coef, exp, ctx.precision, ctx.rounding);
  if (parts.rounded) {
    ctx.flags.add(Flag.ROUNDED);
    if (parts.inexact) ctx.flags.add(Flag.INEXACT);
  }
  return Decimal.fromParts(sign, parts.coef, parts.exp);
}
