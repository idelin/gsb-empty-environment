/**
 * Compressed match bitmap: 1 bit per row, packed into 32-bit words.
 * Iteration skips all-zero words, so sparse results are scanned without
 * touching individual bits; cardinality uses word-level popcount.
 */
export class Bitmap {
  /** Number of addressable rows. */
  readonly size: number;
  /** Packed bits; bit (i & 31) of words[i >>> 5] answers row i. */
  readonly words: Uint32Array;

  constructor(size: number, words?: Uint32Array) {
    this.size = size;
    this.words = words ?? new Uint32Array((size + 31) >>> 5);
  }

  /** Compressed size in bytes (1 bit per row). */
  get byteLength(): number {
    return this.words.byteLength;
  }

  set(i: number): void {
    this.words[i >>> 5] |= 1 << (i & 31);
  }

  get(i: number): boolean {
    return (this.words[i >>> 5] & (1 << (i & 31))) !== 0;
  }

  cardinality(): number {
    let count = 0;
    const words = this.words;
    for (let w = 0; w < words.length; w++) {
      let word = words[w];
      // Kernighan popcount: one iteration per set bit.
      while (word !== 0) {
        word &= word - 1;
        count++;
      }
    }
    return count;
  }

  /** Visit each set row index; skips empty 32-row blocks entirely. */
  forEachSet(fn: (rowIndex: number) => void): void {
    const words = this.words;
    for (let w = 0; w < words.length; w++) {
      let word = words[w];
      while (word !== 0) {
        const lowest = word & -word; // isolate lowest set bit
        const bit = 31 - Math.clz32(lowest);
        const row = (w << 5) + bit;
        if (row >= this.size) return;
        fn(row);
        word &= word - 1;
      }
    }
  }

  /** Materialize matching row indices as a dense typed array. */
  toIndices(): Uint32Array {
    const out = new Uint32Array(this.cardinality());
    let cursor = 0;
    this.forEachSet((row) => {
      out[cursor++] = row;
    });
    return out;
  }
}
