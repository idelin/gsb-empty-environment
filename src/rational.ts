export type RationalInput =
  | Rational
  | bigint
  | number
  | string
  | RationalTuple
  | RationalLike;

export type RationalTuple = readonly [numerator: bigint | number, denominator: bigint | number];

export interface RationalLike {
  readonly n: bigint | number | string;
  readonly d?: bigint | number | string;
  readonly numerator?: bigint | number | string;
  readonly denominator?: bigint | number | string;
}

const BIGINT_ZERO = 0n;
const BIGINT_ONE = 1n;

function gcd(a: bigint, b: bigint): bigint {
  let x = a < 0n ? -a : a;
  let y = b < 0n ? -b : b;
  while (y !== 0n) {
    const t = x % y;
    x = y;
    y = t;
  }
  return x;
}

function toBigInt(value: bigint | number | string): bigint {
  if (typeof value === 'bigint') return value;
  if (typeof value === 'number') {
    if (!Number.isSafeInteger(value)) {
      throw new TypeError(`Exact rational integer expected, got non-integer number ${value}`);
    }
    return BigInt(value);
  }
  if (typeof value === 'string' && /^[+-]?\d+$/.test(value)) return BigInt(value);
  throw new TypeError(`Expected a bigint, safe integer, or integer string, got ${String(value)}`);
}

function parseDecimal(value: string): [bigint, bigint] {
  const matched = /^([+-]?)(\d+)(?:\.(\d+))?(?:[eE]([+-]?\d+))?$/.exec(value.trim());
  if (!matched) throw new TypeError(`Invalid rational literal: ${value}`);
  const sign = matched[1] === '-' ? -1n : 1n;
  const integerPart = matched[2] ?? '0';
  const fractionalPart = matched[3] ?? '';
  const exponent = matched[4] ? Number(matched[4]) : 0;
  const digits = `${integerPart}${fractionalPart}`.replace(/^0+(?=\d)/, '');
  let numerator = BigInt(digits || '0') * sign;
  let denominator = 10n ** BigInt(fractionalPart.length);

  if (exponent >= 0) {
    numerator *= 10n ** BigInt(exponent);
  } else {
    const shift = BigInt(-exponent);
    denominator *= 10n ** shift;
  }
  return [numerator, denominator];
}

export class Rational {
  readonly n: bigint;
  readonly d: bigint;

  constructor(numerator: bigint | number | string = 0n, denominator: bigint | number | string = 1n) {
    let n = toBigInt(numerator);
    let d = toBigInt(denominator);
    if (d === 0n) throw new RangeError('Rational denominator must not be zero');
    if (d < 0n) {
      n = -n;
      d = -d;
    }
    const g = gcd(n, d);
    this.n = n / g;
    this.d = d / g;
  }

  static from(input: RationalInput): Rational {
    if (input instanceof Rational) return input;
    if (typeof input === 'bigint') return new Rational(input, 1n);
    if (typeof input === 'number') {
      if (Number.isSafeInteger(input)) return new Rational(BigInt(input), 1n);
      return Rational.from(String(input));
    }
    if (typeof input === 'string') {
      const trimmed = input.trim();
      const fractionMatch = /^([+-]?\d+)\s*\/\s*([+-]?\d+)$/.exec(trimmed);
      if (fractionMatch) return new Rational(fractionMatch[1], fractionMatch[2]);
      return new Rational(...parseDecimal(trimmed));
    }
    if (Array.isArray(input)) return new Rational(input[0], input[1]);
    const numerator = input.n ?? input.numerator;
    const denominator = input.d ?? input.denominator ?? 1n;
    if (numerator === undefined) throw new TypeError('Rational input requires a numerator');
    return new Rational(numerator, denominator);
  }

  static zero(): Rational {
    return ZERO;
  }

  static one(): Rational {
    return ONE;
  }

  isZero(): boolean {
    return this.n === BIGINT_ZERO;
  }

  isPositive(): boolean {
    return this.n > BIGINT_ZERO;
  }

  add(other: RationalInput): Rational {
    const b = Rational.from(other);
    return new Rational(this.n * b.d + b.n * this.d, this.d * b.d);
  }

  sub(other: RationalInput): Rational {
    const b = Rational.from(other);
    return new Rational(this.n * b.d - b.n * this.d, this.d * b.d);
  }

  mul(other: RationalInput): Rational {
    const b = Rational.from(other);
    return new Rational(this.n * b.n, this.d * b.d);
  }

  div(other: RationalInput): Rational {
    const b = Rational.from(other);
    if (b.isZero()) throw new RangeError('Division by zero rational');
    return new Rational(this.n * b.d, this.d * b.n);
  }

  neg(): Rational {
    return new Rational(-this.n, this.d);
  }

  cmp(other: RationalInput): -1 | 0 | 1 {
    const b = Rational.from(other);
    const left = this.n * b.d;
    const right = b.n * this.d;
    if (left < right) return -1;
    if (left > right) return 1;
    return 0;
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
    const b = Rational.from(other);
    return this.lte(b) ? this : b;
  }

  max(other: RationalInput): Rational {
    const b = Rational.from(other);
    return this.gte(b) ? this : b;
  }

  toJSON(): { n: string; d: string } {
    return { n: this.n.toString(), d: this.d.toString() };
  }

  toString(): string {
    return this.d === BIGINT_ONE ? this.n.toString() : `${this.n.toString()}/${this.d.toString()}`;
  }
}

export const ZERO = new Rational(0n, 1n);
export const ONE = new Rational(1n, 1n);
export const THREE = new Rational(3n, 1n);

export function maxRational<T extends Rational | null>(a: T, b: T): T {
  if (a === null) return b;
  if (b === null) return a;
  return a.gte(b) ? a : b;
}

export type ExtendedRationalJSON =
  | { kind: 'finite'; value: { n: string; d: string }; text: string }
  | { kind: 'infinite'; text: '+Infinity' };

export class ExtendedRational {
  readonly value: Rational | null;

  private constructor(value: Rational | null) {
    this.value = value;
  }

  static finite(value: RationalInput): ExtendedRational {
    return new ExtendedRational(Rational.from(value));
  }

  static infinite(): ExtendedRational {
    return new ExtendedRational(null);
  }

  get infinite(): boolean {
    return this.value === null;
  }

  cmp(other: ExtendedRational): -1 | 0 | 1 {
    if (this.value === null && other.value === null) return 0;
    if (this.value === null) return 1;
    if (other.value === null) return -1;
    return this.value.cmp(other.value);
  }

  max(other: ExtendedRational): ExtendedRational {
    return this.cmp(other) >= 0 ? this : other;
  }

  exceeds(cap: Rational): boolean {
    return this.value === null || this.value.gt(cap);
  }

  equalsBoundary(cap: Rational): boolean {
    return this.value !== null && this.value.eq(cap);
  }

  toJSON(): ExtendedRationalJSON {
    if (this.value === null) return { kind: 'infinite', text: '+Infinity' };
    return { kind: 'finite', value: this.value.toJSON(), text: this.value.toString() };
  }

  toString(): string {
    return this.value === null ? '+Infinity' : this.value.toString();
  }
}
