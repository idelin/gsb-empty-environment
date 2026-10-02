/**
 * 压缩位图：按 256 bit（8 个 uint32 字）分块，只为含 1 的块分配内存。
 * 内存占用严格正比于非零块数量，而非节点总数，适合稀疏可达集。
 */
export const CHUNK_WORDS = 8;
export const CHUNK_BITS = CHUNK_WORDS * 32;

export class CompressedBitmap {
  private chunks = new Map<number, Uint32Array>();

  set(bit: number): void {
    const chunkIndex = (bit / CHUNK_BITS) | 0;
    const word = (bit % CHUNK_BITS) >>> 5;
    const mask = 1 << (bit & 31);
    let chunk = this.chunks.get(chunkIndex);
    if (chunk === undefined) {
      chunk = new Uint32Array(CHUNK_WORDS);
      this.chunks.set(chunkIndex, chunk);
    }
    chunk[word] |= mask;
  }

  has(bit: number): boolean {
    const chunk = this.chunks.get((bit / CHUNK_BITS) | 0);
    if (chunk === undefined) return false;
    return (chunk[((bit % CHUNK_BITS) >>> 5)] & (1 << (bit & 31))) !== 0;
  }

  /** 位运算并行合并：逐块 8 路字级 OR，仅遍历源位图的非零块。返回是否有新位置位。 */
  unionFrom(other: CompressedBitmap): boolean {
    let changed = false;
    for (const [chunkIndex, src] of other.chunks) {
      let dst = this.chunks.get(chunkIndex);
      if (dst === undefined) {
        dst = new Uint32Array(CHUNK_WORDS);
        this.chunks.set(chunkIndex, dst);
      }
      for (let w = 0; w < CHUNK_WORDS; w++) {
        const merged = dst[w] | src[w];
        if (merged !== dst[w]) changed = true;
        dst[w] = merged;
      }
    }
    return changed;
  }

  count(): number {
    let total = 0;
    for (const chunk of this.chunks.values()) {
      for (let w = 0; w < CHUNK_WORDS; w++) {
        let word = chunk[w];
        word = word - ((word >>> 1) & 0x55555555);
        word = (word & 0x33333333) + ((word >>> 2) & 0x33333333);
        total += (((word + (word >>> 4)) & 0x0f0f0f0f) * 0x01010101) >>> 24;
      }
    }
    return total;
  }

  /** 实际占用字节数（Map 项开销按固定估计计入，便于内存约束校验）。 */
  byteSize(): number {
    return this.chunks.size * (CHUNK_WORDS * 4 + 16);
  }

  chunkCount(): number {
    return this.chunks.size;
  }

  *bits(): Generator<number> {
    const entries = [...this.chunks.entries()].sort((a, b) => a[0] - b[0]);
    for (const [chunkIndex, chunk] of entries) {
      for (let w = 0; w < CHUNK_WORDS; w++) {
        let word = chunk[w];
        while (word !== 0) {
          const low = word & -word;
          const offset = 31 - Math.clz32(low);
          yield chunkIndex * CHUNK_BITS + w * 32 + offset;
          word ^= low;
        }
      }
    }
  }
}
