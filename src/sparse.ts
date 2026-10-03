import { ProtobufDecodeError, ProtobufEncodeError } from './errors.ts';
import { WireReader } from './reader.ts';
import { WireWriter } from './writer.ts';

export type ContainerMode = 'map' | 'dense';

export interface SparseStats {
  mode: ContainerMode;
  /** 非零元素个数。 */
  count: number;
  /** 索引跨度（最大索引 + 1）。 */
  span: number;
  /** 密度 = count / span。 */
  density: number;
  /** 当前容器的估算内存占用（字节）。 */
  estimatedBytes: number;
}

/** 密度高于该阈值时切换为 dense 容器。 */
export const DENSE_THRESHOLD = 0.5;
/** 密度低于该阈值时回落为 map 容器（与上阈值形成迟滞，避免抖动）。 */
export const SPARSE_THRESHOLD = 0.25;

const MAP_ENTRY_BYTES = 32; // Map 节点估算开销
const DENSE_ELEM_BYTES = 8;
/** dense 容器允许的最大跨度（超出则保持 map，避免病态巨型数组）。 */
export const MAX_DENSE_SPAN = 1 << 20;

/**
 * 海量稀疏整数向量的内存紧凑容器。
 *
 * - 自适应容器转换：依据密度在 Map（稀疏）与 Array（稠密）之间切换；
 * - 压缩：RLE 游程 + varint 索引增量 + ZigZag 值编码，输出 protobuf 风格二进制；
 * - 并发安全：同步单操作天然原子；异步批量操作经 AsyncMutex 串行化；
 * - 无泄漏：clear() 释放全部内部容器，压缩不持有源数据引用。
 */
export class SparseVector {
  private map: Map<number, number> | null = new Map();
  private dense: number[] | null = null;
  private maxIndex = -1;
  private count = 0;

  get size(): number {
    return this.count;
  }

  get mode(): ContainerMode {
    return this.dense ? 'dense' : 'map';
  }

  get(index: number): number {
    this.checkIndex(index);
    if (this.dense) return this.dense[index] ?? 0;
    return this.map!.get(index) ?? 0;
  }

  set(index: number, value: number): void {
    this.checkIndex(index);
    if (!Number.isInteger(value)) {
      throw new ProtobufEncodeError(`SparseVector only stores integers, got ${value}`);
    }
    if (this.dense && index >= this.dense.length) {
      if (index + 1 <= MAX_DENSE_SPAN) {
        const grown = new Array<number>(index + 1).fill(0);
        for (let i = 0; i < this.dense.length; i++) grown[i] = this.dense[i] ?? 0;
        this.dense = grown;
      } else {
        this.convertToMap();
      }
    }
    const had = this.has(index);
    if (value === 0) {
      if (!had) return;
      this.count--;
      if (this.dense) this.dense[index] = 0;
      else this.map!.delete(index);
    } else {
      if (!had) this.count++;
      if (index > this.maxIndex) this.maxIndex = index;
      if (this.dense) this.dense[index] = value;
      else this.map!.set(index, value);
    }
    this.rebalance();
  }

  delete(index: number): void {
    this.set(index, 0);
  }

  has(index: number): boolean {
    if (this.dense) return (this.dense[index] ?? 0) !== 0;
    return this.map!.has(index);
  }

  /** 按索引升序迭代非零项。 */
  *entries(): IterableIterator<[number, number]> {
    if (this.dense) {
      for (let i = 0; i < this.dense.length; i++) {
        const v = this.dense[i];
        if (v !== undefined && v !== 0) yield [i, v];
      }
    } else {
      const keys = [...this.map!.keys()].sort((a, b) => a - b);
      for (const k of keys) yield [k, this.map!.get(k)!];
    }
  }

  stats(): SparseStats {
    const span = this.maxIndex + 1;
    const density = span === 0 ? 0 : this.count / span;
    return {
      mode: this.mode,
      count: this.count,
      span,
      density,
      estimatedBytes: this.dense
        ? this.dense.length * DENSE_ELEM_BYTES
        : this.count * MAP_ENTRY_BYTES,
    };
  }

  /** 依据密度在两种容器间自适应转换（带迟滞）。 */
  private rebalance(): void {
    const span = this.maxIndex + 1;
    if (span === 0) return;
    const density = this.count / span;
    if (!this.dense && density >= DENSE_THRESHOLD && span <= MAX_DENSE_SPAN) {
      const arr: number[] = new Array(span).fill(0);
      for (const [k, v] of this.map!) arr[k] = v;
      this.dense = arr;
      this.map = null; // 释放稀疏容器，避免双份占用
    } else if (this.dense && density < SPARSE_THRESHOLD) {
      this.convertToMap();
    }
  }

  private convertToMap(): void {
    const m = new Map<number, number>();
    const dense = this.dense!;
    for (let i = 0; i < dense.length; i++) {
      const v = dense[i];
      if (v !== undefined && v !== 0) m.set(i, v);
    }
    this.map = m;
    this.dense = null;
  }

  /**
   * 压缩为紧凑二进制：
   *   varint runCount
   *   每游程: varint indexDelta | varint runLength | svarint value
   * 索引增量相对上一游程末尾，稀疏间隙零开销。
   */
  compress(): Uint8Array {
    const writer = new WireWriter();
    const runs: Array<[number, number, number]> = [];
    for (const [idx, val] of this.entries()) {
      const last = runs[runs.length - 1];
      if (last && last[2] === val && last[0] + last[1] === idx) {
        last[1]++;
      } else {
        runs.push([idx, 1, val]);
      }
    }
    writer.writeVarint(runs.length);
    let prevEnd = 0;
    for (const [start, len, val] of runs) {
      writer.writeVarint(start - prevEnd);
      writer.writeVarint(len);
      writer.writeSVarint64(BigInt(val));
      prevEnd = start + len;
    }
    return writer.finish();
  }

  /** 从 compress() 的输出还原。 */
  static decompress(buf: Uint8Array): SparseVector {
    const reader = new WireReader(buf);
    const runCount = Number(reader.readVarint());
    const out = new SparseVector();
    let cursor = 0;
    for (let i = 0; i < runCount; i++) {
      const delta = Number(reader.readVarint());
      const len = Number(reader.readVarint());
      const value = Number(reader.readSVarint64());
      if (len === 0) throw new ProtobufDecodeError('Zero-length run', reader.offset);
      cursor += delta;
      for (let j = 0; j < len; j++) out.set(cursor + j, value);
      cursor += len;
    }
    if (!reader.eof) throw new ProtobufDecodeError('Trailing bytes in compressed vector', reader.offset);
    return out;
  }

  /** 释放全部内部存储。调用后对象恢复为空向量。 */
  clear(): void {
    this.map = new Map();
    this.dense = null;
    this.maxIndex = -1;
    this.count = 0;
  }

  private checkIndex(index: number): void {
    if (!Number.isInteger(index) || index < 0) {
      throw new ProtobufEncodeError(`Invalid sparse index ${index}`);
    }
  }
}

/** 简单异步互斥锁：串行化并发批量操作，保证容器转换期间状态一致。 */
export class AsyncMutex {
  private tail: Promise<void> = Promise.resolve();

  run<T>(fn: () => Promise<T> | T): Promise<T> {
    const result = this.tail.then(fn);
    this.tail = result.then(
      () => undefined,
      () => undefined,
    );
    return result;
  }
}

/** 并发安全的稀疏向量包装：所有变更经互斥锁串行执行。 */
export class ConcurrentSparseVector {
  readonly inner = new SparseVector();
  private readonly mutex = new AsyncMutex();

  set(index: number, value: number): Promise<void> {
    return this.mutex.run(() => this.inner.set(index, value));
  }

  delete(index: number): Promise<void> {
    return this.mutex.run(() => this.inner.delete(index));
  }

  get(index: number): number {
    return this.inner.get(index);
  }

  clear(): Promise<void> {
    return this.mutex.run(() => this.inner.clear());
  }
}
