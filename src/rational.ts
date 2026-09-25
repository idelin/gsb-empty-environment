/**
 * Exact rational arithmetic on bigints. All unit conversion math in this
 * library is done with Rational so that multi-step conversions are exact
 * and rounding only ever happens at explicitly declared precision points.
 */

export type RationalInput = Rational | bigint | number | string;

const bigAbs = (a: bigint): bigint => (a < 0n ? -a : a);

function gcd(a: bigint, b: bigint): bigint {
  a = bigAbs(a);
  b = bigAbs(b);
  while (b !== 0n) {
    const t = a % b;
    a = b;
    b = t;
  }
  return a === 0n ? 1n : a;
}

export class Rational {
  /** Numerator, any sign. */
  readonly num: bigint;
  /** Denominator, always > 0. num/den is fully reduced. */
  readonly den: bigint;

  private constructor(num: bigint, den: bigint) {
    if (den === 0n) throw new Error('rational with zero denominator');
    if (den < 0n) {
      num = -num;
      den = -den;
    }
    const g = gcd(num, den);
    this.num = num / g;
    this.den = den / g;
  }

  static of(input: RationalInput): Rational {
    if (input instanceof Rational) return input;
    if (typeof input === 'bigint') return new Rational(input, 1n);
    if (typeof input === 'number') {
      if (!Number.isFinite(input)) {
        throw new Error(`not a finite number: ${input}`);
      }
      // String(number) yields the shortest decimal that round-trips to the
      // same double, so the rational is an exact representation of the input.
      return Rational.parse(String(input));
    }
    return Rational.parse(input);
  }

  /**
   * Parses exact decimal strings ("12", "-3.25", "1.5e-3") and explicit
   * fractions ("22/7"). Never approximates.
   */
  static parse(text: string): Rational {
    const t = text.trim();
    const frac = /^([+-]?\d+)\s*\/\s*(\d+)$/.exec(t);
    if (frac) {
      return new Rational(BigInt(frac[1]), BigInt(frac[2]));
    }
    const dec = /^([+-]?)(\d*)(?:\.(\d+))?(?:[eE]([+-]?\d+))?$/.exec(t);
    if (dec && (dec[2] !== '' || dec[3] !== undefined)) {
      const sign = dec[1] === '-' ? -1n : 1n;
      const intPart = dec[2] === '' ? '0' : dec[2];
      const fracPart = dec[3] ?? '';
      const exp = BigInt(dec[4] ?? '0');
      let num = BigInt(intPart + fracPart) * sign;
      let den = 10n ** BigInt(fracPart.length);
      if (exp >= 0n) {
        num *= 10n ** exp;
      } else {
        den *= 10n ** -exp;
      }
      return new Rational(num, den);
    }
    throw new Error(`invalid numeric string: ${JSON.stringify(text)}`);
  }

  add(other: RationalInput): Rational {
    const r = Rational.of(other);
    return new Rational(this.num * r.den + r.num * this.den, this.den * r.den);
  }

  sub(other: RationalInput): Rational {
    const r = Rational.of(other);
    return new Rational(this.num * r.den - r.num * this.den, this.den * r.den);
  }

  mul(other: RationalInput): Rational {
    const r = Rational.of(other);
    return new Rational(this.num * r.num, this.den * r.den);
  }

  div(other: RationalInput): Rational {
    const r = Rational.of(other);
    if (r.num === 0n) throw new Error('division by zero rational');
    return new Rational(this.num * r.den, this.den * r.num);
  }

  neg(): Rational {
    return new Rational(-this.num, this.den);
  }

  inv(): Rational {
    if (this.num === 0n) throw new Error('cannot invert zero rational');
    return new Rational(this.den, this.num);
  }

  abs(): Rational {
    return this.num < 0n ? this.neg() : this;
  }

  cmp(other: RationalInput): number {
    const r = Rational.of(other);
    const d = this.num * r.den - r.num * this.den;
    return d < 0n ? -1 : d > 0n ? 1 : 0;
  }

  equals(other: RationalInput): boolean {
    return this.cmp(other) === 0;
  }

  get isInteger(): boolean {
    return this.den === 1n;
  }

  get isZero(): boolean {
    return this.num === 0n;
  }

  get sign(): number {
    return this.num < 0n ? -1 : this.num > 0n ? 1 : 0;
  }

  /** Canonical exact form: "3" or "-7/2". */
  toString(): string {
    return this.den === 1n ? this.num.toString() : `${this.num}/${this.den}`;
  }

  /** Decimal string if the value terminates in base 10, otherwise null. */
  toTerminatingDecimal(): string | null {
    let d = this.den;
    let twos = 0n;
    let fives = 0n;
    while (d % 2n === 0n) {
      d /= 2n;
      twos += 1n;
    }
    while (d % 5n === 0n) {
      d /= 5n;
      fives += 1n;
    }
    if (d !== 1n) return null;
    const k = twos > fives ? twos : fives;
    let scaled = this.num;
    for (let i = twos; i < k; i += 1n) scaled *= 2n;
    for (let i = fives; i < k; i += 1n) scaled *= 5n;
    const negative = scaled < 0n;
    let digits = bigAbs(scaled).toString();
    const ki = Number(k);
    if (ki > 0) {
      while (digits.length <= ki) digits = '0' + digits;
      digits = `${digits.slice(0, digits.length - ki)}.${digits.slice(digits.length - ki)}`;
    }
    return (negative ? '-' : '') + digits;
  }

  toNumber(): number {
    return Number(this.num) / Number(this.den);
  }
}

/** Human-friendly exact rendering: terminating decimal when possible. */
export function formatRational(r: Rational): string {
  return r.toTerminatingDecimal() ?? r.toString();
}

export type RoundingMode = 'half-up' | 'half-even' | 'floor' | 'ceil' | 'toward-zero';

export const ROUNDING_MODES: readonly RoundingMode[] = [
  'half-up',
  'half-even',
  'floor',
  'ceil',
  'toward-zero',
];

/**
 * Rounds an exact rational to an integer.
 * - half-up: ties round away from zero
 * - half-even: ties round to the even neighbour (banker's rounding)
 * - floor / ceil: toward -inf / +inf
 * - toward-zero: truncation
 */
export function roundToInteger(value: Rational, mode: RoundingMode): bigint {
  const q = value.num / value.den; // truncates toward zero
  const rem = value.num % value.den;
  if (rem === 0n) return q;
  const negative = value.num < 0n;
  switch (mode) {
    case 'floor':
      return negative ? q - 1n : q;
    case 'ceil':
      return negative ? q : q + 1n;
    case 'toward-zero':
      return q;
    case 'half-up': {
      const twice = bigAbs(rem) * 2n;
      if (twice >= value.den) return negative ? q - 1n : q + 1n;
      return q;
    }
    case 'half-even': {
      const twice = bigAbs(rem) * 2n;
      if (twice > value.den) return negative ? q - 1n : q + 1n;
      if (twice < value.den) return q;
      const away = negative ? q - 1n : q + 1n;
      const qEven = ((q % 2n) + 2n) % 2n === 0n;
      return qEven ? q : away;
    }
  }
}

export interface ErrorInterval {
  lo: Rational;
  loInclusive: boolean;
  hi: Rational;
  hiInclusive: boolean;
}

/**
 * Worst-case rounding error bound for a mode: after rounding an exact value
 * x to integer n, x is guaranteed to lie in n + [lo, hi] (respecting the
 * inclusivity flags). The exact per-value error is reported separately.
 */
export function roundingInterval(mode: RoundingMode): ErrorInterval {
  const zero = Rational.of(0n);
  const half = Rational.of('1/2');
  const one = Rational.of(1n);
  switch (mode) {
    case 'half-up':
    case 'half-even':
      return { lo: half.neg(), loInclusive: true, hi: half, hiInclusive: true };
    case 'floor':
      return { lo: zero, loInclusive: true, hi: one, hiInclusive: false };
    case 'ceil':
      return { lo: one.neg(), loInclusive: false, hi: zero, hiInclusive: true };
    case 'toward-zero':
      return { lo: one.neg(), loInclusive: false, hi: one, hiInclusive: false };
  }
}

/** Number(value) if it fits the safe-integer range, otherwise null. */
export function bigintToSafeNumber(n: bigint): number | null {
  const max = BigInt(Number.MAX_SAFE_INTEGER);
  if (n > max || n < -max) return null;
  return Number(n);
}
