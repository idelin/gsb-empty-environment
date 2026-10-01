// Exact rational arithmetic on BigInt. No Number is ever used for values,
// so magnitudes far beyond 2^53 stay exact.
export class Rat {
  readonly n: bigint;
  readonly d: bigint;

  private constructor(n: bigint, d: bigint) {
    this.n = n;
    this.d = d;
  }

  static of(n: bigint, d: bigint = 1n): Rat {
    if (d === 0n) throw new Error('Rat: zero denominator');
    if (d < 0n) { n = -n; d = -d; }
    const g = gcd(n < 0n ? -n : n, d);
    return new Rat(n / g, d / g);
  }

  static zero(): Rat { return Rat.of(0n); }
  static one(): Rat { return Rat.of(1n); }

  // Parses "3", "-2/5", "3.14", "0.001". Decimal strings become exact
  // rationals (3.14 -> 157/50); nothing passes through Number.
  static parse(s: string): Rat {
    const t = s.trim();
    if (/^[+-]?\d+$/.test(t)) return Rat.of(BigInt(t));
    const frac = t.match(/^([+-]?\d+)\/(\d+)$/);
    if (frac) return Rat.of(BigInt(frac[1]), BigInt(frac[2]));
    const dec = t.match(/^([+-]?)(\d*)\.(\d+)$/);
    if (dec) {
      const sign = dec[1] === '-' ? -1n : 1n;
      const intPart = dec[2] === '' ? 0n : BigInt(dec[2]);
      const fracDigits = dec[3];
      const scale = 10n ** BigInt(fracDigits.length);
      return Rat.of(sign * (intPart * scale + BigInt(fracDigits)), scale);
    }
    throw new Error(`Rat.parse: cannot parse ${JSON.stringify(s)}`);
  }

  add(o: Rat): Rat { return Rat.of(this.n * o.d + o.n * this.d, this.d * o.d); }
  sub(o: Rat): Rat { return Rat.of(this.n * o.d - o.n * this.d, this.d * o.d); }
  mul(o: Rat): Rat { return Rat.of(this.n * o.n, this.d * o.d); }
  div(o: Rat): Rat {
    if (o.n === 0n) throw new Error('Rat: division by zero');
    return Rat.of(this.n * o.d, this.d * o.n);
  }
  neg(): Rat { return Rat.of(-this.n, this.d); }
  abs(): Rat { return this.n < 0n ? this.neg() : this; }

  cmp(o: Rat): number {
    const l = this.n * o.d;
    const r = o.n * this.d;
    return l < r ? -1 : l > r ? 1 : 0;
  }
  eq(o: Rat): boolean { return this.n === o.n && this.d === o.d; }
  lt(o: Rat): boolean { return this.cmp(o) < 0; }
  lte(o: Rat): boolean { return this.cmp(o) <= 0; }
  gt(o: Rat): boolean { return this.cmp(o) > 0; }
  gte(o: Rat): boolean { return this.cmp(o) >= 0; }
  isZero(): boolean { return this.n === 0n; }
  sign(): number { return this.n < 0n ? -1 : this.n > 0n ? 1 : 0; }

  // Largest integer <= this.
  floor(): bigint {
    const q = this.n / this.d;
    return this.n % this.d === 0n || this.n >= 0n ? q : q - 1n;
  }
  // Smallest integer >= this.
  ceil(): bigint { return -this.neg().floor(); }

  toString(): string {
    return this.d === 1n ? this.n.toString() : `${this.n}/${this.d}`;
  }

  // Exact decimal expansion when the denominator only has factors 2 and 5,
  // otherwise a fraction string. Never a float.
  toDecimalString(): string {
    let den = this.d;
    let twos = 0n, fives = 0n;
    while (den % 2n === 0n) { den /= 2n; twos++; }
    while (den % 5n === 0n) { den /= 5n; fives++; }
    if (den !== 1n) return this.toString();
    const places = twos > fives ? twos : fives;
    const scaled = this.n * (2n ** (places - twos)) * (5n ** (places - fives));
    const neg = scaled < 0n;
    const digits = (neg ? -scaled : scaled).toString().padStart(Number(places) + 1, '0');
    const split = digits.length - Number(places);
    const out = places === 0n ? digits : `${digits.slice(0, split)}.${digits.slice(split)}`;
    return (neg ? '-' : '') + out;
  }
}

export function gcd(a: bigint, b: bigint): bigint {
  while (b !== 0n) { const t = a % b; a = b; b = t; }
  return a === 0n ? 1n : a;
}

export function ratMin(a: Rat, b: Rat): Rat { return a.lte(b) ? a : b; }
export function ratMax(a: Rat, b: Rat): Rat { return a.gte(b) ? a : b; }
