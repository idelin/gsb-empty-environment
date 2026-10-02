/**
 * Exact rational numbers on BigInt. Values never pass through Number,
 * so arbitrarily large magnitudes stay exact.
 *
 * Invariants:
 *  - den > 0
 *  - gcd(|num|, den) = 1
 *  - zero is always 0/1  (negative-zero policy: zero carries no sign)
 */
export interface Rat {
  readonly n: bigint;
  readonly d: bigint;
}

function gcd(a: bigint, b: bigint): bigint {
  a = a < 0n ? -a : a;
  b = b < 0n ? -b : b;
  while (b !== 0n) {
    const t = a % b;
    a = b;
    b = t;
  }
  return a === 0n ? 1n : a;
}

export function rat(n: bigint, d: bigint = 1n): Rat {
  if (d === 0n) throw new Error("rational with zero denominator");
  if (d < 0n) {
    n = -n;
    d = -d;
  }
  if (n === 0n) return { n: 0n, d: 1n }; // negative-zero policy: 0 has no sign
  const g = gcd(n, d);
  return { n: n / g, d: d / g };
}

export const ZERO: Rat = rat(0n);
export const ONE: Rat = rat(1n);

/** Parse an exact decimal/integer/fraction string, e.g. "0.1", "-3", "22/7", "-0". */
export function fromString(s: string): Rat {
  const frac = s.trim().match(/^([+-]?\d+)\/(\d+)$/);
  if (frac) return rat(BigInt(frac[1]), BigInt(frac[2]));
  const dec = s.trim().match(/^([+-]?)(\d*)(?:\.(\d+))?$/);
  if (!dec || (dec[2] === "" && dec[3] === undefined))
    throw new Error(`not a decimal: ${s}`);
  const digits = (dec[2] || "0") + (dec[3] || "");
  const scale = 10n ** BigInt(dec[3]?.length ?? 0);
  const value = rat(BigInt(digits), scale);
  return dec[1] === "-" ? neg(value) : value;
}

export function add(a: Rat, b: Rat): Rat {
  return rat(a.n * b.d + b.n * a.d, a.d * b.d);
}
export function sub(a: Rat, b: Rat): Rat {
  return rat(a.n * b.d - b.n * a.d, a.d * b.d);
}
export function mul(a: Rat, b: Rat): Rat {
  return rat(a.n * b.n, a.d * b.d);
}
export function div(a: Rat, b: Rat): Rat {
  if (b.n === 0n) throw new Error("division by zero rational");
  return rat(a.n * b.d, a.d * b.n);
}
export function neg(a: Rat): Rat {
  return rat(-a.n, a.d);
}
export function abs(a: Rat): Rat {
  return a.n < 0n ? { n: -a.n, d: a.d } : a;
}
export function inv(a: Rat): Rat {
  return div(ONE, a);
}

/** -1, 0, 1 */
export function cmp(a: Rat, b: Rat): number {
  const l = a.n * b.d;
  const r = b.n * a.d;
  return l < r ? -1 : l > r ? 1 : 0;
}
export function eq(a: Rat, b: Rat): boolean {
  return a.n === b.n && a.d === b.d;
}
export function isZero(a: Rat): boolean {
  return a.n === 0n;
}
export function sign(a: Rat): number {
  return a.n < 0n ? -1 : a.n > 0n ? 1 : 0;
}
export function min(a: Rat, b: Rat): Rat {
  return cmp(a, b) <= 0 ? a : b;
}
export function max(a: Rat, b: Rat): Rat {
  return cmp(a, b) >= 0 ? a : b;
}

/** Floor to the nearest integer towards -inf. */
export function floorRat(a: Rat): bigint {
  const q = a.n / a.d;
  const r = a.n % a.d;
  return r !== 0n && a.n < 0n ? q - 1n : q;
}
/** Ceil to the nearest integer towards +inf. */
export function ceilRat(a: Rat): bigint {
  return -floorRat(neg(a));
}

/** Exact decimal expansion when the denominator only has factors 2 and 5. */
function exactDecimal(a: Rat): string | null {
  let d = a.d;
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
  if (d !== 1n) return null;
  const places = twos > fives ? twos : fives;
  const scaled = a.n * 2n ** (places - twos) * 5n ** (places - fives);
  const negative = scaled < 0n;
  const digits = (negative ? -scaled : scaled).toString().padStart(Number(places) + 1, "0");
  const ip = digits.slice(0, digits.length - Number(places));
  const fp = digits.slice(digits.length - Number(places)).replace(/0+$/, "");
  return (negative ? "-" : "") + ip + (fp ? "." + fp : "");
}

/** Exact string: integer, finite decimal, or "num/den". Never prints "-0". */
export function toString(a: Rat): string {
  if (a.n === 0n) return "0";
  if (a.d === 1n) return a.n.toString();
  return exactDecimal(a) ?? `${a.n}/${a.d}`;
}
