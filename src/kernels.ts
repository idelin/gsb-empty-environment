/**
 * 向量化计算内核。
 *
 * 所有内核遵循同一模式：对紧凑连续的 Int32Array 做单次顺序扫描，
 * 循环体只包含纯算术运算（无函数调用、无分支、无堆访问），
 * 并按 8 路展开，帮助 JIT 消除边界检查并生成 SIMD 指令。
 */

import { Int32Vector } from './vector.ts';

const UNROLL = 8;

function assertSameLength(a: Int32Vector, b: Int32Vector, out: Int32Vector): void {
  if (a.length !== b.length || a.length !== out.length) {
    throw new Error(`length mismatch: ${a.length}, ${b.length}, ${out.length}`);
  }
}

/** out[i] = a[i] + b[i] */
export function add(a: Int32Vector, b: Int32Vector, out: Int32Vector): void {
  assertSameLength(a, b, out);
  const x = a.data, y = b.data, r = out.data;
  const n = a.length;
  let i = 0;
  const limit = n - (n % UNROLL);
  for (; i < limit; i += UNROLL) {
    r[i] = x[i] + y[i];
    r[i + 1] = x[i + 1] + y[i + 1];
    r[i + 2] = x[i + 2] + y[i + 2];
    r[i + 3] = x[i + 3] + y[i + 3];
    r[i + 4] = x[i + 4] + y[i + 4];
    r[i + 5] = x[i + 5] + y[i + 5];
    r[i + 6] = x[i + 6] + y[i + 6];
    r[i + 7] = x[i + 7] + y[i + 7];
  }
  for (; i < n; i++) r[i] = x[i] + y[i];
}

/** out[i] = a[i] * b[i] */
export function mul(a: Int32Vector, b: Int32Vector, out: Int32Vector): void {
  assertSameLength(a, b, out);
  const x = a.data, y = b.data, r = out.data;
  const n = a.length;
  let i = 0;
  const limit = n - (n % UNROLL);
  for (; i < limit; i += UNROLL) {
    r[i] = x[i] * y[i];
    r[i + 1] = x[i + 1] * y[i + 1];
    r[i + 2] = x[i + 2] * y[i + 2];
    r[i + 3] = x[i + 3] * y[i + 3];
    r[i + 4] = x[i + 4] * y[i + 4];
    r[i + 5] = x[i + 5] * y[i + 5];
    r[i + 6] = x[i + 6] * y[i + 6];
    r[i + 7] = x[i + 7] * y[i + 7];
  }
  for (; i < n; i++) r[i] = x[i] * y[i];
}

/** out[i] = a[i] + k */
export function addScalar(a: Int32Vector, k: number, out: Int32Vector): void {
  if (a.length !== out.length) throw new Error('length mismatch');
  const x = a.data, r = out.data;
  const n = a.length;
  let i = 0;
  const limit = n - (n % UNROLL);
  for (; i < limit; i += UNROLL) {
    r[i] = x[i] + k;
    r[i + 1] = x[i + 1] + k;
    r[i + 2] = x[i + 2] + k;
    r[i + 3] = x[i + 3] + k;
    r[i + 4] = x[i + 4] + k;
    r[i + 5] = x[i + 5] + k;
    r[i + 6] = x[i + 6] + k;
    r[i + 7] = x[i + 7] + k;
  }
  for (; i < n; i++) r[i] = x[i] + k;
}

/** out[i] = a[i] * k */
export function mulScalar(a: Int32Vector, k: number, out: Int32Vector): void {
  if (a.length !== out.length) throw new Error('length mismatch');
  const x = a.data, r = out.data;
  const n = a.length;
  let i = 0;
  const limit = n - (n % UNROLL);
  for (; i < limit; i += UNROLL) {
    r[i] = x[i] * k;
    r[i + 1] = x[i + 1] * k;
    r[i + 2] = x[i + 2] * k;
    r[i + 3] = x[i + 3] * k;
    r[i + 4] = x[i + 4] * k;
    r[i + 5] = x[i + 5] * k;
    r[i + 6] = x[i + 6] * k;
    r[i + 7] = x[i + 7] * k;
  }
  for (; i < n; i++) r[i] = x[i] * k;
}

/**
 * 融合内核：out[i] = a[i] * b[i] + k
 * 单次遍历完成 'A * B + 10'，避免中间结果写回内存。
 */
export function mulAddScalar(
  a: Int32Vector,
  b: Int32Vector,
  k: number,
  out: Int32Vector,
): void {
  assertSameLength(a, b, out);
  const x = a.data, y = b.data, r = out.data;
  const n = a.length;
  let i = 0;
  const limit = n - (n % UNROLL);
  for (; i < limit; i += UNROLL) {
    r[i] = x[i] * y[i] + k;
    r[i + 1] = x[i + 1] * y[i + 1] + k;
    r[i + 2] = x[i + 2] * y[i + 2] + k;
    r[i + 3] = x[i + 3] * y[i + 3] + k;
    r[i + 4] = x[i + 4] * y[i + 4] + k;
    r[i + 5] = x[i + 5] * y[i + 5] + k;
    r[i + 6] = x[i + 6] * y[i + 6] + k;
    r[i + 7] = x[i + 7] * y[i + 7] + k;
  }
  for (; i < n; i++) r[i] = x[i] * y[i] + k;
}
