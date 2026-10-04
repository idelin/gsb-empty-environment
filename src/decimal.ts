/**
 * Exact decimal arithmetic on a BigInt mantissa: value = coeff * 10^exp.
 * No Number intermediates are ever used for payload digits, so values far
 * beyond 2^53 stay exact. Rounding is explicit: every operation that can
 * be inexact takes a scale (fractional decimal digits) and a RoundMode.
 */

export type RoundMode =
  | 'floor' // toward -infinity
  | 'ceil'  // toward +infinity
  | 'down'  // toward zero
  | 'up';   // away from zero

const POW10: bigint[] = [1n];

export function pow10(n: number): bigint {
  if (!Number.isInteger(n) || n < 0) throw new RangeError(`pow10: bad exponent ${n}`);
  while (POW10.length <= n) POW10.push(POW10[POW10.length - 1]! * 10n);
  return POW10[n]!;
}

/** BigInt division with an explicit rounding mode. den must be non-zero. */
export function divWithMode(num: bigint, den: bigint, mode: RoundMode): bigint {
  if (den === 0n) throw new RangeError('division by zero');
  const q = num / den; // truncates toward zero
  const r = num % den;
  if (r === 0n) return q;
  const negative = (num < 0n) !== (den < 0n);
  switch (mode) {
    case 'floor': return negative ? q - 1n : q;
    case 'ceil': return negative ? q : q + 1n;
    case 'down': return q;
    case 'up': return negative ? q - 1n : q + 1n;
  }
}

export class Decimal {
  readonly coeff: bigint;
  readonly exp: number;

  private constructor(coeff: bigint, exp: number) {
    this.coeff = coeff;
    this.exp = exp;
  }

  static readonly ZERO: Decimal = new Decimal(0n, 0);
  static readonly ONE: Decimal = new Decimal(1n, 0);

  static of(coeff: bigint, exp = 0): Decimal {
    return new Decimal(coeff, exp);
  }

  static fromInt(n: number | bigint): Decimal {
    return new Decimal(BigInt(n), 0);
  }

  /** Parses [-]ddd[.ddd][e[-]ddd] directly into a BigInt mantissa. */
  static parse(text: string): Decimal {
    const m = /^([+-]?)(\d+)(?:\.(\d+))?(?:[eE]([+-]?\d+))?$/.exec(text.trim());
    if (!m) throw new SyntaxError(`Decimal.parse: invalid input ${JSON.stringify(text)}`);
    const intPart = m[2]!;
    const fracPart = m[3] ?? '';
    const expPart = m[4] !== undefined ? Number.parseInt(m[4], 10) : 0;
    let coeff = BigInt(intPart + fracPart);
    if (m[1] === '-') coeff = -coeff;
    return new Decimal(coeff, expPart - fracPart.length);
  }

  sign(): -1 | 0 | 1 {
    return this.coeff < 0n ? -1 : this.coeff > 0n ? 1 : 0;
  }

  isZero(): boolean {
    return this.coeff === 0n;
  }

  neg(): Decimal {
    return new Decimal(-this.coeff, this.exp);
  }

  abs(): Decimal {
    return this.coeff < 0n ? new Decimal(-this.coeff, this.exp) : this;
  }

  /** Exact addition. */
  add(o: Decimal): Decimal {
    const m = Math.min(this.exp, o.exp);
    const a = this.coeff * pow10(this.exp - m);
    const b = o.coeff * pow10(o.exp - m);
    return new Decimal(a + b, m);
  }

  /** Exact subtraction. */
  sub(o: Decimal): Decimal {
    return this.add(o.neg());
  }

  /** Exact multiplication. */
  mul(o: Decimal): Decimal {
    return new Decimal(this.coeff * o.coeff, this.exp + o.exp);
  }

  /** Division rounded to `scale` fractional digits under `mode`. */
  div(o: Decimal, scale: number, mode: RoundMode): Decimal {
    if (o.coeff === 0n) throw new RangeError('Decimal.div: division by zero');
    const e = this.exp - o.exp + scale;
    let num = this.coeff;
    let den = o.coeff;
    if (e >= 0) num *= pow10(e);
    else den *= pow10(-e);
    return new Decimal(divWithMode(num, den, mode), -scale);
  }

  /** Rounds this value to `scale` fractional digits under `mode`. */
  round(scale: number, mode: RoundMode): Decimal {
    if (this.exp >= -scale) return new Decimal(this.coeff * pow10(this.exp + scale), -scale);
    const shift = -scale - this.exp;
    return new Decimal(divWithMode(this.coeff, pow10(shift), mode), -scale);
  }

  /** Exact halving (multiply by 5 and shift when the mantissa is odd). */
  divExact2(): Decimal {
    if (this.coeff % 2n === 0n) return new Decimal(this.coeff / 2n, this.exp);
    return new Decimal(this.coeff * 5n, this.exp - 1);
  }

  cmp(o: Decimal): number {
    const sa = this.sign();
    const sb = o.sign();
    if (sa !== sb) return sa < sb ? -1 : 1;
    if (sa === 0) return 0;
    const m = Math.min(this.exp, o.exp);
    const a = this.coeff * pow10(this.exp - m);
    const b = o.coeff * pow10(o.exp - m);
    return a < b ? -1 : a > b ? 1 : 0;
  }

  eq(o: Decimal): boolean {
    return this.cmp(o) === 0;
  }

  static min(a: Decimal, b: Decimal): Decimal {
    return a.cmp(b) <= 0 ? a : b;
  }

  static max(a: Decimal, b: Decimal): Decimal {
    return a.cmp(b) >= 0 ? a : b;
  }

  toString(): string {
    if (this.coeff === 0n) return '0';
    const neg = this.coeff < 0n;
    const digits = (neg ? -this.coeff : this.coeff).toString();
    let body: string;
    if (this.exp >= 0) {
      body = digits + '0'.repeat(this.exp);
    } else {
      const point = digits.length + this.exp;
      if (point > 0) body = `${digits.slice(0, point)}.${digits.slice(point)}`;
      else body = `0.${'0'.repeat(-point)}${digits}`;
    }
    return (neg ? '-' : '') + body;
  }
}
