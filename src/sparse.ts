import { decodeVarint, encodeVarint, varintLength, zigzagDecode, zigzagEncode } from "./varint.ts";

/**
 * Sparse-data & memory-compact optimizer.
 *
 * Three cooperating pieces:
 *  - {@link SparseVector}: adaptive container that switches between a sparse
 *    hash map and a dense array depending on fill density / memory pressure.
 *  - {@link rleCompress} / {@link rleDecompress}: run-length encoding for
 *    data dominated by a fill value (e.g. zeros).
 *  - {@link packDeltas} / {@link unpackDeltas}: zigzag + varint delta packing
 *    for monotonic-ish integer sequences (e.g. sparse indices).
 */

const MAP_ENTRY_BYTES = 24; // estimated Map entry overhead (key+value+slot)
const DENSE_SLOT_BYTES = 8; // number slot in a dense Float64-ish array

export type ContainerMode = "sparse" | "dense";

export interface OptimizeStats {
  mode: ContainerMode;
  length: number;
  nonDefault: number;
  density: number;
  sparseBytes: number;
  denseBytes: number;
  currentBytes: number;
}

/**
 * Adaptive numeric vector over a huge logical index space.
 * Starts sparse; converts to dense when density makes it cheaper, and back
 * to sparse when the fill ratio collapses. All transitions are atomic from
 * the caller's perspective (build-then-swap), so concurrent readers that
 * hold an old snapshot iterator never observe a torn state.
 */
export class SparseVector {
  private readonly fill: number;
  private readonly denseThreshold: number;
  private map: Map<number, number> | null = new Map();
  private dense: Float64Array | null = null;
  private len = 0;
  /** Non-default entry count, maintained incrementally (O(1) per write). */
  private live = 0;

  constructor(opts: { fill?: number; denseThreshold?: number } = {}) {
    this.fill = opts.fill ?? 0;
    this.denseThreshold = opts.denseThreshold ?? 0.5;
  }

  get mode(): ContainerMode {
    return this.dense ? "dense" : "sparse";
  }

  get length(): number {
    return this.len;
  }

  get size(): number {
    return this.live;
  }

  get(index: number): number {
    this.checkIndex(index, false);
    if (this.dense) return this.dense[index]!;
    return this.map!.get(index) ?? this.fill;
  }

  set(index: number, value: number): void {
    this.checkIndex(index, true);
    if (this.dense) {
      if (index >= this.dense.length) this.growDense(index + 1);
      const wasDefault = this.dense[index] === this.fill;
      const isDefault = value === this.fill;
      if (wasDefault && !isDefault) this.live++;
      else if (!wasDefault && isDefault) this.live--;
      this.dense[index] = value;
    } else if (value === this.fill) {
      if (this.map!.delete(index)) this.live--;
    } else {
      if (!this.map!.has(index)) this.live++;
      this.map!.set(index, value);
    }
    this.adapt();
  }

  delete(index: number): void {
    this.set(index, this.fill);
  }

  private checkIndex(index: number, grow: boolean): void {
    if (!Number.isInteger(index) || index < 0) {
      throw new RangeError(`index must be a non-negative integer, got ${index}`);
    }
    if (grow && index >= this.len) this.len = index + 1;
    if (!grow && index >= this.len) {
      throw new RangeError(`index ${index} out of bounds (length ${this.len})`);
    }
  }

  /** Estimated heap footprint of the current representation, in bytes. */
  byteSize(): number {
    return this.dense
      ? this.dense.byteLength
      : this.map!.size * MAP_ENTRY_BYTES + 64;
  }

  private sparseCost(): number {
    return this.size * MAP_ENTRY_BYTES + 64;
  }

  private denseCost(): number {
    return Math.max(this.len, 1) * DENSE_SLOT_BYTES;
  }

  /** Re-evaluate representation; convert when the other mode is cheaper. */
  adapt(): void {
    if (this.dense) {
      const density = this.len === 0 ? 0 : this.size / this.len;
      if (density < this.denseThreshold * 0.5 && this.sparseCost() < this.denseCost()) {
        this.toSparse();
      }
    } else {
      const density = this.len === 0 ? 0 : this.map!.size / Math.max(this.len, 1);
      if (density >= this.denseThreshold && this.denseCost() <= this.sparseCost()) {
        this.toDense();
      }
    }
  }

  private toDense(): void {
    const src = this.map!;
    const next = new Float64Array(Math.max(this.len, 16));
    if (this.fill !== 0) next.fill(this.fill);
    for (const [i, v] of src) next[i] = v;
    this.dense = next;
    this.map = null; // release sparse storage before swap completes
    this.live = src.size;
  }

  /** Grow the dense backing store so `minCapacity` slots are addressable. */
  private growDense(minCapacity: number): void {
    const cur = this.dense!;
    let cap = Math.max(cur.length, 16);
    while (cap < minCapacity) cap *= 2;
    const next = new Float64Array(cap);
    if (this.fill !== 0) next.fill(this.fill);
    next.set(cur);
    this.dense = next;
  }

  private toSparse(): void {
    const src = this.dense!;
    const next = new Map<number, number>();
    for (let i = 0; i < src.length; i++) {
      if (src[i] !== this.fill) next.set(i, src[i]!);
    }
    this.map = next;
    this.dense = null;
    this.live = next.size;
  }

  /** Iterate non-default entries in ascending index order. */
  *entries(): IterableIterator<[number, number]> {
    if (this.dense) {
      const snapshot = this.dense;
      for (let i = 0; i < snapshot.length; i++) {
        if (snapshot[i] !== this.fill) yield [i, snapshot[i]!];
      }
    } else {
      const sorted = [...this.map!.keys()].sort((a, b) => a - b);
      for (const i of sorted) yield [i, this.map!.get(i)!];
    }
  }

  stats(): OptimizeStats {
    const nonDefault = this.size;
    const density = this.len === 0 ? 0 : nonDefault / this.len;
    return {
      mode: this.mode,
      length: this.len,
      nonDefault,
      density,
      sparseBytes: this.sparseCost(),
      denseBytes: this.denseCost(),
      currentBytes: this.byteSize(),
    };
  }

  /** Release all storage; afterwards the vector is empty and reusable. */
  clear(): void {
    this.map = new Map();
    this.dense = null;
    this.len = 0;
    this.live = 0;
  }
}

/* ------------------------- run-length compression ------------------------ */

export interface RleBlock {
  /** [value, runLength] pairs, flattened. */
  pairs: number[];
  /** Logical length of the decompressed vector. */
  length: number;
}

/** Compress a numeric array into [value, count] run pairs. */
export function rleCompress(values: ArrayLike<number>): RleBlock {
  const pairs: number[] = [];
  const n = values.length;
  let i = 0;
  while (i < n) {
    const v = values[i]!;
    let run = 1;
    while (i + run < n && values[i + run] === v) run++;
    pairs.push(v, run);
    i += run;
  }
  return { pairs, length: n };
}

export function rleDecompress(block: RleBlock): number[] {
  const { pairs, length } = block;
  if (pairs.length % 2 !== 0) {
    throw new Error("corrupt RLE block: odd pair count");
  }
  const out: number[] = [];
  for (let i = 0; i < pairs.length; i += 2) {
    const value = pairs[i]!;
    const count = pairs[i + 1]!;
    if (!Number.isInteger(count) || count <= 0) {
      throw new Error(`corrupt RLE block: bad run length ${count}`);
    }
    for (let j = 0; j < count; j++) out.push(value);
  }
  if (out.length !== length) {
    throw new Error(`corrupt RLE block: expected ${length} items, got ${out.length}`);
  }
  return out;
}

/* ---------------------- delta + zigzag + varint pack --------------------- */

/**
 * Pack an integer sequence as zigzag-encoded deltas in a varint stream.
 * Ideal for sorted sparse indices: mostly small positive gaps -> 1 byte each.
 */
export function packDeltas(values: ArrayLike<number | bigint>): Uint8Array {
  const chunks: Uint8Array[] = [];
  let total = 0;
  const push = (c: Uint8Array) => {
    chunks.push(c);
    total += c.length;
  };
  push(encodeVarint(values.length));
  let prev = 0n;
  for (let i = 0; i < values.length; i++) {
    const v = BigInt(values[i]!);
    const delta = v - prev;
    prev = v;
    push(encodeVarint(zigzagEncode(delta)));
  }
  const out = new Uint8Array(total);
  let off = 0;
  for (const c of chunks) {
    out.set(c, off);
    off += c.length;
  }
  return out;
}

export function unpackDeltas(buf: Uint8Array): bigint[] {
  let offset = 0;
  const read = (): bigint => {
    const { value, bytesRead } = decodeVarint(buf, offset);
    offset += bytesRead;
    return value;
  };
  const count = Number(read());
  const out: bigint[] = new Array(count);
  let prev = 0n;
  for (let i = 0; i < count; i++) {
    prev += zigzagDecode(read());
    out[i] = prev;
  }
  if (offset !== buf.length) {
    throw new Error(`corrupt delta pack: ${buf.length - offset} trailing byte(s)`);
  }
  return out;
}

/** Compressed size estimate helper for tests and the demo. */
export function packedSize(values: ArrayLike<number | bigint>): number {
  let total = varintLength(values.length);
  let prev = 0n;
  for (let i = 0; i < values.length; i++) {
    const v = BigInt(values[i]!);
    total += varintLength(zigzagEncode(v - prev));
    prev = v;
  }
  return total;
}
