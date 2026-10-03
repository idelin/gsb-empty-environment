export type RationalInput = Fraction | bigint | number | string;

export function bigGcd(a: bigint, b: bigint): bigint {
  let x = a < 0n ? -a : a;
  let y = b < 0n ? -b : b;
  while (y !== 0n) {
    const t = x % y;
    x = y;
    y = t;
  }
  return x;
}

export function bigLcm(a: bigint, b: bigint): bigint {
  if (a === 0n || b === 0n) return 0n;
  return (a / bigGcd(a, b)) * b;
}

/** Exact rational number backed by BigInt numerator/denominator, always normalized. */
export class Fraction {
  readonly num: bigint;
  readonly den: bigint;

  constructor(num: bigint, den: bigint = 1n) {
    if (den === 0n) throw new Error('Fraction: denominator must not be zero');
    let n = num;
    let d = den;
    if (d < 0n) {
      n = -n;
      d = -d;
    }
    const g = bigGcd(n, d) || 1n;
    this.num = n / g;
    this.den = d / g;
  }

  static readonly ZERO = new Fraction(0n, 1n);
  static readonly ONE = new Fraction(1n, 1n);

  static from(value: RationalInput): Fraction {
    if (value instanceof Fraction) return value;
    if (typeof value === 'bigint') return new Fraction(value, 1n);
    if (typeof value === 'number') {
      if (!Number.isFinite(value)) throw new Error(`Fraction: non-finite number ${value}`);
      if (Number.isInteger(value)) return new Fraction(BigInt(value), 1n);
      return Fraction.from(value.toString());
    }
    const s = value.trim();
    if (s.includes('/')) {
      const parts = s.split('/');
      if (parts.length !== 2) throw new Error(`Fraction: bad input "${value}"`);
      return new Fraction(BigInt(parts[0].trim()), BigInt(parts[1].trim()));
    }
    const expMatch = /^([+-]?[\d.]+)[eE]([+-]?\d+)$/.exec(s);
    if (expMatch) {
      const base = Fraction.from(expMatch[1]);
      const e = BigInt(expMatch[2]);
      const scale = new Fraction(10n ** (e < 0n ? -e : e), 1n);
      return e >= 0n ? base.mul(scale) : base.div(scale);
    }
    if (s.includes('.')) {
      const dot = s.indexOf('.');
      const intPart = s.slice(0, dot);
      const fracPart = s.slice(dot + 1);
      if (!/^[+-]?(\d*)$/.test(intPart) || !/^\d+$/.test(fracPart)) {
        throw new Error(`Fraction: bad decimal "${value}"`);
      }
      const negative = intPart.startsWith('-');
      const intDigits = intPart.replace(/^[+-]/, '') || '0';
      const scale = 10n ** BigInt(fracPart.length);
      const magnitude = BigInt(intDigits) * scale + BigInt(fracPart);
      return new Fraction(negative ? -magnitude : magnitude, scale);
    }
    return new Fraction(BigInt(s), 1n);
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
    if (o.num === 0n) throw new Error('Fraction: division by zero');
    return new Fraction(this.num * o.den, this.den * o.num);
  }

  neg(): Fraction {
    return new Fraction(-this.num, this.den);
  }

  abs(): Fraction {
    return this.num < 0n ? this.neg() : this;
  }

  sign(): number {
    return this.num > 0n ? 1 : this.num < 0n ? -1 : 0;
  }

  isZero(): boolean {
    return this.num === 0n;
  }

  cmp(o: Fraction): number {
    const diff = this.num * o.den - o.num * this.den;
    return diff > 0n ? 1 : diff < 0n ? -1 : 0;
  }

  equals(o: Fraction): boolean {
    return this.num === o.num && this.den === o.den;
  }

  toString(): string {
    return this.den === 1n ? this.num.toString() : `${this.num}/${this.den}`;
  }

  toJSON(): string {
    return this.toString();
  }
}

export function dot(a: Fraction[], b: Fraction[]): Fraction {
  let acc = Fraction.ZERO;
  for (let i = 0; i < a.length; i++) acc = acc.add(a[i].mul(b[i]));
  return acc;
}
