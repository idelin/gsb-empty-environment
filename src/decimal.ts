import { Context, DEFAULT_CONTEXT, Flag } from './context.ts';
import { absBig, digitCount, pow10, roundDrop, roundToPrecision } from './rounding.ts';

export type DecimalInput = Decimal | string | bigint | number;

type Special = 'nan' | 'inf' | null;

const MAX_EXP = 999_999;

/**
 * Exact decimal: value = coeff * 10^exp, with coeff an arbitrary-precision
 * BigInt. Big values never pass through Number. Special values NaN and
 * +/-Infinity are represented with `special` (sign kept in coeff for inf).
 */
export class Decimal {
  readonly coeff: bigint;
  readonly exp: number;
  readonly special: Special;

  constructor(coeff: bigint, exp: number, special: Special = null) {
    this.coeff = coeff;
    this.exp = exp;
    this.special = special;
  }

  static nan(): Decimal {
    return new Decimal(0n, 0, 'nan');
  }

  static infinity(sign: 1 | -1): Decimal {
    return new Decimal(sign === 1 ? 1n : -1n, 0, 'inf');
  }

  static from(value: DecimalInput): Decimal {
    if (value instanceof Decimal) return value;
    if (typeof value === 'bigint') return new Decimal(value, 0);
    if (typeof value === 'number') {
      if (Number.isNaN(value)) return Decimal.nan();
      if (!Number.isFinite(value)) return Decimal.infinity(value > 0 ? 1 : -1);
      return Decimal.parse(String(value));
    }
    return Decimal.parse(value);
  }

  private static parse(raw: string): Decimal {
    const s = raw.trim();
    if (/^[+-]?nan$/i.test(s)) return Decimal.nan();
    const inf = /^([+-]?)infinity$/i.exec(s);
    if (inf) return Decimal.infinity(inf[1] === '-' ? -1 : 1);
    const m = /^([+-]?)(?:(\d+)(?:\.(\d*))?|\.(\d+))(?:[eE]([+-]?\d+))?$/.exec(s);
    if (!m) throw new TypeError(`invalid decimal literal: ${JSON.stringify(raw)}`);
    const [, sign, intPart = '', fracA, fracB, expStr] = m;
    const frac = fracA ?? fracB ?? '';
    const exp = (expStr !== undefined ? Number(expStr) : 0) - frac.length;
    if (!Number.isSafeInteger(exp) || Math.abs(exp) > MAX_EXP) {
      throw new RangeError(`exponent out of range in ${JSON.stringify(raw)}`);
    }
    const digits = intPart + frac;
    const coeff = BigInt((sign === '-' ? '-' : '') + (digits === '' ? '0' : digits));
    return new Decimal(coeff, exp);
  }

  get isNaN(): boolean {
    return this.special === 'nan';
  }

  get isInfinite(): boolean {
    return this.special === 'inf';
  }

  get isZero(): boolean {
    return this.special === null && this.coeff === 0n;
  }

  get sign(): number {
    if (this.special === 'nan') return 0;
    if (this.coeff > 0n) return 1;
    if (this.coeff < 0n) return -1;
    return 0;
  }

  negate(): Decimal {
    return new Decimal(-this.coeff, this.exp, this.special);
  }

  abs(): Decimal {
    return this.coeff < 0n ? this.negate() : this;
  }

  add(other: DecimalInput, ctx: Context = DEFAULT_CONTEXT): Decimal {
    const rhs = Decimal.from(other);
    const special = propagateAdd(this, rhs, ctx);
    if (special) return special;
    const exp = Math.min(this.exp, rhs.exp);
    const sum =
      this.coeff * pow10(this.exp - exp) + rhs.coeff * pow10(rhs.exp - exp);
    return finish(sum, exp, ctx);
  }

  sub(other: DecimalInput, ctx: Context = DEFAULT_CONTEXT): Decimal {
    return this.add(Decimal.from(other).negate(), ctx);
  }

  mul(other: DecimalInput, ctx: Context = DEFAULT_CONTEXT): Decimal {
    const rhs = Decimal.from(other);
    if (this.isNaN || rhs.isNaN) return Decimal.nan();
    if (this.isInfinite || rhs.isInfinite) {
      if (this.isZero || rhs.isZero) {
        ctx.flags.add(Flag.InvalidOperation);
        return Decimal.nan();
      }
      return Decimal.infinity(this.sign * rhs.sign === 1 ? 1 : -1);
    }
    return finish(this.coeff * rhs.coeff, this.exp + rhs.exp, ctx);
  }

  div(other: DecimalInput, ctx: Context = DEFAULT_CONTEXT): Decimal {
    const rhs = Decimal.from(other);
    if (this.isNaN || rhs.isNaN) return Decimal.nan();
    if (this.isInfinite || rhs.isInfinite) {
      if (this.isInfinite && rhs.isInfinite) {
        ctx.flags.add(Flag.InvalidOperation);
        return Decimal.nan();
      }
      if (this.isInfinite) {
        return Decimal.infinity(this.sign * rhs.sign === 1 ? 1 : -1);
      }
      return new Decimal(0n, 0);
    }
    if (rhs.coeff === 0n) {
      if (this.coeff === 0n) {
        ctx.flags.add(Flag.InvalidOperation);
        return Decimal.nan();
      }
      ctx.flags.add(Flag.DivisionByZero);
      return Decimal.infinity(this.coeff > 0n ? 1 : -1);
    }
    if (this.coeff === 0n) {
      return new Decimal(0n, this.exp - rhs.exp);
    }

    const precision = ctx.precision;
    const ac = absBig(this.coeff);
    const bc = absBig(rhs.coeff);
    // Scale so the integer quotient carries precision+1 significant digits.
    const shift = precision + 1 - (digitCount(ac) - digitCount(bc));
    let num = ac;
    let den = bc;
    if (shift >= 0) num = ac * pow10(shift);
    else den = bc * pow10(-shift);
    const quotient = num / den;
    const remainder = num % den;
    const exp = this.exp - rhs.exp - shift;
    const negative = this.coeff < 0n !== rhs.coeff < 0n;
    const signed = negative ? -quotient : quotient;

    if (remainder === 0n) {
      // Exact quotient: canonicalize by stripping trailing zeros, then let
      // finish() apply precision only if genuinely necessary.
      let coeff = signed;
      let outExp = exp;
      while (coeff !== 0n && coeff % 10n === 0n) {
        coeff /= 10n;
        outExp += 1;
      }
      return finish(coeff, outExp, ctx);
    }

    const drop = digitCount(quotient) - precision;
    let parts = roundDrop(signed, exp, drop, ctx.rounding, ctx, true);
    if (digitCount(parts.coeff) > precision) {
      parts = { coeff: parts.coeff / 10n, exp: parts.exp + 1 };
    }
    return new Decimal(parts.coeff, parts.exp);
  }

  /** Round to the given exponent (quantum), e.g. quantize(-2) -> cents. */
  quantize(targetExp: number, ctx: Context = DEFAULT_CONTEXT): Decimal {
    if (this.isNaN) return this;
    if (this.isInfinite) {
      ctx.flags.add(Flag.InvalidOperation);
      return Decimal.nan();
    }
    if (!Number.isSafeInteger(targetExp) || Math.abs(targetExp) > MAX_EXP) {
      throw new RangeError(`invalid target exponent ${targetExp}`);
    }
    let coeff = this.coeff;
    let exp = this.exp;
    if (targetExp < exp) {
      coeff *= pow10(exp - targetExp);
      exp = targetExp;
    } else if (targetExp > exp) {
      const parts = roundDrop(coeff, exp, targetExp - exp, ctx.rounding, ctx);
      coeff = parts.coeff;
      exp = parts.exp;
    }
    if (digitCount(coeff) > ctx.precision && coeff !== 0n) {
      ctx.flags.add(Flag.InvalidOperation);
      return Decimal.nan();
    }
    return new Decimal(coeff, exp);
  }

  compare(other: DecimalInput): number {
    const rhs = Decimal.from(other);
    if (this.isNaN || rhs.isNaN) {
      throw new TypeError('cannot compare NaN');
    }
    if (this.isInfinite || rhs.isInfinite) {
      if (this.isInfinite && rhs.isInfinite) {
        return this.sign === rhs.sign ? 0 : this.sign < rhs.sign ? -1 : 1;
      }
      return this.isInfinite ? this.sign : -rhs.sign;
    }
    if (this.isZero && rhs.isZero) return 0;
    if (this.sign !== rhs.sign) return this.sign < rhs.sign ? -1 : 1;
    const exp = Math.min(this.exp, rhs.exp);
    const a = this.coeff * pow10(this.exp - exp);
    const b = rhs.coeff * pow10(rhs.exp - exp);
    return a < b ? -1 : a > b ? 1 : 0;
  }

  eq(other: DecimalInput): boolean {
    if (this.isNaN || Decimal.from(other).isNaN) return false;
    return this.compare(other) === 0;
  }

  toString(): string {
    if (this.special === 'nan') return 'NaN';
    if (this.special === 'inf') return this.coeff < 0n ? '-Infinity' : 'Infinity';
    const negative = this.coeff < 0n;
    const digits = absBig(this.coeff).toString();
    let body: string;
    if (this.exp >= 0) {
      body = digits + '0'.repeat(this.exp);
    } else {
      const point = digits.length + this.exp;
      body =
        point > 0
          ? digits.slice(0, point) + '.' + digits.slice(point)
          : '0.' + '0'.repeat(-point) + digits;
    }
    return negative ? '-' + body : body;
  }
}

function finish(coeff: bigint, exp: number, ctx: Context): Decimal {
  if (coeff === 0n) return new Decimal(0n, exp);
  const parts = roundToPrecision(coeff, exp, ctx);
  return new Decimal(parts.coeff, parts.exp);
}

function propagateAdd(a: Decimal, b: Decimal, ctx: Context): Decimal | null {
  if (a.isNaN || b.isNaN) return Decimal.nan();
  if (a.isInfinite || b.isInfinite) {
    if (a.isInfinite && b.isInfinite && a.sign !== b.sign) {
      ctx.flags.add(Flag.InvalidOperation);
      return Decimal.nan();
    }
    return a.isInfinite ? a : b;
  }
  return null;
}

export function d(value: DecimalInput): Decimal {
  return Decimal.from(value);
}
