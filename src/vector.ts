/**
 * Columnar memory layout primitives.
 *
 * A `Vector` is a fixed-length chunk of CHUNK_SIZE int32 values stored in a
 * compact, contiguous, 64-byte-aligned buffer so that the kernel loops are
 * friendly to CPU caches (no pointer chasing) and SIMD loads/stores.
 */

export const CHUNK_SIZE = 1024;
export const ALIGNMENT = 64; // cache-line / AVX-512 friendly alignment

export class Vector {
  readonly length: number;
  readonly data: Int32Array;

  constructor(length: number = CHUNK_SIZE) {
    this.length = length;
    // Over-allocate so we can offset the view to a 64-byte boundary.
    const buffer = new ArrayBuffer(length * Int32Array.BYTES_PER_ELEMENT + ALIGNMENT);
    const start = (ALIGNMENT - (buffer.byteOffset % ALIGNMENT)) % ALIGNMENT;
    const byteOffset = (buffer.byteOffset + start) % ALIGNMENT === 0 ? start : 0;
    this.data = new Int32Array(buffer, byteOffset, length);
  }

  static from(values: ArrayLike<number>): Vector {
    const vec = new Vector(values.length);
    vec.data.set(values instanceof Int32Array ? values : Int32Array.from(values));
    return vec;
  }

  /** Byte address alignment of the backing store, in bytes. */
  get alignment(): number {
    const addr = this.data.byteOffset % ALIGNMENT;
    return addr === 0 ? ALIGNMENT : addr;
  }

  get isAligned(): boolean {
    return this.data.byteOffset % ALIGNMENT === 0;
  }
}

/** Deterministic pseudo-random vector for benchmarks/tests. */
export function makeVector(length: number, seed: number): Vector {
  const vec = new Vector(length);
  let state = seed >>> 0 || 1;
  for (let i = 0; i < length; i++) {
    // xorshift32
    state ^= state << 13;
    state ^= state >>> 17;
    state ^= state << 5;
    vec.data[i] = (state >>> 0) % 1000;
  }
  return vec;
}
