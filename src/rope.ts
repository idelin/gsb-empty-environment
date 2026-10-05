/**
 * A minimal rope: text stored as an ordered list of chunks.
 * The replacement engine reads through the Rope interface, so matches
 * that span chunk boundaries work transparently.
 */
export class Rope {
  readonly chunks: readonly string[];
  /** Start offset of each chunk within the logical text. */
  private readonly offsets: number[];
  readonly length: number;

  constructor(chunks: Iterable<string>) {
    const list: string[] = [];
    const offsets: number[] = [];
    let total = 0;
    for (const chunk of chunks) {
      if (chunk.length === 0) continue;
      offsets.push(total);
      list.push(chunk);
      total += chunk.length;
    }
    this.chunks = list;
    this.offsets = offsets;
    this.length = total;
  }

  static of(...chunks: string[]): Rope {
    return new Rope(chunks);
  }

  private chunkIndexAt(index: number): number {
    // Binary search for the chunk containing `index`.
    let lo = 0;
    let hi = this.offsets.length - 1;
    while (lo < hi) {
      const mid = (lo + hi + 1) >> 1;
      if (this.offsets[mid] <= index) lo = mid;
      else hi = mid - 1;
    }
    return lo;
  }

  charAt(index: number): string {
    if (index < 0 || index >= this.length) return "";
    const c = this.chunkIndexAt(index);
    return this.chunks[c].charAt(index - this.offsets[c]);
  }

  slice(start: number, end: number = this.length): string {
    const s = Math.max(0, start);
    const e = Math.min(this.length, end);
    if (s >= e) return "";
    let out = "";
    let c = this.chunkIndexAt(s);
    let pos = s;
    while (pos < e) {
      const chunk = this.chunks[c];
      const from = pos - this.offsets[c];
      const take = Math.min(chunk.length - from, e - pos);
      out += chunk.substr(from, take);
      pos += take;
      c += 1;
    }
    return out;
  }

  toString(): string {
    return this.chunks.join("");
  }
}
