/**
 * 逐行迭代（火山模型）基线。
 *
 * 模拟传统行式执行引擎：算子链通过 next() 一次拉取一行，
 * 每行都经历 取行 -> 表达式求值 -> 输出 的完整调用链，
 * 无任何批量摊销，用于对照向量化内核的吞吐提升。
 */

import type { Column } from './engine.ts';

interface Row {
  a: number;
  b: number;
}

/** 扫描算子：每次 next() 吐出一行 */
class ScanOperator {
  private pos = 0;
  private readonly total: number;
  private readonly colA: Column;
  private readonly colB: Column;
  constructor(colA: Column, colB: Column) {
    this.colA = colA;
    this.colB = colB;
    this.total = colA.length;
  }
  next(): Row | null {
    if (this.pos >= this.total) return null;
    const chunkIdx = (this.pos / 1024) | 0;
    const rowIdx = this.pos % 1024;
    this.pos++;
    return {
      a: this.colA.chunks[chunkIdx].data[rowIdx],
      b: this.colB.chunks[chunkIdx].data[rowIdx],
    };
  }
}

/** 投影算子：对每行执行表达式 a * b + k */
class ProjectOperator {
  private readonly child: ScanOperator;
  private readonly k: number;
  constructor(child: ScanOperator, k: number) {
    this.child = child;
    this.k = k;
  }
  next(): number | null {
    const row = this.child.next();
    if (row === null) return null;
    return row.a * row.b + this.k;
  }
}

/** 逐行执行 'A * B + k'，结果写入连续数组 */
export function evalRowWise(a: Column, b: Column, k: number): Int32Array {
  const out = new Int32Array(a.length);
  const project = new ProjectOperator(new ScanOperator(a, b), k);
  let i = 0;
  let value: number | null;
  while ((value = project.next()) !== null) {
    out[i++] = value;
  }
  return out;
}
