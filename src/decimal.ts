/**
 * Exact decimal arithmetic on BigInt mantissas.
 *
 * A Dec is value = m * 10^e with m a BigInt. Large magnitudes never pass
 * through Number; only decimal exponents (small by construction) are JS
 * numbers. Rounding is always explicit: every operation that can be inexact
 * takes a precision (significant decimal digits) and a RoundingMode.
 */

export type RoundingMode = 'floor' | 'ceil' | 'half-even';

const TEN = 10n;

export function pow10(k: number): bigint {
  if (!Number.isInteger(k) || k < 0) throw new RangeError(`pow10: bad exponent ${k}`);
  return TEN ** BigInt(k);
}

export function numDigits(x: bigint): number {
  return (x < 0n ? -x : x).toString().length;
}

function signOf(x: bigint): number {
  return x > 0n ? 1 : x < 0n ? -1 : 0;
}

function parseSmallInt(s: string): number {
  let i = 0;
  let neg = false;
  if (s[0] === '+') i = 1;
  else if (s[0] === '-') { neg = true; i = 1; }
  let v = 0;
  for (; i < s.length; i++) {
    v = v * 10 + (s.charCodeAt(i) - 48);
    if (v > 1_000_000) throw new RangeError('decimal exponent too large');
  }
  return neg ? -v : v;
}

function divScaled(A: bigint, B: bigint, k: number): { q: bigint; r: bigint; d: bigint } {
  // floor(A * 10^k / B) with remainder; d is the effective divisor.
  if (k >= 0) {
    const n = A * pow10(k);
    return { q: n / B, r: n % B, d: B };
  }
  const d = B * pow10(-k);
  return { q: A / d, r: A % d, d };
}

export class Dec {
  readonly m: bigint;
  readonly e: number;

  private constructor(m: bigint, e: number) {
    this.m = m;
    this.e = e;
  }

  static readonly zero: Dec = Dec.of(0n);
  static readonly one: Dec = Dec.of(1n);

  /** Canonical form: zero has e = 0, otherwise the mantissa has no trailing zeros. */
  static of(m: bigint, e = 0): Dec {
    if (m === 0n) return new Dec(0n, 0);
    while (m % TEN === 0n) {
      m /= TEN;
      e += 1;
    }
    return new Dec(m, e);
  }

  static fromBigInt(v: bigint): Dec {
    return Dec.of(v, 0);
  }

  /** Parses [-]ddd[.ddd][e[-]ddd] exactly; the mantissa never touches Number. */
  static fromString(src: string): Dec {
    const s = src.trim();
    let m = /^([+-]?)(\d+)(?:\.(\d+))?(?:[eE]([+-]?\d+))?$/.exec(s);
    if (m) {
      const exp = m[4] === undefined ? 0 : parseSmallInt(m[4]);
      const mant = BigInt((m[1] === '-' ? '-' : '') + m[2] + (m[3] ?? ''));
      return Dec.of(mant, exp - (m[3] ?? '').length);
    }
    m = /^([+-]?)\.(\d+)(?:[eE]([+-]?\d+))?$/.exec(s);
    if (m) {
      const exp = m[3] === undefined ? 0 : parseSmallInt(m[3]);
      const mant = BigInt((m[1] === '-' ? '-' : '') + m[2]);
      return Dec.of(mant, exp - m[2].length);
    }
    throw new SyntaxError(`bad decimal literal: ${src}`);
  }

  toString(): string {
    if (this.m === 0n) return '0';
    const neg = this.m < 0n;
    const digits = (neg ? -this.m : this.m).toString();
    let out: string;
    if (this.e >= 0) {
      out = digits + '0'.repeat(this.e);
    } else {
      const point = digits.length + this.e;
      out = point > 0
        ? digits.slice(0, point) + '.' + digits.slice(point)
        : '0.' + '0'.repeat(-point) + digits;
    }
    return neg ? '-' + out : out;
  }

  static sign(d: Dec): number {
    return signOf(d.m);
  }

  static isZero(d: Dec): boolean {
    return d.m === 0n;
  }

  static cmp(a: Dec, b: Dec): number {
    const sa = signOf(a.m);
    const sb = signOf(b.m);
    if (sa !== sb) return sa < sb ? -1 : 1;
    if (sa === 0) return 0;
    let am = a.m;
    let bm = b.m;
    if (a.e > b.e) am = a.m * pow10(a.e - b.e);
    else if (b.e > a.e) bm = b.m * pow10(b.e - a.e);
    return am < bm ? -1 : am > bm ? 1 : 0;
  }

  static eq(a: Dec, b: Dec): boolean {
    return a.m === b.m && a.e === b.e;
  }

  static min(a: Dec, b: Dec): Dec {
    return Dec.cmp(a, b) <= 0 ? a : b;
  }

  static max(a: Dec, b: Dec): Dec {
    return Dec.cmp(a, b) >= 0 ? a : b;
  }

  static neg(a: Dec): Dec {
    return Dec.of(-a.m, a.e);
  }

  static abs(a: Dec): Dec {
    return a.m < 0n ? Dec.of(-a.m, a.e) : a;
  }

  /** Exact. */
  static add(a: Dec, b: Dec): Dec {
    const e = a.e < b.e ? a.e : b.e;
    const am = a.e === e ? a.m : a.m * pow10(a.e - e);
    const bm = b.e === e ? b.m : b.m * pow10(b.e - e);
    return Dec.of(am + bm, e);
  }

  /** Exact. */
  static sub(a: Dec, b: Dec): Dec {
    return Dec.add(a, Dec.neg(b));
  }

  /** Exact. */
  static mul(a: Dec, b: Dec): Dec {
    return Dec.of(a.m * b.m, a.e + b.e);
  }

  /** Exact: d / 2 = d * 5 * 10^-1. */
  static halve(d: Dec): Dec {
    return Dec.of(d.m * 5n, d.e - 1);
  }

  /**
   * a / b rounded to `prec` significant decimal digits in the given direction.
   * 'floor' rounds toward -Infinity, 'ceil' toward +Infinity,
   * 'half-even' to nearest with ties to even.
   */
  static div(a: Dec, b: Dec, prec: number, mode: RoundingMode): Dec {
    if (!Number.isInteger(prec) || prec < 1) throw new RangeError('prec must be an integer >= 1');
    if (b.m === 0n) throw new RangeError('decimal division by zero');
    if (a.m === 0n) return Dec.zero;
    const neg = a.m < 0n !== b.m < 0n;
    const A = a.m < 0n ? -a.m : a.m;
    const B = b.m < 0n ? -b.m : b.m;
    let k = prec - (numDigits(A) - numDigits(B));
    let { q, r, d } = divScaled(A, B, k);
    const dq = numDigits(q);
    if (dq !== prec) {
      k += prec - dq;
      ({ q, r, d } = divScaled(A, B, k));
    }
    let up = false;
    if (r !== 0n) {
      if (mode === 'floor') up = neg;
      else if (mode === 'ceil') up = !neg;
      else {
        const twice = 2n * r;
        up = twice > d || (twice === d && q % 2n !== 0n);
      }
    }
    let m = up ? q + 1n : q;
    let e = a.e - b.e - k;
    if (numDigits(m) > prec) {
      m /= TEN;
      e += 1;
    }
    return Dec.of(neg ? -m : m, e);
  }

  /** Round an exact value to `prec` significant digits in the given direction. */
  static round(d: Dec, prec: number, mode: RoundingMode): Dec {
    return Dec.div(d, Dec.one, prec, mode);
  }
}
