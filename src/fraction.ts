// Exact rational arithmetic backed by BigInt.
// A Fraction is always kept normalized: d > 0 and gcd(|n|, d) = 1.

export interface Fraction {
  readonly n: bigint;
  readonly d: bigint;
}

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

export function makeFraction(n: bigint, d: bigint): Fraction {
  if (d === 0n) {
    throw new Error('fraction with zero denominator');
  }
  let num = n;
  let den = d;
  if (den < 0n) {
    num = -num;
    den = -den;
  }
  const g = gcd(num, den);
  return { n: num / g, d: den / g };
}

export function fractionFromInt(v: bigint): Fraction {
  return { n: v, d: 1n };
}

export function fracAdd(a: Fraction, b: Fraction): Fraction {
  return makeFraction(a.n * b.d + b.n * a.d, a.d * b.d);
}

export function fracSub(a: Fraction, b: Fraction): Fraction {
  return makeFraction(a.n * b.d - b.n * a.d, a.d * b.d);
}

export function fracMul(a: Fraction, b: Fraction): Fraction {
  return makeFraction(a.n * b.n, a.d * b.d);
}

export function fracDiv(a: Fraction, b: Fraction): Fraction {
  return makeFraction(a.n * b.d, a.d * b.n);
}

export function fracNeg(a: Fraction): Fraction {
  return { n: -a.n, d: a.d };
}

export function fracCompare(a: Fraction, b: Fraction): number {
  const lhs = a.n * b.d;
  const rhs = b.n * a.d;
  return lhs < rhs ? -1 : lhs > rhs ? 1 : 0;
}

export function fracMin(a: Fraction, b: Fraction): Fraction {
  return fracCompare(a, b) <= 0 ? a : b;
}

export function fracMax(a: Fraction, b: Fraction): Fraction {
  return fracCompare(a, b) >= 0 ? a : b;
}

export function fracToString(f: Fraction): string {
  return f.d === 1n ? f.n.toString() : f.n.toString() + '/' + f.d.toString();
}

export function fracToNumber(f: Fraction): number {
  return Number(f.n) / Number(f.d);
}
