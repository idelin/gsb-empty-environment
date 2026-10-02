/**
 * Vectorized compute kernels.
 *
 * Every kernel is a tight loop over compact contiguous Int32Arrays. There are
 * no per-row function calls, no allocations, and no branches inside the hot
 * loop, so the JIT can keep values in registers and the CPU can stream whole
 * cache lines (and auto-vectorize where applicable).
 */

import { Vector } from './vector.ts';

/** out[i] = a[i] + b[i] */
export function add(a: Vector, b: Vector, out: Vector): void {
  const x = a.data, y = b.data, o = out.data;
  const n = a.length;
  for (let i = 0; i < n; i++) {
    o[i] = x[i] + y[i];
  }
}

/** out[i] = a[i] * b[i] */
export function mul(a: Vector, b: Vector, out: Vector): void {
  const x = a.data, y = b.data, o = out.data;
  const n = a.length;
  for (let i = 0; i < n; i++) {
    o[i] = Math.imul(x[i], y[i]);
  }
}

/** out[i] = a[i] + scalar */
export function addScalar(a: Vector, scalar: number, out: Vector): void {
  const x = a.data, o = out.data;
  const n = a.length;
  for (let i = 0; i < n; i++) {
    o[i] = x[i] + scalar;
  }
}

/** out[i] = a[i] * scalar */
export function mulScalar(a: Vector, scalar: number, out: Vector): void {
  const x = a.data, o = out.data;
  const n = a.length;
  for (let i = 0; i < n; i++) {
    o[i] = Math.imul(x[i], scalar);
  }
}

/**
 * Fused kernel: out[i] = a[i] * b[i] + scalar
 * One single pass over memory instead of materializing intermediates.
 */
export function mulAddScalar(a: Vector, b: Vector, scalar: number, out: Vector): void {
  const x = a.data, y = b.data, o = out.data;
  const n = a.length;
  for (let i = 0; i < n; i++) {
    o[i] = Math.imul(x[i], y[i]) + scalar;
  }
}
