/**
 * 自适应稀疏向量：针对海量稀疏数值数据的内存紧凑容器。
 *
 * 三种内部表示，按密度与访问冷热自动转换：
 *  - sparse：Map<index, value>，适合极低密度（默认 < 25%）
 *  - dense：Float64Array 平铺，适合高密度（默认 > 50%），0 表示空槽
 *  - compressed：delta+varint 下标 + zigzag/RLE 值压缩为 Uint8Array，
 *    冷数据（长时间只读）自动压缩，写入时惰性解压
 *
 * 不变量：任何模式下 get/has/size/迭代行为完全一致。
 */
import {
  decodeDeltaIndices,
  decodeRleValues,
  encodeDeltaIndices,
  encodeRleValues,
} from "./codec.ts";

export type SparseMode = "sparse" | "dense" | "compressed";

export interface SparseVectorOptions {
  /** 密度超过该阈值时 sparse -> dense。默认 0.5。 */
  denseThreshold?: number;
  /** 密度低于该阈值时 dense -> sparse。默认 0.25。 */
  sparseThreshold?: number;
  /** 连续只读访问达到该次数后自动压缩。默认 1024；0 表示关闭。 */
  autoCompactAfter?: number;
}

interface CompressedState {
  blob: Uint8Array;
  count: number;
  maxIndex: number;
}

const SPARSE_ENTRY_BYTES = 24; // Map 条目近似开销（key+value+指针）
const SPARSE_BASE_BYTES = 64;
const DENSE_BASE_BYTES = 48;

export class SparseVector {
  private sparse = new Map<number, number>();
  private dense: Float64Array | null = null;
  private packed: CompressedState | null = null;
  private length = 0; // 逻辑容量上界（maxIndex + 1）
  private coldReads = 0;

  private readonly denseThreshold: number;
  private readonly sparseThreshold: number;
  private readonly autoCompactAfter: number;

  constructor(opts: SparseVectorOptions = {}) {
    this.denseThreshold = opts.denseThreshold ?? 0.5;
    this.sparseThreshold = opts.sparseThreshold ?? 0.25;
    this.autoCompactAfter = opts.autoCompactAfter ?? 1024;
  }

  get mode(): SparseMode {
    if (this.packed) return "compressed";
    if (this.dense) return "dense";
    return "sparse";
  }

  get size(): number {
    if (this.packed) return this.packed.count;
    if (this.dense) {
      let n = 0;
      for (let i = 0; i < this.dense.length; i++) if (this.dense[i] !== 0) n++;
      return n;
    }
    return this.sparse.size;
  }

  get capacity(): number {
    return this.length;
  }

  density(): number {
    return this.length === 0 ? 0 : this.size / this.length;
  }

  private ensureWritable(): void {
    if (this.packed) this.decompress();
  }

  set(index: number, value: number): void {
    if (!Number.isInteger(index) || index < 0) throw new RangeError(`bad index ${index}`);
    this.ensureWritable();
    this.coldReads = 0;
    if (value === 0) {
      this.deleteInternal(index);
      return;
    }
    if (this.dense) {
      if (index >= this.dense.length) this.maybeGrowDense(index);
      if (this.dense) {
        this.dense[index] = value;
        this.length = Math.max(this.length, index + 1);
        this.maybeSparsify();
        return;
      }
    }
    this.sparse.set(index, value);
    this.length = Math.max(this.length, index + 1);
    this.maybeDensify();
  }

  get(index: number): number {
    if (this.packed) {
      this.noteColdRead();
      return this.getPacked(index);
    }
    if (this.dense) {
      this.noteColdRead();
      return index < this.dense.length ? this.dense[index] : 0;
    }
    this.noteColdRead();
    return this.sparse.get(index) ?? 0;
  }

  has(index: number): boolean {
    return this.get(index) !== 0;
  }

  delete(index: number): void {
    this.ensureWritable();
    this.coldReads = 0;
    this.deleteInternal(index);
  }

  private deleteInternal(index: number): void {
    if (this.dense) {
      if (index < this.dense.length) this.dense[index] = 0;
      this.maybeSparsify();
      return;
    }
    this.sparse.delete(index);
  }

  private noteColdRead(): void {
    if (this.autoCompactAfter > 0 && ++this.coldReads >= this.autoCompactAfter) {
      this.compact();
    }
  }

  // ---------- 自适应转换 ----------

  private maybeDensify(): void {
    if (this.dense) return;
    if (this.length > 0 && this.sparse.size / this.length >= this.denseThreshold) {
      const dense = new Float64Array(this.length);
      for (const [i, v] of this.sparse) dense[i] = v;
      this.dense = dense;
      this.sparse.clear();
    }
  }

  private maybeSparsify(): void {
    if (!this.dense) return;
    let count = 0;
    for (let i = 0; i < this.dense.length; i++) if (this.dense[i] !== 0) count++;
    if (this.dense.length > 0 && count / this.dense.length < this.sparseThreshold) {
      for (let i = 0; i < this.dense.length; i++) {
        if (this.dense[i] !== 0) this.sparse.set(i, this.dense[i]);
      }
      this.dense = null;
      this.trimLength();
    }
  }

  private maybeGrowDense(index: number): void {
    if (!this.dense) return;
    const prospective = index + 1;
    const count = this.size;
    // 扩容后密度仍高于稀疏阈值才保持 dense，否则退回 sparse。
    if (count / prospective >= this.sparseThreshold) {
      const next = new Float64Array(Math.max(prospective, this.dense.length * 2));
      next.set(this.dense);
      this.dense = next;
    } else {
      for (let i = 0; i < this.dense.length; i++) {
        if (this.dense[i] !== 0) this.sparse.set(i, this.dense[i]);
      }
      this.dense = null;
    }
  }

  private trimLength(): void {
    let max = 0;
    for (const i of this.sparse.keys()) if (i + 1 > max) max = i + 1;
    this.length = max;
  }

  // ---------- 压缩 ----------

  /** 强制压缩为紧凑字节表示。返回压缩前后估算字节数。 */
  compact(): { before: number; after: number } {
    const before = this.estimateBytes();
    if (this.packed) return { before, after: before };
    const entries = [...this.entriesInternal()].sort((a, b) => a[0] - b[0]);
    const indices = entries.map((e) => e[0]);
    const values = entries.map((e) => e[1]);
    const idxBytes = encodeDeltaIndices(indices);
    const valBytes = encodeRleValues(values);
    const blob = new Uint8Array(4 + idxBytes.length + valBytes.length);
    new DataView(blob.buffer).setUint32(0, idxBytes.length, true);
    blob.set(idxBytes, 4);
    blob.set(valBytes, 4 + idxBytes.length);
    this.packed = { blob, count: entries.length, maxIndex: this.length === 0 ? -1 : this.length - 1 };
    this.sparse.clear();
    this.dense = null;
    this.coldReads = 0;
    return { before, after: this.estimateBytes() };
  }

  private decompress(): void {
    if (!this.packed) return;
    const { blob, count } = this.packed;
    const idxLen = new DataView(blob.buffer, blob.byteOffset).getUint32(0, true);
    const indices = decodeDeltaIndices(blob.subarray(4, 4 + idxLen), count);
    const values = decodeRleValues(blob.subarray(4 + idxLen), count);
    this.sparse.clear();
    for (let i = 0; i < count; i++) this.sparse.set(indices[i], values[i]);
    this.length = this.packed.maxIndex + 1;
    this.packed = null;
    this.maybeDensify();
  }

  private getPacked(index: number): number {
    const { blob, count } = this.packed!;
    const idxLen = new DataView(blob.buffer, blob.byteOffset).getUint32(0, true);
    const indices = decodeDeltaIndices(blob.subarray(4, 4 + idxLen), count);
    const at = indices.indexOf(index);
    if (at < 0) return 0;
    const values = decodeRleValues(blob.subarray(4 + idxLen), count);
    return values[at];
  }

  // ---------- 内存核算与释放 ----------

  /** 估算当前内部表示占用字节数。 */
  estimateBytes(): number {
    if (this.packed) return this.packed.blob.byteLength + 16;
    if (this.dense) return this.dense.byteLength + DENSE_BASE_BYTES;
    return this.sparse.size * SPARSE_ENTRY_BYTES + SPARSE_BASE_BYTES;
  }

  /** 释放全部内部存储；调用后对象回到空初始状态。 */
  dispose(): void {
    this.sparse.clear();
    this.dense = null;
    this.packed = null;
    this.length = 0;
    this.coldReads = 0;
  }

  get disposedBytes(): number {
    return SPARSE_BASE_BYTES;
  }

  // ---------- 迭代 ----------

  private *entriesInternal(): Generator<[number, number]> {
    if (this.packed) {
      this.decompress();
    }
    if (this.dense) {
      for (let i = 0; i < this.dense.length; i++) {
        if (this.dense[i] !== 0) yield [i, this.dense[i]];
      }
      return;
    }
    yield* this.sparse.entries();
  }

  /** 按下标升序迭代 [index, value]。 */
  *entries(): Generator<[number, number]> {
    const all = [...this.entriesInternal()].sort((a, b) => a[0] - b[0]);
    yield* all;
  }

  forEach(fn: (value: number, index: number) => void): void {
    for (const [i, v] of this.entries()) fn(v, i);
  }
}
