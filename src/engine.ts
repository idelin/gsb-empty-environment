/**
 * 列式批量求值引擎。
 *
 * 以定长 Chunk（1024 行）为单位处理任意长度的列数据，
 * 通过融合内核在紧凑连续内存上完成 'A * B + 10' 这类表达式。
 */

import { CHUNK_SIZE, Int32Vector } from './vector.ts';
import { mulAddScalar } from './kernels.ts';

export interface Column {
  name: string;
  /** 列数据，按 CHUNK_SIZE 切分为若干连续 Chunk */
  chunks: Int32Vector[];
  readonly length: number;
}

export function makeColumn(name: string, values: ArrayLike<number>): Column {
  const chunks: Int32Vector[] = [];
  for (let off = 0; off < values.length; off += CHUNK_SIZE) {
    const size = Math.min(CHUNK_SIZE, values.length - off);
    const vec = new Int32Vector(size);
    for (let i = 0; i < size; i++) vec.data[i] = values[off + i];
    chunks.push(vec);
  }
  return { name, chunks, length: values.length };
}

/**
 * 向量化执行 'A * B + k'：逐 Chunk 调用融合内核，
 * 每个 Chunk 内部是紧凑连续内存上的单次顺序扫描。
 */
export function evalMulAddScalar(a: Column, b: Column, k: number): Int32Vector[] {
  if (a.length !== b.length) throw new Error('column length mismatch');
  const out: Int32Vector[] = [];
  for (let c = 0; c < a.chunks.length; c++) {
    const dst = new Int32Vector(a.chunks[c].length);
    mulAddScalar(a.chunks[c], b.chunks[c], k, dst);
    out.push(dst);
  }
  return out;
}
