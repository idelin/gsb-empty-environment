import {
  Rational,
  RationalLike,
  add,
  cmp,
  div,
  fromNumberExact,
  isPositive,
  mul,
  parseDecimalString,
  rational,
  sub,
  toNumber,
} from './rational.js';
import { Tick, computeTicks } from './ticks.js';

export type ScaleMode = 'linear' | 'log';

export class ScaleError extends Error {}

/** Thrown when the domain or pixel range collapses to a single point. */
export class DegenerateScaleError extends ScaleError {}

/** Thrown when a log scale meets a non-positive domain or input value. */
export class LogDomainError extends ScaleError {}

export interface ScaleOptions {
  readonly domain: readonly [RationalLike, RationalLike];
  readonly range: readonly [number, number];
  readonly mode?: ScaleMode;
}

/**
 * Maps an exact rational domain to a numeric pixel range.
 *
 * - `map` and `invert` never clamp; clamping is available separately through
 *   `clamp` / `clampPixel`.
 * - Inverted pixel ranges (`range[0] > range[1]`) and inverted domains are
 *   legal; the mapping simply flips direction.
 */
export interface Scale {
  readonly mode: ScaleMode;
  readonly domain: readonly [Rational, Rational];
  readonly range: readonly [number, number];
  /** Domain value -> pixel. Never clamps. Log mode rejects non-positive input. */
  map(value: RationalLike): number;
  /** Pixel -> exact domain value. Never clamps. */
  invert(pixel: number): Rational;
  /** Clamp a domain value into the domain. Log mode rejects non-positive input. */
  clamp(value: RationalLike): Rational;
  /** Clamp a pixel into the pixel range. */
  clampPixel(pixel: number): number;
  /** Generate ticks with unique labels and exact values. */
  ticks(targetCount?: number): Tick[];
}

export function createScale(options: ScaleOptions): Scale {
  const mode: ScaleMode = options.mode ?? 'linear';
  const d0 = rational(options.domain[0]);
  const d1 = rational(options.domain[1]);
  const p0 = options.range[0];
  const p1 = options.range[1];

  if (!Number.isFinite(p0) || !Number.isFinite(p1)) {
    throw new ScaleError('pixel range endpoints must be finite numbers');
  }
  if (p0 === p1) throw new DegenerateScaleError('pixel range endpoints must differ');
  if (cmp(d0, d1) === 0) throw new DegenerateScaleError('domain endpoints must differ');
  if (mode === 'log' && (!isPositive(d0) || !isPositive(d1))) {
    throw new LogDomainError('log scale requires a strictly positive domain');
  }

  const lo = cmp(d0, d1) <= 0 ? d0 : d1;
  const hi = cmp(d0, d1) <= 0 ? d1 : d0;
  const span = sub(d1, d0);
  const logD0 = mode === 'log' ? Math.log(toNumber(d0)) : 0;
  const logD1 = mode === 'log' ? Math.log(toNumber(d1)) : 0;

  function requirePositive(value: Rational): void {
    if (!isPositive(value)) {
      throw new LogDomainError('log scale cannot handle a non-positive value');
    }
  }

  function map(value: RationalLike): number {
    const v = rational(value);
    if (cmp(v, d0) === 0) return p0;
    if (cmp(v, d1) === 0) return p1;
    let t: number;
    if (mode === 'log') {
      requirePositive(v);
      t = (Math.log(toNumber(v)) - logD0) / (logD1 - logD0);
    } else {
      t = toNumber(div(sub(v, d0), span));
    }
    return p0 + t * (p1 - p0);
  }

  function invert(pixel: number): Rational {
    if (!Number.isFinite(pixel)) throw new ScaleError(`cannot invert pixel ${pixel}`);
    if (pixel === p0) return d0;
    if (pixel === p1) return d1;
    const t = (pixel - p0) / (p1 - p0);
    if (mode === 'log') {
      // Inherently approximate: exp/log are transcendental.
      return parseDecimalString(Math.exp(logD0 + t * (logD1 - logD0)).toString());
    }
    // Exact rational arithmetic on the exact binary value of t.
    return add(d0, mul(fromNumberExact(t), span));
  }

  function clamp(value: RationalLike): Rational {
    const v = rational(value);
    if (mode === 'log') requirePositive(v);
    if (cmp(v, lo) < 0) return lo;
    if (cmp(v, hi) > 0) return hi;
    return v;
  }

  function clampPixel(pixel: number): number {
    const loPixel = Math.min(p0, p1);
    const hiPixel = Math.max(p0, p1);
    return Math.min(hiPixel, Math.max(loPixel, pixel));
  }

  function ticks(targetCount: number = 10): Tick[] {
    return computeTicks(mode, [lo, hi], map, targetCount);
  }

  return { mode, domain: [d0, d1], range: [p0, p1], map, invert, clamp, clampPixel, ticks };
}
