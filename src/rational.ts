/**
 * Exact rational arithmetic on BigInt. Large values never pass through Number.
 */
export interface Rational {
  readonly n: bigint; // numerator (any sign)
  readonly d: bigint; // denominator (always > 0), fraction kept reduced
}

export function gcd(a: bigint, b: bigint): bigint {
  if (a < 0n) a = -a;
  if (b < 0n) b = -b;
  while (b !== 0n) {
    const t = a % b;
    a = b;
    b = t;
  }
  return a;
}

export function make(n: bigint, d: bigint = 1n): Rational {
  if (d === 0n) throw new Error('rational: denominator is zero');
  if (d < 0n) {
    n = -n;
    d = -d;
  }
  const g = gcd(n, d);
  if (g > 1n) {
    n /= g;
    d /= g;
  }
  return { n, d };
}

export const ZERO: Rational = { n: 0n, d: 1n };
export const ONE: Rational = { n: 1n, d: 1n };

export function fromBigInt(v: bigint): Rational {
  return make(v);
}

/** Parses "a/b", "12", "-3", "1.25", "-0.005". No exponent notation, no Number detour. */
export function parse(text: string): Rational {
  const t = text.trim();
  let m = /^([+-]?\d+)\/([+-]?\d+)$/.exec(t);
  if (m) return make(BigInt(m[1]), BigInt(m[2]));
  m = /^([+-]?)(\d+)(?:\.(\d+))?$/.exec(t);
  if (m) {
    const sign = m[1] === '-' ? -1n : 1n;
    const intPart = BigInt(m[2]);
    const frac = m[3] ?? '';
    const scale = 10n ** BigInt(frac.length);
    const fracBig = frac.length > 0 ? BigInt(frac) : 0n;
    return make(sign * (intPart * scale + fracBig), scale);
  }
  throw new Error(`rational: cannot parse ${JSON.stringify(text)}`);
}

export function add(a: Rational, b: Rational): Rational {
  return make(a.n * b.d + b.n * a.d, a.d * b.d);
}
export function sub(a: Rational, b: Rational): Rational {
  return make(a.n * b.d - b.n * a.d, a.d * b.d);
}
export function mul(a: Rational, b: Rational): Rational {
  return make(a.n * b.n, a.d * b.d);
}
export function div(a: Rational, b: Rational): Rational {
  if (b.n === 0n) throw new Error('rational: division by zero');
  return make(a.n * b.d, a.d * b.n);
}
export function neg(a: Rational): Rational {
  return make(-a.n, a.d);
}
export function abs(a: Rational): Rational {
  return a.n < 0n ? make(-a.n, a.d) : a;
}
export function cmp(a: Rational, b: Rational): number {
  const l = a.n * b.d;
  const r = b.n * a.d;
  return l < r ? -1 : l > r ? 1 : 0;
}
export function eq(a: Rational, b: Rational): boolean {
  return a.n === b.n && a.d === b.d;
}
export function min(a: Rational, b: Rational): Rational {
  return cmp(a, b) <= 0 ? a : b;
}
export function max(a: Rational, b: Rational): Rational {
  return cmp(a, b) >= 0 ? a : b;
}
export function isZero(a: Rational): boolean {
  return a.n === 0n;
}
export function isInteger(a: Rational): boolean {
  return a.d === 1n;
}
export function sign(a: Rational): number {
  return a.n < 0n ? -1 : a.n > 0n ? 1 : 0;
}

/** floor of a rational as bigint. */
export function floorBig(r: Rational): bigint {
  const q = r.n / r.d;
  const rem = r.n % r.d;
  return rem !== 0n && rem < 0n ? q - 1n : q;
}
export function ceilBig(r: Rational): bigint {
  return -floorBig(make(-r.n, r.d));
}

export function toString(r: Rational): string {
  return r.d === 1n ? r.n.toString() : `${r.n}/${r.d}`;
}

function floorDivBig(n: bigint, d: bigint): bigint {
  const q = n / d;
  const rem = n % d;
  return rem !== 0n && rem < 0n ? q - 1n : q;
}

/** Decimal string rounded half-up at `places` fractional digits (display only). */
export function toDecimal(r: Rational, places = 6): string {
  const scale = 10n ** BigInt(places);
  const num = r.n * scale;
  const q = floorDivBig(2n * num + r.d, 2n * r.d);
  const negative = q < 0n;
  const aq = negative ? -q : q;
  const intPart = aq / scale;
  const fracPart = aq % scale;
  const frac = fracPart
    .toString()
    .padStart(places, '0')
    .replace(/0+$/, '');
  return (negative ? '-' : '') + intPart.toString() + (frac.length > 0 ? '.' + frac : '');
}

/** Exact decimal when the denominator only has factors 2 and 5, else "n/d". */
export function toDisplayString(r: Rational): string {
  if (r.d === 1n) return r.n.toString();
  let d = r.d;
  let c2 = 0;
  let c5 = 0;
  while (d % 2n === 0n) {
    d /= 2n;
    c2++;
  }
  while (d % 5n === 0n) {
    d /= 5n;
    c5++;
  }
  if (d === 1n) return toDecimal(r, Math.max(c2, c5));
  return `${r.n}/${r.d}`;
}
