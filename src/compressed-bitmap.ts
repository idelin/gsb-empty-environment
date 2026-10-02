const CHUNK_SHIFT = 5;
const BITS_PER_CHUNK = 32;

function popcount(word: number): number {
  let w = word >>> 0;
  w = w - ((w >>> 1) & 0x55555555);
  w = (w & 0x33333333) + ((w >>> 2) & 0x33333333);
  return Math.imul((w + (w >>> 4)) & 0x0f0f0f0f, 0x01010101) >>> 24;
}

/**
 * 稀疏压缩位图：仅在需要时分配 32 位块（chunk），
 * 内存占用与“非零块数量”成正比，而非与位图长度成正比。
 * 块用 number 存储，位测试/合并不产生堆分配，查询路径零 GC 压力。
 */
export class CompressedBitmap implements Iterable<number> {
  private readonly chunks = new Map<number, number>();

  static from(indices: Iterable<number>): CompressedBitmap {
    const bitmap = new CompressedBitmap();
    for (const index of indices) bitmap.set(index);
    return bitmap;
  }

  set(index: number): void {
    const chunkIndex = index >> CHUNK_SHIFT;
    this.chunks.set(chunkIndex, (this.chunks.get(chunkIndex) ?? 0) | (1 << (index & 31)));
  }

  test(index: number): boolean {
    const word = this.chunks.get(index >> CHUNK_SHIFT);
    return word !== undefined && (word & (1 << (index & 31))) !== 0;
  }

  /** this |= other，返回是否有新位被置 1 */
  orInPlace(other: CompressedBitmap): boolean {
    let changed = false;
    for (const [chunkIndex, word] of other.chunks) {
      const current = this.chunks.get(chunkIndex) ?? 0;
      const next = current | word;
      if (next !== current) {
        this.chunks.set(chunkIndex, next);
        changed = true;
      }
    }
    return changed;
  }

  /** 返回 this & ~other（差集），用于增量传播的 delta 计算 */
  subtract(other: CompressedBitmap): CompressedBitmap {
    const result = new CompressedBitmap();
    for (const [chunkIndex, word] of this.chunks) {
      const diff = word & ~(other.chunks.get(chunkIndex) ?? 0);
      if (diff !== 0) result.chunks.set(chunkIndex, diff);
    }
    return result;
  }

  get isEmpty(): boolean {
    return this.chunks.size === 0;
  }

  cardinality(): number {
    let total = 0;
    for (const word of this.chunks.values()) total += popcount(word);
    return total;
  }

  *indices(): IterableIterator<number> {
    const keys = [...this.chunks.keys()].sort((a, b) => a - b);
    for (const chunkIndex of keys) {
      let word = this.chunks.get(chunkIndex) as number;
      const base = chunkIndex << CHUNK_SHIFT;
      while (word !== 0) {
        const lowest = word & -word;
        yield base + 31 - Math.clz32(lowest);
        word ^= lowest;
      }
    }
  }

  [Symbol.iterator](): IterableIterator<number> {
    return this.indices();
  }

  clone(): CompressedBitmap {
    const copy = new CompressedBitmap();
    for (const [chunkIndex, word] of this.chunks) copy.chunks.set(chunkIndex, word);
    return copy;
  }

  equals(other: CompressedBitmap): boolean {
    if (this.chunks.size !== other.chunks.size) return false;
    for (const [chunkIndex, word] of this.chunks) {
      if (other.chunks.get(chunkIndex) !== word) return false;
    }
    return true;
  }

  get chunkCount(): number {
    return this.chunks.size;
  }

  /** 估算内存占用：固定开销 + 每个存活块 12 字节（8 字节键 + 4 字节 32 位字） */
  byteSize(): number {
    return 32 + this.chunks.size * 12;
  }

  toString(): string {
    return `CompressedBitmap(bits=${this.cardinality()}, chunks=${this.chunks.size})`;
  }
}
