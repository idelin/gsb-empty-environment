import { Rational } from './rational.ts';
import type { ExtVal } from './extval.ts';
import { finite, posInf, negInf } from './extval.ts';

/**
 * A measurement: an exact rational value plus an error bound.
 *
 * error === 'unknown' means the error bound is NOT known. It is never
 * treated as zero: the feasible interval becomes (-inf, +inf), so any
 * classification depending on it degrades to "review" instead of passing.
 */
export interface Measurement {
  readonly id: string;
  readonly value: Rational;
  readonly error: Rational | 'unknown';
}

export function measurement(
  id: string,
  value: Rational | string | bigint,
  error: Rational | string | bigint | 'unknown',
): Measurement {
  const v = typeof value === 'object' && value instanceof Rational ? value : Rational.from(value as string | bigint);
  let e: Rational | 'unknown';
  if (error === 'unknown') {
    e = 'unknown';
  } else {
    e = error instanceof Rational ? error : Rational.from(error as string | bigint);
    if (e.sign() < 0) throw new Error(`measurement ${id}: error bound must be >= 0`);
  }
  return { id, value: v, error: e };
}

export interface Interval {
  readonly lo: ExtVal;
  readonly hi: ExtVal;
}

export function intervalOf(m: Measurement): Interval {
  if (m.error === 'unknown') return { lo: negInf, hi: posInf };
  return { lo: finite(m.value.sub(m.error)), hi: finite(m.value.add(m.error)) };
}

/** A box: per-variable intervals, the domain over which judgments are made. */
export type Box = ReadonlyMap<string, Interval>;

export function boxOf(measurements: readonly Measurement[]): Box {
  const box = new Map<string, Interval>();
  for (const m of measurements) box.set(m.id, intervalOf(m));
  return box;
}
