/**
 * Exact rational arithmetic on BigInt. Values NEVER pass through Number,
 * so arbitrarily large magnitudes keep full precision.
 *
 * Explicit rounding rules (RoundingMode) for decimal rendering:
 *  - 'down'      : truncate toward zero
 *  - 'up'        : away from zero (ceiling of the absolute value)
 *  - 'floor'     : toward negative infinity
 *  - 'ceil'      : toward positive infinity
 *  - 'half-up'   : nearest; exact ties round away from zero
 *  - 'half-even' : nearest; exact ties round to an even last digit (banker's)
 */
export type RoundingMode = 'down' | 'up' | 'floor' | 'ceil' | 'half-up' | 'half-even';

const bigAbs = (x: bigint): bigint => (x < 0n ? -x : x);

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

export class Fraction {
  /** Numerator (any sign). Denominator is always > 0 and coprime with num. */
  readonly num: bigint;
  readonly den: bigint;

  constructor(num: bigint, den: bigint = 1n) {
    if (den === 0n) throw new Error('Fraction: zero denominator');
    if (den < 0n) {
      num = -num;
      den = -den;
    }
    const g = gcd(num, den);
    this.num = num / g;
    this.den = den / g;
  }

  static zero(): Fraction {
    return new Fraction(0n, 1n);
  }

  static one(): Fraction {
    return new Fraction(1n, 1n);
  }

  /**
   * Build from a Fraction | bigint | string.
   * Number input is REJECTED on purpose: it would silently lose precision.
   * Accepted string forms: "3", "-12", "3/7", "1.25", "-0.5".
   */
  static from(value: Fraction | bigint | string): Fraction {
    if (value instanceof Fraction) return value;
    if (typeof value === 'bigint') return new Fraction(value, 1n);
    if (typeof value === 'string') return Fraction.parse(value);
    throw new Error(
      `Fraction.from: unsupported input of type ${typeof value}; ` +
        'Number is forbidden to avoid precision loss, use a string like "1.5" or "3/2"',
    );
  }

  static parse(text: string): Fraction {
    const s = text.trim();
    const frac = /^([+-]?\d+)\s*\/\s*([+-]?\d+)$/.exec(s);
    if (frac) return new Fraction(BigInt(frac[1]), BigInt(frac[2]));
    const dec = /^([+-]?)(\d+)(?:\.(\d+))?$/.exec(s) ?? /^([+-]?)\.(\d+)$/.exec(s);
    if (dec) {
      if (dec.length === 4) {
        const sign = dec[1] === '-' ? -1n : 1n;
        const intPart = dec[2];
        const fracPart = dec[3] ?? '';
        const den = 10n ** BigInt(fracPart.length);
        const num = sign * (BigInt(intPart) * den + BigInt(fracPart === '' ? '0' : fracPart));
        return new Fraction(num, den);
      }
      const sign = dec[1] === '-' ? -1n : 1n;
      const fracPart = dec[2];
      const den = 10n ** BigInt(fracPart.length);
      return new Fraction(sign * BigInt(fracPart), den);
    }
    throw new Error(`Fraction.parse: cannot parse "${text}"`);
  }

  add(o: Fraction): Fraction {
    return new Fraction(this.num * o.den + o.num * this.den, this.den * o.den);
  }

  sub(o: Fraction): Fraction {
    return new Fraction(this.num * o.den - o.num * this.den, this.den * o.den);
  }

  mul(o: Fraction): Fraction {
    return new Fraction(this.num * o.num, this.den * o.den);
  }

  div(o: Fraction): Fraction {
    if (o.num === 0n) throw new Error('Fraction.div: division by zero');
    return new Fraction(this.num * o.den, this.den * o.num);
  }

  neg(): Fraction {
    return new Fraction(-this.num, this.den);
  }

  abs(): Fraction {
    return new Fraction(bigAbs(this.num), this.den);
  }

  /** -1 | 0 | 1 */
  cmp(o: Fraction): number {
    const l = this.num * o.den;
    const r = o.num * this.den;
    return l < r ? -1 : l > r ? 1 : 0;
  }

  eq(o: Fraction): boolean {
    return this.num === o.num && this.den === o.den;
  }

  isNegative(): boolean {
    return this.num < 0n;
  }

  isZero(): boolean {
    return this.num === 0n;
  }

  /** Mathematical floor (toward -inf), exact for any sign. */
  floor(): bigint {
    const q = this.num / this.den;
    const r = this.num % this.den;
    return r !== 0n && this.num < 0n ? q - 1n : q;
  }

  /** Mathematical ceil (toward +inf), exact for any sign. */
  ceil(): bigint {
    return new Fraction(-this.num, this.den).floor() * -1n;
  }

  /** Exact fraction string: "3/7", "-2", "5". */
  toString(): string {
    return this.den === 1n ? this.num.toString() : `${this.num}/${this.den}`;
  }

  /**
   * Decimal rendering with an EXPLICIT rounding mode; pure BigInt math.
   * `places` is a small non-negative integer (a count, not a value).
   */
  toDecimal(places: number, mode: RoundingMode = 'half-up'): string {
    if (!Number.isInteger(places) || places < 0) {
      throw new Error('Fraction.toDecimal: places must be a non-negative integer');
    }
    const scale = 10n ** BigInt(places);
    const negative = this.num < 0n;
    const a = bigAbs(this.num) * scale;
    let q = a / this.den;
    const r = a % this.den;
    let bump = false;
    switch (mode) {
      case 'down':
        bump = false;
        break;
      case 'up':
        bump = r !== 0n;
        break;
      case 'floor':
        bump = negative && r !== 0n;
        break;
      case 'ceil':
        bump = !negative && r !== 0n;
        break;
      case 'half-up':
        bump = 2n * r >= this.den;
        break;
      case 'half-even':
        bump = 2n * r > this.den || (2n * r === this.den && (q & 1n) === 1n);
        break;
    }
    if (bump) q += 1n;
    const intPart = q / scale;
    const fracPart = q % scale;
    const sign = negative && q !== 0n ? '-' : '';
    if (places === 0) return sign + intPart.toString();
    return `${sign}${intPart}.${fracPart.toString().padStart(places, '0')}`;
  }
}
