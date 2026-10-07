/**
 * Linear and log scales from an exact rational domain to a pixel range.
 *
 * Design contract:
 * - `map` and `invert` never clamp; `clip` is a separate explicit operation.
 * - Inverted pixel ranges (p0 > p1) and inverted domains (d0 > d1) are legal.
 * - Log mode explicitly rejects non-positive values (RangeError).
 * - Ticks carry both a display `label` (unique within one `ticks()` call)
 *   and the exact rational `value`.
 */

import {
  type Rational,
  type RationalInput,
  abs,
  add,
  ceilLog10,
  ceilMultiple,
  cmp,
  div,
  floorLog10,
  formatRational,
  fromNumber,
  isPositive,
  isZero,
  mul,
  pow10,
  rat,
  sign,
  sub,
  toNumber,
} from './rational.ts';

export type ScaleMode = 'linear' | 'log';

export interface Tick {
  /** Exact rational value in the domain. */
  readonly value: Rational;
  /** Display label; guaranteed unique within a single ticks() result. */
  readonly label: string;
}

export interface ScaleOptions {
  readonly domain: readonly [RationalInput, RationalInput];
  readonly range: readonly [number, number];
  readonly mode?: ScaleMode;
  /** Log base, default 10. */
  readonly base?: number;
}

export interface Scale {
  readonly mode: ScaleMode;
  readonly base: number;
  /** Domain endpoints as given (may be inverted). */
  readonly domain: readonly [Rational, Rational];
  /** Pixel range as given (may be inverted). */
  readonly range: readonly [number, number];
  /** Domain value -> pixel. Unclamped. Throws in log mode for value <= 0. */
  map(value: RationalInput): number;
  /** Pixel -> exact rational domain value. Unclamped. */
  invert(pixel: number): Rational;
  /** Clamp a pixel into the (possibly inverted) range. Separate from invert. */
  clip(pixel: number): number;
  /** Nice ticks inside the domain with unique labels and exact values. */
  ticks(count?: number): readonly Tick[];
}

function describe(r: Rational): string {
  return r.d === 1n ? r.n.toString() : `${r.n}/${r.d}`;
}

/** Round span/count up to a nice 1/2/5 * 10^e step (exact rational). */
function niceStep(raw: Rational): Rational {
  if (!isPositive(raw)) throw new RangeError('niceStep requires a positive input');
  const e = floorLog10(raw);
  const unit = pow10(e);
  const f = div(raw, unit); // in [1, 10)
  const nice = cmp(f, rat(1)) <= 0 ? 1n : cmp(f, rat(2)) <= 0 ? 2n : cmp(f, rat(5)) <= 0 ? 5n : 10n;
  return mul(rat(nice), unit);
}

const MAX_TICKS = 10000;

function linearTickValues(lo: Rational, hi: Rational, count: number): Rational[] {
  const span = sub(hi, lo);
  if (!isPositive(span)) throw new RangeError('linearTickValues requires lo < hi');
  const step = niceStep(div(span, rat(BigInt(Math.max(1, Math.floor(count))))));
  const values: Rational[] = [];
  let k = ceilMultiple(lo, step);
  for (;;) {
    const v = mul(rat(k), step);
    if (cmp(v, hi) > 0) break;
    if (cmp(v, lo) >= 0) values.push(v);
    if (values.length > MAX_TICKS) throw new Error('tick generation overflow');
    k += 1n;
  }
  return values;
}

function logTickValues(lo: Rational, hi: Rational, count: number): Rational[] {
  const eLo = ceilLog10(lo);
  const eHi = floorLog10(hi);
  if (eHi < eLo) {
    // Span narrower than one decade: fall back to linear nice steps.
    return linearTickValues(lo, hi, count);
  }
  const values: Rational[] = [];
  for (let e = eLo; e <= eHi; e += 1) {
    for (const m of [1n, 2n, 5n]) {
      const v = mul(rat(m), pow10(e));
      if (cmp(v, lo) >= 0 && cmp(v, hi) <= 0) values.push(v);
    }
  }
  return values;
}

/**
 * Format distinct values so that every label is unique: raise the
 * significant-digit budget until labels stop colliding, then fall back
 * to exact "n/d" notation for any value still ambiguous.
 */
function uniqueLabels(values: readonly Rational[]): string[] {
  for (let sig = 6; sig <= 17; sig += 1) {
    const labels = values.map((v) => formatRational(v, sig));
    if (new Set(labels).size === labels.length) return labels;
  }
  return values.map((v) => (v.d === 1n ? v.n.toString() : `${v.n}/${v.d}`));
}

export function createScale(options: ScaleOptions): Scale {
  const mode: ScaleMode = options.mode ?? 'linear';
  if (mode !== 'linear' && mode !== 'log') {
    throw new RangeError(`unknown scale mode: ${String(mode)}`);
  }
  const base = options.base ?? 10;
  if (!(base > 0) || base === 1 || !Number.isFinite(base)) {
    throw new RangeError(`log base must be positive and != 1, got ${base}`);
  }
  const d0 = rat(options.domain[0]);
  const d1 = rat(options.domain[1]);
  if (cmp(d0, d1) === 0) {
    throw new RangeError(`domain endpoints must differ, both are ${describe(d0)}`);
  }
  const [p0, p1] = options.range;
  if (!Number.isFinite(p0) || !Number.isFinite(p1)) {
    throw new RangeError(`pixel range must be finite, got [${p0}, ${p1}]`);
  }
  if (p0 === p1) {
    throw new RangeError(`pixel range endpoints must differ, both are ${p0}`);
  }
  if (mode === 'log') {
    if (!isPositive(d0) || !isPositive(d1)) {
      throw new RangeError(
        `log scale requires a positive domain, got [${describe(d0)}, ${describe(d1)}]`,
      );
    }
  }

  const lo = cmp(d0, d1) < 0 ? d0 : d1;
  const hi = cmp(d0, d1) < 0 ? d1 : d0;
  const logB = Math.log(base);
  const logD0 = mode === 'log' ? Math.log(toNumber(d0)) / logB : 0;
  const logD1 = mode === 'log' ? Math.log(toNumber(d1)) / logB : 0;

  function map(value: RationalInput): number {
    const v = rat(value);
    if (mode === 'log') {
      if (!isPositive(v)) {
        throw new RangeError(`log scale cannot map non-positive value ${describe(v)}`);
      }
      const t = (Math.log(toNumber(v)) / logB - logD0) / (logD1 - logD0);
      return p0 + t * (p1 - p0);
    }
    const t = toNumber(sub(v, d0)) / toNumber(sub(d1, d0));
    return p0 + t * (p1 - p0);
  }

  function invert(pixel: number): Rational {
    if (!Number.isFinite(pixel)) {
      throw new RangeError(`cannot invert non-finite pixel ${pixel}`);
    }
    const t = div(fromNumber(pixel - p0), fromNumber(p1 - p0));
    if (mode === 'log') {
      const logV = logD0 + toNumber(t) * (logD1 - logD0);
      return fromNumber(base ** logV);
    }
    // Exact rational arithmetic on the exact binary value of the doubles.
    return add(d0, mul(t, sub(d1, d0)));
  }

  function clip(pixel: number): number {
    const loP = Math.min(p0, p1);
    const hiP = Math.max(p0, p1);
    return Math.min(hiP, Math.max(loP, pixel));
  }

  function ticks(count = 10): readonly Tick[] {
    const values = mode === 'log' ? logTickValues(lo, hi, count) : linearTickValues(lo, hi, count);
    const labels = uniqueLabels(values);
    return values.map((value, i) => ({ value, label: labels[i] }));
  }

  return {
    mode,
    base,
    domain: [d0, d1],
    range: [p0, p1],
    map,
    invert,
    clip,
    ticks,
  };
}

export { sign, abs, isZero };
