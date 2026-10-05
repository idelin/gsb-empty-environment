const bigAbs = (value: bigint): bigint => (value < 0n ? -value : value);

function bigGcd(a: bigint, b: bigint): bigint {
  let x = bigAbs(a);
  let y = bigAbs(b);
  while (y !== 0n) {
    const r = x % y;
    x = y;
    y = r;
  }
  return x === 0n ? 1n : x;
}

/**
 * Exact rational number backed by bigints.
 * Always normalized: den > 0 and gcd(|num|, den) = 1,
 * so two equal rationals have identical num/den and string keys.
 */
export class Rational {
  private constructor(
    readonly num: bigint,
    readonly den: bigint,
  ) {}

  static ratio(num: bigint, den: bigint): Rational {
    if (den === 0n) {
      throw new Error("Rational: denominator must not be zero");
    }
    let n = num;
    let d = den;
    if (d < 0n) {
      n = -n;
      d = -d;
    }
    const g = bigGcd(n, d);
    return new Rational(n / g, d / g);
  }

  static of(value: number | bigint): Rational {
    if (typeof value === "bigint") {
      return new Rational(value, 1n);
    }
    if (!Number.isFinite(value)) {
      throw new Error(`Rational: cannot represent ${value}`);
    }
    if (Number.isInteger(value)) {
      return new Rational(BigInt(value), 1n);
    }
    const match = /^(-?)(\d+)(?:\.(\d+))?(?:[eE]([+-]?\d+))?$/.exec(value.toString());
    if (!match) {
      throw new Error(`Rational: cannot parse ${value}`);
    }
    const sign = match[1] === "-" ? "-" : "";
    const digits = (match[2] ?? "0") + (match[3] ?? "");
    const exponent = (match[4] ? BigInt(match[4]) : 0n) - BigInt(match[3]?.length ?? 0);
    let n = BigInt(sign + digits);
    let d = 1n;
    if (exponent >= 0n) {
      n *= 10n ** exponent;
    } else {
      d = 10n ** -exponent;
    }
    return Rational.ratio(n, d);
  }

  get isZero(): boolean {
    return this.num === 0n;
  }

  neg(): Rational {
    return Rational.ratio(-this.num, this.den);
  }

  add(other: Rational): Rational {
    return Rational.ratio(
      this.num * other.den + other.num * this.den,
      this.den * other.den,
    );
  }

  sub(other: Rational): Rational {
    return this.add(other.neg());
  }

  mul(other: Rational): Rational {
    return Rational.ratio(this.num * other.num, this.den * other.den);
  }

  div(other: Rational): Rational {
    if (other.isZero) {
      throw new Error("Rational: division by zero");
    }
    return Rational.ratio(this.num * other.den, this.den * other.num);
  }

  compare(other: Rational): number {
    const diff = this.num * other.den - other.num * this.den;
    return diff < 0n ? -1 : diff > 0n ? 1 : 0;
  }

  equals(other: Rational): boolean {
    return this.num === other.num && this.den === other.den;
  }

  toNumber(): number {
    return Number(this.num) / Number(this.den);
  }

  toString(): string {
    return this.den === 1n ? this.num.toString() : `${this.num}/${this.den}`;
  }

  toJSON(): string {
    return this.toString();
  }
}
