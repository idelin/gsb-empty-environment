/**
 * Exact rational arithmetic backed by bigint.
 * A Rational is always normalized: d > 0 and gcd(|n|, d) = 1.
 */

export interface Rational {
  readonly n: bigint;
  readonly d: bigint;
}

export type RationalInput = Rational | bigint | number | string;

function gcd(a: bigint, b: bigint): bigint {
  a = a < 0n ? -a : a;
  b = b < 0n ? -b : b;
  while (b !== 0n) {
    const t = a % b;
    a = b;
    b = t;
  }
  return a;
}

function normalize(n: bigint, d: bigint): Rational {
  if (d === 0n) throw new RangeError('rational denominator must not be zero');
  if (d < 0n) {
    n = -n;
    d = -d;
  }
  const g = gcd(n, d);
  return { n: n / g, d: d / g };
}

/** Exact decimal/integer/fraction string parsing: "12", "-3", "0.001", "1e-3", "22/7". */
function fromString(s: string): Rational {
  const text = s.trim();
  const frac = /^([+-]?\d+)\s*\/\s*(\d+)$/.exec(text);
  if (frac) return normalize(BigInt(frac[1]), BigInt(frac[2]));
  const dec = /^([+-]?)(\d*)(?:\.(\d*))?(?:[eE]([+-]?\d+))?$/.exec(text);
  if (!dec || (dec[2] === '' && (dec[3] ?? '') === '')) {
    throw new SyntaxError(`cannot parse rational: ${JSON.stringify(s)}`);
  }
  const digits = (dec[2] + (dec[3] ?? '')).replace(/^0+(?=\d)/, '') || '0';
  const exp = BigInt(dec[4] ?? '0') - BigInt(dec[3]?.length ?? 0);
  const sign = dec[1] === '-' ? -1n : 1n;
  const mantissa = sign * BigInt(digits);
  return exp >= 0n
    ? normalize(mantissa * 10n ** exp, 1n)
    : normalize(mantissa, 10n ** -exp);
}

/** Exact conversion of a finite double to a Rational (every double is rational). */
export function fromNumber(x: number): Rational {
  if (!Number.isFinite(x)) {
    throw new RangeError(`cannot convert non-finite number to rational: ${x}`);
  }
  if (Number.isInteger(x) && Math.abs(x) <= Number.MAX_SAFE_INTEGER) {
    return normalize(BigInt(x), 1n);
  }
  const view = new DataView(new ArrayBuffer(8));
  view.setFloat64(0, x);
  const bits = view.getBigUint64(0);
  const sign = (bits >> 63n) !== 0n ? -1n : 1n;
  const expField = Number((bits >> 52n) & 0x7ffn);
  const frac = bits & ((1n << 52n) - 1n);
  let mantissa: bigint;
  let exp: number;
  if (expField === 0) {
    mantissa = frac;
    exp = -1074;
  } else {
    mantissa = frac | (1n << 52n);
    exp = expField - 1075;
  }
  const m = sign * mantissa;
  return exp >= 0 ? normalize(m * (2n ** BigInt(exp)), 1n) : normalize(m, 2n ** BigInt(-exp));
}

/** Build a Rational from num/den, a decimal string, an existing Rational, or a number. */
export function rat(num: RationalInput, den: bigint | number | string = 1n): Rational {
  if (typeof num === 'object') {
    if (den !== 1n) throw new TypeError('denominator not allowed when num is a Rational');
    return num;
  }
  if (typeof num === 'string' && den === 1n) return fromString(num);
  if (typeof num === 'number' && !Number.isInteger(num)) {
    return div(fromNumber(num), rat(den));
  }
  const n = typeof num === 'number' ? BigInt(num) : num;
  const d = typeof den === 'number' ? BigInt(den) : den;
  return normalize(n, d);
}

export function isZero(r: Rational): boolean {
  return r.n === 0n;
}

export function isPositive(r: Rational): boolean {
  return r.n > 0n;
}

export function sign(r: Rational): -1 | 0 | 1 {
  return r.n < 0n ? -1 : r.n > 0n ? 1 : 0;
}

export function cmp(a: Rational, b: Rational): -1 | 0 | 1 {
  const lhs = a.n * b.d;
  const rhs = b.n * a.d;
  return lhs < rhs ? -1 : lhs > rhs ? 1 : 0;
}

export function eq(a: Rational, b: Rational): boolean {
  return a.n === b.n && a.d === b.d;
}

export function add(a: Rational, b: Rational): Rational {
  return normalize(a.n * b.d + b.n * a.d, a.d * b.d);
}

export function sub(a: Rational, b: Rational): Rational {
  return normalize(a.n * b.d - b.n * a.d, a.d * b.d);
}

export function mul(a: Rational, b: Rational): Rational {
  return normalize(a.n * b.n, a.d * b.d);
}

export function div(a: Rational, b: Rational): Rational {
  if (b.n === 0n) throw new RangeError('rational division by zero');
  return normalize(a.n * b.d, a.d * b.n);
}

export function neg(a: Rational): Rational {
  return { n: -a.n, d: a.d };
}

export function abs(a: Rational): Rational {
  return a.n < 0n ? { n: -a.n, d: a.d } : a;
}

export function min(a: Rational, b: Rational): Rational {
  return cmp(a, b) <= 0 ? a : b;
}

export function max(a: Rational, b: Rational): Rational {
  return cmp(a, b) >= 0 ? a : b;
}

/** 10^e as an exact Rational, for any integer e. */
export function pow10(e: number | bigint): Rational {
  const b = BigInt(e);
  return b >= 0n ? { n: 10n ** b, d: 1n } : { n: 1n, d: 10n ** -b };
}

/** ceil(p / q) for q > 0. */
export function ceilDiv(p: bigint, q: bigint): bigint {
  if (q <= 0n) throw new RangeError('ceilDiv requires q > 0');
  return p >= 0n ? (p + q - 1n) / q : -(-p / q);
}

/** floor(p / q) for q > 0. */
export function floorDiv(p: bigint, q: bigint): bigint {
  if (q <= 0n) throw new RangeError('floorDiv requires q > 0');
  return p >= 0n ? p / q : -((-p + q - 1n) / q);
}

/** Smallest integer k with r <= k * step (step > 0). */
export function ceilMultiple(r: Rational, step: Rational): bigint {
  if (step.n <= 0n) throw new RangeError('ceilMultiple requires step > 0');
  return ceilDiv(r.n * step.d, r.d * step.n);
}

/** Largest integer e with 10^e <= r (r > 0). */
export function floorLog10(r: Rational): number {
  if (r.n <= 0n) throw new RangeError('floorLog10 requires a positive value');
  let e = Math.floor(Math.log10(toNumber(r)));
  while (cmp(pow10(e), r) > 0) e -= 1;
  while (cmp(pow10(e + 1), r) <= 0) e += 1;
  return e;
}

/** Smallest integer e with 10^e >= r (r > 0). */
export function ceilLog10(r: Rational): number {
  if (r.n <= 0n) throw new RangeError('ceilLog10 requires a positive value');
  let e = Math.ceil(Math.log10(toNumber(r)));
  while (cmp(pow10(e), r) < 0) e += 1;
  while (cmp(pow10(e - 1), r) >= 0) e -= 1;
  return e;
}

export function toNumber(r: Rational): number {
  return Number(r.n) / Number(r.d);
}

/** Exact finite decimal expansion when the denominator has only factors 2 and 5. */
export function toExactDecimal(r: Rational): string | null {
  let d = r.d;
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
  const places = twos > fives ? twos : fives;
  const scaled = r.n * 2n ** (places - twos) * 5n ** (places - fives);
  const negative = scaled < 0n;
  const digits = (negative ? -scaled : scaled).toString().padStart(Number(places) + 1, '0');
  const intPart = digits.slice(0, digits.length - Number(places));
  const fracPart = digits.slice(digits.length - Number(places)).replace(/0+$/, '');
  const body = fracPart === '' ? intPart : `${intPart}.${fracPart}`;
  return (negative ? '-' : '') + body;
}

/**
 * Human-readable label with at most `sig` significant digits.
 * Falls back to the exact decimal expansion when it is shorter/cleaner,
 * and to "n/d" when no finite decimal exists.
 */
export function formatRational(r: Rational, sig: number): string {
  if (r.n === 0n) return '0';
  if (r.d === 1n) return r.n.toString();
  const exact = toExactDecimal(r);
  const x = toNumber(r);
  let s = x.toPrecision(sig);
  if (s.includes('e')) {
    const [mant, exp] = s.split('e');
    const cleaned = mant.replace(/\.?0+$/, '');
    s = `${cleaned}e${exp.replace('+', '')}`;
  } else if (s.includes('.')) {
    s = s.replace(/\.?0+$/, '');
  }
  if (exact !== null && exact.length <= s.length + 2) return exact;
  if (exact === null && s.length > sig + 6) return `${r.n}/${r.d}`;
  return s;
}
