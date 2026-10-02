/**
 * SparseArray：面向海量稀疏数据（下标空间可达 2^53）的内存紧凑数组。
 *
 * - 分块存储（每块 4096 槽位），块内 Map/RLE/Dense 容器自适应转换。
 * - compress()/decompress()：delta-varint 下标 + RLE 值压缩。
 * - memoryBytes()：精确到块级的内存估算。
 * - transaction()：AsyncMutex 保护的并发安全读-改-写。
 * - dispose()：释放全部块；配合 LeakRegistry 断言零泄漏。
 */
import {
  CHUNK_BITS,
  CHUNK_MASK,
  LeakRegistry,
  MapChunk,
  optimizeChunk,
  type Chunk,
} from "./containers.ts";
import {
  readDeltaIndices,
  readRleValues,
  writeDeltaIndices,
  writeRleValues,
} from "./encoding.ts";
import { AsyncMutex } from "./async-mutex.ts";

export class SparseArray<T> {
  private chunks = new Map<number, Chunk<T>>();
  private size = 0;
  private readonly mutex = new AsyncMutex();
  private disposed = false;

  constructor(entries?: Iterable<[number, T]>) {
    LeakRegistry.acquireArray();
    if (entries) for (const [i, v] of entries) this.set(i, v);
  }

  get length(): number { return this.size; }
  get chunkCount(): number { return this.chunks.size; }

  /** 测试观测：各块当前容器类型。 */
  chunkKinds(): string[] {
    return [...this.chunks.values()].map((c) => c.kind);
  }

  private assertLive(): void {
    if (this.disposed) throw new Error("SparseArray already disposed");
  }

  private static chunkIndexOf(index: number): number {
    return Math.floor(index / (CHUNK_MASK + 1));
  }

  has(index: number): boolean {
    this.assertLive();
    this.checkIndex(index);
    return this.chunks.get(SparseArray.chunkIndexOf(index))?.has(index & CHUNK_MASK) ?? false;
  }

  get(index: number): T | undefined {
    this.assertLive();
    this.checkIndex(index);
    return this.chunks.get(SparseArray.chunkIndexOf(index))?.get(index & CHUNK_MASK);
  }

  set(index: number, value: T): void {
    this.assertLive();
    this.checkIndex(index);
    const ci = SparseArray.chunkIndexOf(index);
    const offset = index & CHUNK_MASK;
    let chunk = this.chunks.get(ci);
    if (!chunk) {
      chunk = new MapChunk<T>();
      this.chunks.set(ci, chunk);
    }
    const existed = chunk.has(offset);
    const next = chunk.set(offset, value);
    if (next !== chunk) this.chunks.set(ci, next);
    if (!existed) this.size++;
  }

  delete(index: number): boolean {
    this.assertLive();
    this.checkIndex(index);
    const ci = SparseArray.chunkIndexOf(index);
    const chunk = this.chunks.get(ci);
    if (!chunk) return false;
    const offset = index & CHUNK_MASK;
    if (!chunk.has(offset)) return false;
    const next = chunk.delete(offset);
    if (next === null) this.chunks.delete(ci);
    else if (next !== chunk) this.chunks.set(ci, next);
    this.size--;
    return true;
  }

  /** 强制全量容器自适应评估（批量写入后调用一次可达最优布局）。 */
  optimize(): void {
    this.assertLive();
    for (const [ci, chunk] of this.chunks) {
      const next = optimizeChunk(chunk);
      if (next !== chunk) this.chunks.set(ci, next);
    }
  }

  /** 内存估算（字节）。 */
  memoryBytes(): number {
    this.assertLive();
    let total = 64 + this.chunks.size * 16; // 数组头 + 块索引
    for (const chunk of this.chunks.values()) total += chunk.memoryBytes();
    return total;
  }

  /** 与等效稠密数组对比的空间利用率。 */
  get density(): number {
    this.assertLive();
    if (this.chunks.size === 0) return 0;
    return this.size / (this.chunks.size * (CHUNK_MASK + 1));
  }

  *entries(): IterableIterator<[number, T]> {
    this.assertLive();
    for (const [ci, chunk] of [...this.chunks.entries()].sort((a, b) => a[0] - b[0])) {
      for (const [offset, value] of chunk.entries()) {
        yield [ci * (CHUNK_MASK + 1) + offset, value];
      }
    }
  }

  keys(): number[] {
    const out: number[] = [];
    for (const [i] of this.entries()) out.push(i);
    return out;
  }

  /**
   * 压缩：delta-varint 编码有序下标 + RLE 编码值序列。
   * 对稀疏 + 重复值分布达到极致压缩率。
   */
  compress(): Uint8Array {
    this.assertLive();
    const indices: number[] = [];
    const values: T[] = [];
    for (const [i, v] of this.entries()) { indices.push(i); values.push(v); }
    const out: number[] = [];
    writeDeltaIndices(out, indices);
    writeRleValues(out, values);
    return Uint8Array.from(out);
  }

  static decompress<T>(buf: Uint8Array): SparseArray<T> {
    const pos = { p: 0 };
    const indices = readDeltaIndices(buf, pos);
    const values = readRleValues(buf, pos) as T[];
    if (indices.length !== values.length) {
      throw new Error("corrupt buffer: index/value count mismatch");
    }
    if (pos.p !== buf.length) throw new Error("corrupt buffer: trailing bytes");
    const arr = new SparseArray<T>();
    for (let k = 0; k < indices.length; k++) arr.set(indices[k], values[k]);
    arr.optimize();
    return arr;
  }

  /** 并发安全事务：互斥保护的读-改-写。 */
  transaction<R>(fn: (arr: SparseArray<T>) => R | Promise<R>): Promise<R> {
    this.assertLive();
    return this.mutex.runExclusive(() => fn(this));
  }

  /** 并发安全自增（数值场景的原子读-改-写）。 */
  increment(index: number, delta = 1): Promise<number> {
    return this.transaction((arr) => {
      const cur = (arr.get(index) as unknown as number | undefined) ?? 0;
      const next = cur + delta;
      arr.set(index, next as unknown as T);
      return next;
    });
  }

  clear(): void {
    this.assertLive();
    for (const chunk of this.chunks.values()) chunk.dispose();
    this.chunks.clear();
    this.size = 0;
  }

  dispose(): void {
    if (this.disposed) return;
    for (const chunk of this.chunks.values()) chunk.dispose();
    this.chunks.clear();
    this.size = 0;
    this.disposed = true;
    LeakRegistry.releaseArray();
  }

  private checkIndex(index: number): void {
    if (!Number.isSafeInteger(index) || index < 0) {
      throw new RangeError(`index must be a non-negative safe integer, got ${index}`);
    }
  }
}
