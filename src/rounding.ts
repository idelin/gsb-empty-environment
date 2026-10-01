import type { Rational } from './rational.ts';
import { add, div, isInteger, make, mul, sub } from './rational.ts';
import type { Interval } from './interval.ts';

/**
 * Explicit rounding modes. Ties are defined precisely:
 *  - 'floor' / 'ceil' / 'trunc': toward -inf / +inf / zero.
 *  - 'half-up': nearest, ties toward +inf.
 *  - 'half-away-from-zero': nearest, ties away from zero.
 *  - 'half-even': nearest, ties to the even multiple of the quantum.
 */
export type RoundingMode = 'floor' | 'ceil' | 'trunc' | 'half-up' | 'half-away-from-zero' | 'half-even';

export interface RoundingSpec {
  readonly quantum: Rational; // must be > 0
  readonly mode: RoundingMode;
}

function floorDivBig(n: bigint, d: bigint): bigint {
  // d > 0
  const q = n / d;
  const r = n % d;
  return r !== 0n && r < 0n ? q - 1n : q;
}
function floorModBig(n: bigint, d: bigint): bigint {
  const r = n % d;
  return r < 0n ? r + d : r;
}

/** Rounds the rational n/d (d > 0) to an integer per mode. */
export function roundRatioToBigInt(n: bigint, d: bigint, mode: RoundingMode): bigint {
  switch (mode) {
    case 'floor':
      return floorDivBig(n, d);
    case 'ceil':
      return -floorDivBig(-n, d);
    case 'trunc':
      return n / d;
    case 'half-up':
      return floorDivBig(2n * n + d, 2n * d);
    case 'half-away-from-zero': {
      const s = n < 0n ? -1n : 1n;
      const an = n < 0n ? -n : n;
      return s * floorDivBig(2n * an + d, 2n * d);
    }
    case 'half-even': {
      const up = floorDivBig(2n * n + d, 2n * d); // half-up result
      const tie = floorModBig(2n * n, 2n * d) === d;
      if (tie && up % 2n !== 0n) return up - 1n;
      return up;
    }
  }
}

export function roundTo(v: Rational, spec: RoundingSpec): Rational {
  const q = spec.quantum;
  if (q.n <= 0n) throw new Error('rounding: quantum must be positive');
  // v / q = (v.n * q.d) / (v.d * q.n)
  const k = roundRatioToBigInt(v.n * q.d, v.d * q.n, spec.mode);
  return make(k * q.n, q.d);
}

/**
 * Exact preimage of a rounded value: the interval of v with roundTo(v, spec) === y.
 * Returns null when y is not an integer multiple of the quantum (no input can produce it).
 */
export function preimage(y: Rational, spec: RoundingSpec): Interval | null {
  const q = spec.quantum;
  if (q.n <= 0n) throw new Error('rounding: quantum must be positive');
  const k = div(y, q);
  if (!isInteger(k)) return null;
  const kBig = k.n;
  const half = make(q.n, q.d * 2n);
  const lo = sub(y, half);
  const hi = add(y, half);
  switch (spec.mode) {
    case 'floor':
      return { lo: { value: y, closed: true }, hi: { value: add(y, q), closed: false } };
    case 'ceil':
      return { lo: { value: sub(y, q), closed: false }, hi: { value: y, closed: true } };
    case 'trunc':
      if (kBig > 0n) return { lo: { value: y, closed: true }, hi: { value: add(y, q), closed: false } };
      if (kBig < 0n) return { lo: { value: sub(y, q), closed: false }, hi: { value: y, closed: true } };
      return { lo: { value: sub(y, q), closed: false }, hi: { value: add(y, q), closed: false } };
    case 'half-up':
      return { lo: { value: lo, closed: true }, hi: { value: hi, closed: false } };
    case 'half-away-from-zero':
      // ties round away from zero, so the tie nearer to zero belongs to y, the far one does not
      if (kBig > 0n) return { lo: { value: lo, closed: true }, hi: { value: hi, closed: false } };
      if (kBig < 0n) return { lo: { value: lo, closed: false }, hi: { value: hi, closed: true } };
      return { lo: { value: lo, closed: false }, hi: { value: hi, closed: false } };
    case 'half-even':
      if (kBig % 2n === 0n) return { lo: { value: lo, closed: true }, hi: { value: hi, closed: true } };
      return { lo: { value: lo, closed: false }, hi: { value: hi, closed: false } };
  }
}

/** Convenience: apply rounding to a value only when a spec is present. */
export function maybeRound(v: Rational, spec: RoundingSpec | undefined): Rational {
  return spec ? roundTo(v, spec) : v;
}

