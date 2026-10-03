const encoder = new TextEncoder();
const decoder = new TextDecoder();

/**
 * Columnar string storage: all rows live in one compact UTF-8 byte array,
 * addressed by a dense offset array. No per-row string objects are kept,
 * so vectorized operators can scan raw bytes without unwrapping strings.
 */
export class StringColumn {
  /** Concatenated UTF-8 bytes of every row. */
  readonly bytes: Uint8Array;
  /** Row boundaries; length === rowCount + 1. Row i spans [offsets[i], offsets[i+1]). */
  readonly offsets: Uint32Array;
  /** Number of rows. */
  readonly length: number;

  constructor(bytes: Uint8Array, offsets: Uint32Array) {
    this.bytes = bytes;
    this.offsets = offsets;
    this.length = offsets.length - 1;
  }

  static from(rows: Iterable<string>): StringColumn {
    const builder = new StringColumnBuilder();
    for (const row of rows) builder.append(row);
    return builder.build();
  }

  /** Byte length of row `i` without materializing the string. */
  byteLengthOf(i: number): number {
    return this.offsets[i + 1] - this.offsets[i];
  }

  /** Raw byte view of row `i` (zero-copy slice of the compact buffer). */
  rowBytes(i: number): Uint8Array {
    return this.bytes.subarray(this.offsets[i], this.offsets[i + 1]);
  }

  /** Decode row `i` to a JS string. Only used on slow paths / result extraction. */
  get(i: number): string {
    return decoder.decode(this.rowBytes(i));
  }
}

export class StringColumnBuilder {
  private chunks: Uint8Array[] = [];
  private offsets: number[] = [0];
  private totalBytes = 0;

  append(value: string): this {
    const encoded = encoder.encode(value);
    this.chunks.push(encoded);
    this.totalBytes += encoded.length;
    this.offsets.push(this.totalBytes);
    return this;
  }

  build(): StringColumn {
    const bytes = new Uint8Array(this.totalBytes);
    let cursor = 0;
    for (const chunk of this.chunks) {
      bytes.set(chunk, cursor);
      cursor += chunk.length;
    }
    return new StringColumn(bytes, Uint32Array.from(this.offsets));
  }
}
