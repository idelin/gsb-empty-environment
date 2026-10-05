/**
 * A minimal immutable rope: text stored as an ordered list of chunks.
 * All reads go through chunk-aware accessors so callers never need to
 * flatten the rope, and logical positions may fall anywhere, including
 * in the middle of a chunk boundary region.
 */
export class Rope {
  private readonly chunks: readonly string[];
  private readonly offsets: readonly number[];
  readonly length: number;

  private constructor(chunks: readonly string[]) {
    this.chunks = chunks;
    const offsets: number[] = [];
    let total = 0;
    for (const chunk of chunks) {
      offsets.push(total);
      total += chunk.length;
    }
    this.offsets = offsets;
    this.length = total;
  }

  static from(chunks: readonly string[]): Rope {
    if (chunks.length === 0) {
      throw new RangeError("Rope.from requires at least one chunk");
    }
    return new Rope(chunks);
  }

  static of(text: string): Rope {
    return new Rope([text]);
  }

  private chunkIndexAt(index: number): number {
    let lo = 0;
    let hi = this.chunks.length - 1;
    while (lo < hi) {
      const mid = (lo + hi + 1) >> 1;
      if (this.offsets[mid] <= index) lo = mid;
      else hi = mid - 1;
    }
    return lo;
  }

  charAt(index: number): string {
    if (index < 0 || index >= this.length) {
      throw new RangeError(`charAt index ${index} out of bounds (length ${this.length})`);
    }
    const chunk = this.chunkIndexAt(index);
    return this.chunks[chunk][index - this.offsets[chunk]];
  }

  slice(start: number, end: number = this.length): string {
    const from = Math.max(0, start);
    const to = Math.min(this.length, end);
    if (from >= to) return "";
    let out = "";
    let chunk = this.chunkIndexAt(from);
    let pos = from;
    while (pos < to) {
      const chunkStart = this.offsets[chunk];
      const piece = this.chunks[chunk].slice(pos - chunkStart, to - chunkStart);
      out += piece;
      pos += piece.length;
      chunk += 1;
    }
    return out;
  }

  toString(): string {
    return this.chunks.join("");
  }
}
