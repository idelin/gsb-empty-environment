import type { Sample } from './index.js';
import { STALE_NAN_BITS } from './index.js';

/** Build a float64 carrying the exact requested raw bit pattern (little endian). */
export function floatFromBits(bits: bigint): number {
  const buffer = new ArrayBuffer(8);
  new DataView(buffer).setBigUint64(0, bits, true);
  return new DataView(buffer).getFloat64(0, true);
}

/** Prometheus stale marker NaN, preserving its exact bit pattern. */
export function staleNaN(): number {
  return floatFromBits(STALE_NAN_BITS);
}

/** Canonical ordinary NaN (0x7ff8...). */
export function ordinaryNaN(): number {
  return floatFromBits(0x7ff8000000000000n);
}

export function s(t: number, v: number, stale = false): Sample {
  return { t, v, stale };
}

/**
 * Encode interleaved (int64 timestamp ms, float64 raw value) samples into an
 * ArrayBuffer, preserving exact NaN bit patterns on the wire.
 */
export function encodeInterleaved(rows: ReadonlyArray<readonly [number, bigint]>): ArrayBuffer {
  const buffer = new ArrayBuffer(rows.length * 16);
  const view = new DataView(buffer);
  rows.forEach(([t, bits], index) => {
    view.setBigInt64(index * 16, BigInt(t), true);
    view.setBigUint64(index * 16 + 8, bits, true);
  });
  return buffer;
}

export function closeTo(actual: number, expected: number, epsilon = 1e-9): boolean {
  return Math.abs(actual - expected) <= epsilon * Math.max(1, Math.abs(expected));
}
