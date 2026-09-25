/**
 * Exact rational arithmetic on bigints.
 * All values are kept normalized: den > 0, gcd(|num|, den) = 1.
 */

export type RoundingMode = 'trunc' | 'floor' | 'ceil' | 'half-up' | 'half-even';

export const ROUNDING_MODES: readonly RoundingMode[] = [
  'trunc',
  'floor',
  'ceil',
  'half-up',
  'half-even',
];

function gcd(a: bigint, b: bigint): bigint {
  let x = a < 0n ? -a : a;
  let y = b < 0n ? -b : b;
  while (y !== 0n) {
    const t = x % y;
    x = y;
    y = t;
  }
  return x === 0n ? 1n : x;
}

export class Rational {
  readonly num: bigint;
  readonly den: bigint;

  private constructor(num: bigint, den: bigint) {
    if (den === 0n) throw new Error('Rational: zero denominator');
    let n = num;
    let d = den;
    if (d < 0n) {
      n = -n;
      d = -d;
    }
    const g = gcd(n, d);
    this.num = n / g;
    this.den = d / g;
  }

  static of(num: bigint | number, den: bigint | number = 1n): Rational {
    return new Rational(BigInt(num), BigInt(den));
  }

  static zero(): Rational {
    return Rational.of(0n);
  }

  static one(): Rational {
    return Rational.of(1n);
  }

  /**
   * Parse an exact decimal: optional sign, digits, optional fraction,
   * optional exponent. e.g. "273.15", "-0.0254", "1.5e3".
   */
  static parse(text: string): Rational {
    const m = /^([+-]?)(\d*)(?:\.(\d*))?(?:[eE]([+-]?\d+))?$/.exec(text.trim());
    if (!m || (m[2] === '' && (m[3] === undefined || m[3] === ''))) {
      throw new Error(`Rational.parse: invalid decimal literal: ${JSON.stringify(text)}`);
    }
    const sign = m[1] === '-' ? -1n : 1n;
    const intPart = m[2] === '' ? '0' : m[2];
    const fracPart = m[3] ?? '';
    const exp = m[4] === undefined ? 0n : BigInt(m[4]);
    let num = BigInt(intPart + fracPart) * sign;
    let den = 10n ** BigInt(fracPart.length);
    if (exp >= 0n) {
      num *= 10n ** exp;
    } else {
      den *= 10n ** -exp;
    }
    return new Rational(num, den);
  }

  /** Exact decimal interpretation of a JS number (uses its shortest decimal form). */
  static fromNumber(value: number): Rational {
    if (!Number.isFinite(value)) {
      throw new Error(`Rational.fromNumber: not finite: ${value}`);
    }
    return Rational.parse(String(value));
  }

  static from(value: Rational | number | string | bigint): Rational {
    if (value instanceof Rational) return value;
    if (typeof value === 'bigint') return Rational.of(value);
    if (typeof value === 'number') return Rational.fromNumber(value);
    return Rational.parse(value);
  }

  add(other: Rational): Rational {
    return new Rational(this.num * other.den + other.num * this.den, this.den * other.den);
  }

  sub(other: Rational): Rational {
    return new Rational(this.num * other.den - other.num * this.den, this.den * other.den);
  }

  mul(other: Rational): Rational {
    return new Rational(this.num * other.num, this.den * other.den);
  }

  div(other: Rational): Rational {
    if (other.num === 0n) throw new Error('Rational.div: division by zero');
    return new Rational(this.num * other.den, this.den * other.num);
  }

  neg(): Rational {
    return new Rational(-this.num, this.den);
  }

  abs(): Rational {
    return this.num < 0n ? this.neg() : this;
  }

  cmp(other: Rational): number {
    const lhs = this.num * other.den;
    const rhs = other.num * this.den;
    return lhs < rhs ? -1 : lhs > rhs ? 1 : 0;
  }

  equals(other: Rational): boolean {
    return this.num === other.num && this.den === other.den;
  }

  isZero(): boolean {
    return this.num === 0n;
  }

  isInteger(): boolean {
    return this.den === 1n;
  }

  sign(): number {
    return this.num < 0n ? -1 : this.num > 0n ? 1 : 0;
  }

  toNumber(): number {
    return Number(this.num) / Number(this.den);
  }

  /** True when the float64 round-trip is exact. */
  isExactlyRepresentableAsNumber(): boolean {
    const n = this.toNumber();
    if (!Number.isFinite(n)) return false;
    return Rational.fromNumber(n).equals(this);
  }

  /** Exact decimal expansion; throws-free: repeats are truncated with a marker. */
  toDecimalString(maxFracDigits = 30): string {
    if (this.den === 1n) return this.num.toString();
    // Denominators with only factors 2 and 5 terminate.
    let d = this.den;
    let twos = 0;
    let fives = 0;
    while (d % 2n === 0n) {
      d /= 2n;
      twos++;
    }
    while (d % 5n === 0n) {
      d /= 5n;
      fives++;
    }
    if (d === 1n) {
      const shift = Math.max(twos, fives);
      const scaled = this.num * 2n ** BigInt(Math.max(0, shift - twos)) * 5n ** BigInt(Math.max(0, shift - fives));
      const neg = scaled < 0n;
      const digits = (neg ? -scaled : scaled).toString().padStart(shift + 1, '0');
      const intPart = digits.slice(0, digits.length - shift) || '0';
      const fracPart = shift === 0 ? '' : '.' + digits.slice(digits.length - shift);
      return (neg ? '-' : '') + intPart + fracPart;
    }
    // Repeating: produce truncated decimal with explicit marker.
    const neg = this.num < 0n;
    let rem = neg ? -this.num : this.num;
    const intPart = rem / this.den;
    rem %= this.den;
    let frac = '';
    for (let i = 0; i < maxFracDigits && rem !== 0n; i++) {
      rem *= 10n;
      frac += (rem / this.den).toString();
      rem %= this.den;
    }
    const suffix = rem === 0n ? '' : '…';
    return (neg ? '-' : '') + intPart.toString() + '.' + frac + suffix;
  }

  toString(): string {
    if (this.den === 1n) return this.num.toString();
    return `${this.num}/${this.den}`;
  }
}

/** Round a rational to an integer using an explicit mode. Never implicit. */
export function roundToInteger(value: Rational, mode: RoundingMode): bigint {
  const { num, den } = value;
  const q = num / den; // truncates toward zero
  const r = num % den;
  if (r === 0n) return q;
  switch (mode) {
    case 'trunc':
      return q;
    case 'floor':
      return num < 0n ? q - 1n : q;
    case 'ceil':
      return num > 0n ? q + 1n : q;
    case 'half-up': {
      // ties go toward +Infinity
      const twice = r * 2n;
      const absTwice = twice < 0n ? -twice : twice;
      if (absTwice < den) return q;
      if (absTwice > den) return num > 0n ? q + 1n : q - 1n;
      return num > 0n ? q + 1n : q; // exact tie -> +Inf
    }
    case 'half-even': {
      const twice = r * 2n;
      const absTwice = twice < 0n ? -twice : twice;
      if (absTwice < den) return q;
      if (absTwice > den) return num > 0n ? q + 1n : q - 1n;
      // exact tie: choose even neighbor
      const down = num > 0n ? q : q - 1n;
      const up = num > 0n ? q + 1n : q;
      return down % 2n === 0n ? down : up;
    }
  }
}
