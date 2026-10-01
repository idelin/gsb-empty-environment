// Exact rational arithmetic on BigInt. No Number is ever used to carry a
// magnitude: every quantity stays in BigInt numerator/denominator form and
// rounding happens exactly once, at an explicit call site with an explicit
// mode.

export type RoundingMode =
  | 'floor' // toward negative infinity
  | 'ceil' // toward positive infinity
  | 'trunc' // toward zero
  | 'nearest-half-up' // nearest; exact ties go toward positive infinity
  | 'nearest-half-away' // nearest; exact ties go away from zero
  | 'nearest-half-even'; // nearest; exact ties go to the even neighbor

export const ROUNDING_MODES: readonly RoundingMode[] = [
  'floor',
  'ceil',
  'trunc',
  'nearest-half-up',
  'nearest-half-away',
  'nearest-half-even',
];

// Integer division with an explicit rounding mode. Denominator must be > 0.
export function divRound(numerator: bigint, denominator: bigint, mode: RoundingMode): bigint {
  if (denominator <= 0n) throw new RangeError('divRound: denominator must be positive');
  const q = numerator / denominator; // truncating
  const r = numerator % denominator; // sign follows numerator
  if (r === 0n) return q;
  switch (mode) {
    case 'floor':
      return r < 0n ? q - 1n : q;
    case 'ceil':
      return r > 0n ? q + 1n : q;
    case 'trunc':
      return q;
    case 'nearest-half-up':
    case 'nearest-half-away':
    case 'nearest-half-even': {
      const absR = r < 0n ? -r : r;
      const twice = absR * 2n;
      if (twice < denominator) return q;
      const away = r > 0n ? q + 1n : q - 1n;
      if (twice > denominator) return away;
      if (mode === 'nearest-half-up') return r > 0n ? q + 1n : q;
      if (mode === 'nearest-half-away') return away;
      return q % 2n === 0n ? q : away; // half-even
    }
  }
}

// Normalized rational: d > 0, gcd(|n|, d) = 1, zero is 0/1.
export type Rational = { readonly n: bigint; readonly d: bigint };

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

export function rational(n: bigint, d: bigint = 1n): Rational {
  if (d === 0n) throw new RangeError('rational: zero denominator');
  let num = n;
  let den = d;
  if (den < 0n) {
    num = -num;
    den = -den;
  }
  if (num === 0n) return { n: 0n, d: 1n };
  const g = gcd(num, den);
  return { n: num / g, d: den / g };
}

export const addR = (a: Rational, b: Rational): Rational =>
  rational(a.n * b.d + b.n * a.d, a.d * b.d);

export const subR = (a: Rational, b: Rational): Rational =>
  rational(a.n * b.d - b.n * a.d, a.d * b.d);

export const mulR = (a: Rational, b: Rational): Rational => rational(a.n * b.n, a.d * b.d);

export const divR = (a: Rational, b: Rational): Rational => {
  if (b.n === 0n) throw new RangeError('divR: division by zero');
  return rational(a.n * b.d, a.d * b.n);
};

export const negR = (a: Rational): Rational => ({ n: -a.n, d: a.d });

export const absR = (a: Rational): Rational => (a.n < 0n ? { n: -a.n, d: a.d } : a);

export function cmpR(a: Rational, b: Rational): -1 | 0 | 1 {
  const lhs = a.n * b.d;
  const rhs = b.n * a.d;
  return lhs < rhs ? -1 : lhs > rhs ? 1 : 0;
}

export function ratToString(r: Rational): string {
  return r.d === 1n ? r.n.toString() : `${r.n}/${r.d}`;
}

// Exact decimal rendering with `places` fractional digits, rounded
// nearest-half-even at the last displayed digit. Never uses Number.
export function ratToDecimal(r: Rational, places = 6): string {
  if (!Number.isSafeInteger(places) || places < 0) throw new RangeError('places must be a safe non-negative integer');
  const scale = 10n ** BigInt(places);
  const scaled = divRound(r.n * scale, r.d, 'nearest-half-even');
  const neg = scaled < 0n;
  const abs = neg ? -scaled : scaled;
  const intPart = abs / scale;
  if (places === 0) return `${neg ? '-' : ''}${intPart}`;
  const fracPart = (abs % scale).toString().padStart(places, '0');
  return `${neg ? '-' : ''}${intPart}.${fracPart}`;
}
