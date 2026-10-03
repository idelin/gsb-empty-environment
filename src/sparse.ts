import { encodeSparseEntries, decodeSparseEntries } from './codec.ts';

export class MemoryAccountant {
  private totalBytes = 0;

  alloc(bytes: number): void {
    if (bytes < 0) throw new RangeError('alloc requires a non-negative byte count');
    this.totalBytes += bytes;
  }

  free(bytes: number): void {
    if (bytes < 0) throw new RangeError('free requires a non-negative byte count');
    this.totalBytes -= bytes;
    if (this.totalBytes < 0) throw new Error('memory accounting underflow');
  }

  get bytes(): number {
    return this.totalBytes;
  }
}

export interface SparseVectorOptions {
  denseThreshold?: number;
  sparseThreshold?: number;
  maxDenseLength?: number;
  accountant?: MemoryAccountant;
}

export type SparseVectorMode = 'empty' | 'sparse' | 'dense';

const BASE_BYTES = 64;
const SPARSE_ENTRY_BYTES = 48;
const DENSE_VALUE_BYTES = 8;

export class SparseVector {
  static readonly DEFAULT_MAX_DENSE_LENGTH = 1 << 20;

  private sparse: Map<number, number> | null = new Map();
  private dense: Array<number | undefined> | null = null;
  private length = 0;
  private fillCount = 0;
  private accountedBytes = 0;
  private readonly denseThreshold: number;
  private readonly sparseThreshold: number;
  private readonly maxDenseLength: number;
  private readonly accountant?: MemoryAccountant;

  constructor(options: SparseVectorOptions = {}) {
    this.denseThreshold = options.denseThreshold ?? 0.6;
    this.sparseThreshold = options.sparseThreshold ?? 0.25;
    this.maxDenseLength = options.maxDenseLength ?? SparseVector.DEFAULT_MAX_DENSE_LENGTH;
    this.accountant = options.accountant;
    if (!(this.sparseThreshold < this.denseThreshold)) {
      throw new RangeError('sparseThreshold must be smaller than denseThreshold');
    }
    this.syncAccounting();
  }

  get size(): number {
    return this.fillCount;
  }

  get capacity(): number {
    return this.length;
  }

  get mode(): SparseVectorMode {
    if (this.dense) return 'dense';
    return this.fillCount === 0 ? 'empty' : 'sparse';
  }

  get density(): number {
    return this.length === 0 ? 0 : this.fillCount / this.length;
  }

  has(index: number): boolean {
    this.checkIndex(index);
    if (this.dense) return this.dense[index] !== undefined;
    return this.sparse!.has(index);
  }

  get(index: number): number {
    this.checkIndex(index);
    if (this.dense) return this.dense[index] ?? 0;
    return this.sparse!.get(index) ?? 0;
  }

  set(index: number, value: number): void {
    this.checkIndex(index);
    if (!Number.isSafeInteger(value)) {
      throw new RangeError(`value must be a safe integer, got ${value}`);
    }
    if (value === 0) {
      this.delete(index);
      return;
    }
    if (this.dense && index >= this.maxDenseLength) this.toSparse();
    const existed = this.has(index);
    if (this.dense) this.dense[index] = value;
    else this.sparse!.set(index, value);
    if (!existed) this.fillCount++;
    if (index >= this.length) this.length = index + 1;
    this.adapt();
    this.syncAccounting();
  }

  delete(index: number): boolean {
    this.checkIndex(index);
    let existed = false;
    if (this.dense) {
      existed = this.dense[index] !== undefined;
      if (existed) delete this.dense[index];
    } else {
      existed = this.sparse!.delete(index);
    }
    if (existed) this.fillCount--;
    this.adapt();
    this.syncAccounting();
    return existed;
  }

  clear(): void {
    this.sparse = new Map();
    this.dense = null;
    this.length = 0;
    this.fillCount = 0;
    this.syncAccounting();
  }

  *entries(): IterableIterator<[number, number]> {
    if (this.dense) {
      for (let i = 0; i < this.length; i++) {
        const v = this.dense[i];
        if (v !== undefined) yield [i, v];
      }
    } else {
      const keys = [...this.sparse!.keys()].sort((a, b) => a - b);
      for (const k of keys) yield [k, this.sparse!.get(k)!];
    }
  }

  byteSize(): number {
    if (this.dense) return BASE_BYTES + DENSE_VALUE_BYTES * this.length;
    if (this.fillCount === 0) return 0;
    return BASE_BYTES + SPARSE_ENTRY_BYTES * this.fillCount;
  }

  serialize(): Uint8Array {
    return encodeSparseEntries([...this.entries()]);
  }

  static deserialize(buf: Uint8Array, options: SparseVectorOptions = {}): SparseVector {
    const vector = new SparseVector(options);
    const entries = decodeSparseEntries(buf);
    for (const [index, value] of entries) vector.sparse!.set(index, value);
    vector.fillCount = entries.length;
    vector.length = entries.length === 0 ? 0 : entries[entries.length - 1][0] + 1;
    vector.adapt();
    vector.syncAccounting();
    return vector;
  }

  private adapt(): void {
    const density = this.density;
    if (this.dense === null) {
      if (this.fillCount > 0 && density >= this.denseThreshold && this.length <= this.maxDenseLength) {
        this.toDense();
      }
    } else if (density < this.sparseThreshold || this.length > this.maxDenseLength) {
      this.toSparse();
    }
  }

  private toDense(): void {
    const arr: Array<number | undefined> = new Array(this.length);
    for (const [index, value] of this.sparse!) arr[index] = value;
    this.dense = arr;
    this.sparse = null;
  }

  private toSparse(): void {
    const map = new Map<number, number>();
    if (this.dense) {
      for (let i = 0; i < this.length; i++) {
        const v = this.dense[i];
        if (v !== undefined) map.set(i, v);
      }
    }
    this.sparse = map;
    this.dense = null;
  }

  private syncAccounting(): void {
    if (!this.accountant) return;
    const next = this.byteSize();
    const delta = next - this.accountedBytes;
    if (delta > 0) this.accountant.alloc(delta);
    else if (delta < 0) this.accountant.free(-delta);
    this.accountedBytes = next;
  }

  private checkIndex(index: number): void {
    if (!Number.isSafeInteger(index) || index < 0) {
      throw new RangeError(`index must be a non-negative safe integer, got ${index}`);
    }
  }
}
