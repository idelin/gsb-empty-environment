/**
 * Exact rational arithmetic on BigInt. No Number is ever used to carry a
 * value: decimals are parsed straight into (BigInt numerator, BigInt
 * denominator) and every operation is exact.
 */

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
  readonly den: bigint; // always > 0, gcd(|num|, den) = 1

  private constructor(num: bigint, den: bigint) {
    if (den === 0n) throw new Error("Rational: zero denominator");
    if (den < 0n) {
      num = -num;
      den = -den;
    }
    const g = gcd(num, den);
    this.num = num / g;
    this.den = den / g;
  }

  static of(num: bigint, den: bigint = 1n): Rational {
    return new Rational(num, den);
  }

  static zero(): Rational {
    return new Rational(0n, 1n);
  }

  static one(): Rational {
    return new Rational(1n, 1n);
  }

  /**
   * Exact parse. Accepted forms:
   *   "p/q"            fraction of integers
   *   "123"            integer
   *   "-12.345"        finite decimal
   *   "1.25e3"         decimal with base-10 exponent
   */
  static parse(src: string): Rational {
    const s = src.trim();
    const frac = /^([+-]?\d+)\s*\/\s*([+-]?\d+)$/.exec(s);
    if (frac) return Rational.of(BigInt(frac[1]), BigInt(frac[2]));
    const dec = /^([+-]?)(\d*)(?:\.(\d*))?(?:[eE]([+-]?\d+))?$/.exec(s);
    if (!dec || (dec[2] === "" && (dec[3] ?? "") === "")) {
      throw new Error(`Rational.parse: cannot parse ${JSON.stringify(src)}`);
    }
    const sign = dec[1] === "-" ? -1n : 1n;
    const intPart = dec[2] === "" ? "0" : dec[2];
    const fracPart = dec[3] ?? "";
    const exp = dec[4] ? BigInt(dec[4]) : 0n;
    const digits = BigInt(intPart + fracPart);
    const scale = 10n ** BigInt(fracPart.length);
    let num = sign * digits;
    let den = scale;
    if (exp >= 0n) num *= 10n ** exp;
    else den *= 10n ** -exp;
    return Rational.of(num, den);
  }

  add(o: Rational): Rational {
    return Rational.of(this.num * o.den + o.num * this.den, this.den * o.den);
  }
  sub(o: Rational): Rational {
    return Rational.of(this.num * o.den - o.num * this.den, this.den * o.den);
  }
  neg(): Rational {
    return Rational.of(-this.num, this.den);
  }
  mul(o: Rational): Rational {
    return Rational.of(this.num * o.num, this.den * o.den);
  }
  div(o: Rational): Rational {
    if (o.num === 0n) throw new Error("Rational.div: division by zero");
    return Rational.of(this.num * o.den, this.den * o.num);
  }
  abs(): Rational {
    return this.num < 0n ? this.neg() : this;
  }
  sign(): -1 | 0 | 1 {
    return this.num < 0n ? -1 : this.num > 0n ? 1 : 0;
  }
  cmp(o: Rational): -1 | 0 | 1 {
    const d = this.num * o.den - o.num * this.den;
    return d < 0n ? -1 : d > 0n ? 1 : 0;
  }
  equals(o: Rational): boolean {
    return this.num === o.num && this.den === o.den;
  }
  isZero(): boolean {
    return this.num === 0n;
  }
  isInteger(): boolean {
    return this.den === 1n;
  }

  /** Exact canonical string: "n" for integers, "p/q" otherwise. */
  toString(): string {
    return this.den === 1n ? this.num.toString() : `${this.num}/${this.den}`;
  }

  /** True when the decimal expansion terminates (den = 2^a * 5^b). */
  isTerminating(): boolean {
    let d = this.den;
    while (d % 2n === 0n) d /= 2n;
    while (d % 5n === 0n) d /= 5n;
    return d === 1n;
  }

  /**
   * Exact decimal string when terminating; otherwise the exact fraction
   * (never a truncated/approximate decimal).
   */
  toDecimalString(): string {
    if (!this.isTerminating()) return this.toString();
    let d = this.den;
    let twos = 0n;
    let fives = 0n;
    while (d % 2n === 0n) {
      d /= 2n;
      twos++;
    }
    while (d % 5n === 0n) {
      d /= 5n;
      fives++;
    }
    const places = twos > fives ? twos : fives;
    const scaled = this.num * 2n ** (places - twos) * 5n ** (places - fives);
    const neg = scaled < 0n;
    const digits = (neg ? -scaled : scaled).toString().padStart(Number(places) + 1, "0");
    const cut = digits.length - Number(places);
    const intPart = digits.slice(0, cut);
    const fracPart = digits.slice(cut).replace(/0+$/, "");
    return (neg ? "-" : "") + intPart + (fracPart ? "." + fracPart : "");
  }

  toJSON(): string {
    return this.toString();
  }
}

/** Mathematical floor division for BigInt (b > 0). */
export function floorDiv(a: bigint, b: bigint): bigint {
  const q = a / b;
  return a % b !== 0n && a < 0n ? q - 1n : q;
}
