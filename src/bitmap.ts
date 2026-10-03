/**
 * 压缩位图：匹配结果的列式表示。
 *
 * 内部按 32 位字（word）紧凑存储，每行占 1 bit，
 * 相比每行一个 boolean / 字符串对象，内存压缩 32 倍以上。
 * 支持与/或/差等按字（word-wise）向量化位运算，
 * 以及面向存储/传输的 RLE（游程）压缩序列化。
 */

const WORD_BITS = 32;

function popcount32(word: number): number {
  let w = word >>> 0;
  w = w - ((w >>> 1) & 0x55555555);
  w = (w & 0x33333333) + ((w >>> 2) & 0x33333333);
  w = (w + (w >>> 4)) & 0x0f0f0f0f;
  return Math.imul(w, 0x01010101) >>> 24;
}

export class Bitmap {
  /** 位图覆盖的行数。 */
  readonly size: number;
  private readonly words: Uint32Array;

  constructor(size: number, words?: Uint32Array) {
    if (size < 0) throw new RangeError("size must be >= 0");
    this.size = size;
    this.words = words ?? new Uint32Array(Math.ceil(size / WORD_BITS));
  }

  static zeroes(size: number): Bitmap {
    return new Bitmap(size);
  }

  static ones(size: number): Bitmap {
    const bitmap = new Bitmap(size);
    bitmap.words.fill(0xffffffff);
    // 清掉尾部超出 size 的位，保证后续运算语义一致。
    const tail = size % WORD_BITS;
    if (tail !== 0 && bitmap.words.length > 0) {
      bitmap.words[bitmap.words.length - 1] = (1 << tail) - 1;
    }
    return bitmap;
  }

  get wordCount(): number {
    return this.words.length;
  }

  /** 置位第 row 行。 */
  set(row: number): void {
    this.words[row >>> 5] |= 1 << (row & 31);
  }

  /** 读取第 row 行是否命中。 */
  get(row: number): boolean {
    return (this.words[row >>> 5] & (1 << (row & 31))) !== 0;
  }

  /** 按字向量化 AND，返回新位图。 */
  and(other: Bitmap): Bitmap {
    this.assertSameSize(other);
    const out = new Bitmap(this.size);
    for (let i = 0; i < this.words.length; i++) {
      out.words[i] = this.words[i] & other.words[i];
    }
    return out;
  }

  /** 按字向量化 OR，返回新位图。 */
  or(other: Bitmap): Bitmap {
    this.assertSameSize(other);
    const out = new Bitmap(this.size);
    for (let i = 0; i < this.words.length; i++) {
      out.words[i] = this.words[i] | other.words[i];
    }
    return out;
  }

  /** 按字向量化 AND NOT（this 中去掉 other 命中的行）。 */
  andNot(other: Bitmap): Bitmap {
    this.assertSameSize(other);
    const out = new Bitmap(this.size);
    for (let i = 0; i < this.words.length; i++) {
      out.words[i] = this.words[i] & ~other.words[i];
    }
    return out;
  }

  /** 命中行数（popcount）。 */
  cardinality(): number {
    let count = 0;
    for (let i = 0; i < this.words.length; i++) {
      count += popcount32(this.words[i]);
    }
    return count;
  }

  /** 按行号升序迭代所有命中行。 */
  *indices(): Generator<number> {
    for (let w = 0; w < this.words.length; w++) {
      let word = this.words[w];
      while (word !== 0) {
        const bit = word & -word;
        const row = w * WORD_BITS + 31 - Math.clz32(bit);
        if (row < this.size) yield row;
        word ^= bit;
      }
    }
  }

  toIndices(): number[] {
    return [...this.indices()];
  }

  /**
   * RLE 游程压缩：把连续相同的字折叠为 (word, count)，
   * 适合全 0 / 全 1 段较长的过滤结果（如低命中率 LIKE）。
   */
  toCompressed(): RleBitmap {
    const words: number[] = [];
    const counts: number[] = [];
    let i = 0;
    while (i < this.words.length) {
      const word = this.words[i];
      let run = 1;
      while (i + run < this.words.length && this.words[i + run] === word) {
        run++;
      }
      words.push(word);
      counts.push(run);
      i += run;
    }
    return new RleBitmap(this.size, Uint32Array.from(words), Uint32Array.from(counts));
  }

  private assertSameSize(other: Bitmap): void {
    if (other.size !== this.size) {
      throw new RangeError(`bitmap size mismatch: ${this.size} vs ${other.size}`);
    }
  }
}

/** RLE 压缩后的位图，可解压回 Bitmap 或直接统计。 */
export class RleBitmap {
  readonly size: number;
  readonly words: Uint32Array;
  readonly counts: Uint32Array;

  constructor(size: number, words: Uint32Array, counts: Uint32Array) {
    if (words.length !== counts.length) {
      throw new RangeError("words/counts length mismatch");
    }
    this.size = size;
    this.words = words;
    this.counts = counts;
  }

  /** 压缩后占用的字（word）数，相对原始字数的比例即压缩率。 */
  get compressedWordCount(): number {
    return this.words.length * 2;
  }

  decompress(): Bitmap {
    const total = this.counts.reduce((acc, c) => acc + c, 0);
    const out = new Uint32Array(total);
    let offset = 0;
    for (let i = 0; i < this.words.length; i++) {
      out.fill(this.words[i], offset, offset + this.counts[i]);
      offset += this.counts[i];
    }
    return new Bitmap(this.size, out);
  }
}
