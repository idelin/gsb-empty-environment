import {
  Rational,
  cmp,
  div,
  formatRational,
  makeRational,
  mul,
  pow10,
  sub,
  toNumber,
} from './rational.js';

/**
 * A single tick: the exact domain value it represents, the unique display
 * label derived from that value, and its mapped pixel position.
 */
export interface Tick {
  readonly value: Rational;
  readonly label: string;
  readonly pixel: number;
}

const MAX_TICKS = 10000;
const TEN = makeRational(10n);

/**
 * Generate ticks for a sorted domain `[lo, hi]` (`lo < hi`).
 *
 * Every tick value is an exact rational (a multiple of a "nice" power-of-ten
 * step in linear mode, or `m * 10^k` in log mode). Labels are canonical exact
 * representations of the values, so distinct values can never share a label;
 * a defensive check throws if a duplicate ever slips through.
 */
export function computeTicks(
  mode: 'linear' | 'log',
  domain: readonly [Rational, Rational],
  map: (value: Rational) => number,
  targetCount: number = 10,
): Tick[] {
  const [lo, hi] = domain;
  const count = Math.max(1, Math.floor(targetCount));
  const values = mode === 'log' ? logTickValues(lo, hi, count) : linearTickValues(lo, hi, count);
  const labels = values.map(formatRational);
  const seen = new Set<string>();
  for (const label of labels) {
    if (seen.has(label)) throw new Error(`duplicate tick label: ${label}`);
    seen.add(label);
  }
  return values.map((value, index) => ({ value, label: labels[index], pixel: map(value) }));
}

function linearTickValues(lo: Rational, hi: Rational, targetCount: number): Rational[] {
  const span = sub(hi, lo);
  const rawStep = div(span, makeRational(BigInt(targetCount)));
  const step = niceStep(rawStep);
  const k0 = ceilRatio(lo, step);
  const k1 = floorRatio(hi, step);
  const values: Rational[] = [];
  for (let k = k0; k <= k1; k += 1n) {
    values.push(mul(makeRational(k), step));
    if (values.length > MAX_TICKS) break;
  }
  if (values.length === 0) return [lo, hi];
  return values;
}

/** Smallest "nice" step (m * 10^e, m in {1, 2, 5}) that is >= rawStep. */
function niceStep(rawStep: Rational): Rational {
  const approx = toNumber(rawStep);
  if (!(approx > 0) || !Number.isFinite(approx)) return rawStep;
  let e = Math.floor(Math.log10(approx));
  let p = pow10(e);
  while (cmp(p, rawStep) > 0) {
    e -= 1;
    p = pow10(e);
  }
  while (cmp(mul(p, TEN), rawStep) <= 0) {
    e += 1;
    p = pow10(e);
  }
  const factor = toNumber(div(rawStep, p));
  const m = factor <= 1 ? 1 : factor <= 2 ? 2 : factor <= 5 ? 5 : 10;
  return mul(p, makeRational(BigInt(m)));
}

function logTickValues(lo: Rational, hi: Rational, targetCount: number): Rational[] {
  const loNum = toNumber(lo);
  const hiNum = toNumber(hi);
  if (!(loNum > 0) || !Number.isFinite(loNum) || !Number.isFinite(hiNum)) {
    return linearTickValues(lo, hi, targetCount);
  }
  // Exact decade exponents: 10^eLo <= lo < 10^(eLo+1), same for hi.
  let eLo = Math.floor(Math.log10(loNum));
  while (cmp(pow10(eLo), lo) > 0) eLo -= 1;
  while (cmp(pow10(eLo + 1), lo) <= 0) eLo += 1;
  let eHi = Math.floor(Math.log10(hiNum));
  while (cmp(pow10(eHi), hi) > 0) eHi -= 1;
  while (cmp(pow10(eHi + 1), hi) <= 0) eHi += 1;

  const decades = eHi - eLo + 1;
  const mantissas =
    decades >= targetCount ? [1] : decades * 3 >= targetCount ? [1, 2, 5] : [1, 2, 3, 4, 5, 6, 7, 8, 9];
  const values: Rational[] = [];
  for (let e = eLo; e <= eHi; e += 1) {
    const power = pow10(e);
    for (const m of mantissas) {
      const value = mul(power, makeRational(BigInt(m)));
      if (cmp(value, lo) >= 0 && cmp(value, hi) <= 0) values.push(value);
    }
  }
  // Too few powers of ten inside the span: fall back to linear-style ticks.
  if (values.length < 3) return linearTickValues(lo, hi, targetCount);
  return values;
}

function floorDivBig(a: bigint, b: bigint): bigint {
  // Requires b > 0; bigint division truncates toward zero.
  const q = a / b;
  return a % b !== 0n && a < 0n ? q - 1n : q;
}

function ceilDivBig(a: bigint, b: bigint): bigint {
  return -floorDivBig(-a, b);
}

/** Largest integer k with k * step <= r (step > 0). */
function floorRatio(r: Rational, step: Rational): bigint {
  return floorDivBig(r.num * step.den, r.den * step.num);
}

/** Smallest integer k with k * step >= r (step > 0). */
function ceilRatio(r: Rational, step: Rational): bigint {
  return ceilDivBig(r.num * step.den, r.den * step.num);
}
