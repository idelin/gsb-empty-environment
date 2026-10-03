/**
 * 字符串列的紧凑列式存储。
 *
 * 全部行的 UTF-8 字节连续存放在一个 Uint8Array 中，
 * 用 Uint32Array 偏移数组（长度 = 行数 + 1）切分每一行。
 * 过滤算子直接在底层字节数组上比对，全程不产生
 * 每行的 JS 字符串对象，避免频繁解包。
 */

const encoder = new TextEncoder();
const decoder = new TextDecoder();

export class StringColumn {
  /** 所有行拼接后的 UTF-8 字节。 */
  readonly data: Uint8Array;
  /** 行偏移数组，第 i 行字节区间为 [offsets[i], offsets[i+1])。 */
  readonly offsets: Uint32Array;

  constructor(data: Uint8Array, offsets: Uint32Array) {
    if (offsets.length === 0) throw new RangeError("offsets must have at least 1 entry");
    if (offsets[0] !== 0) throw new RangeError("offsets[0] must be 0");
    if (offsets[offsets.length - 1] !== data.length) {
      throw new RangeError("last offset must equal data length");
    }
    this.data = data;
    this.offsets = offsets;
  }

  static builder(estimatedRows = 1024): StringColumnBuilder {
    return new StringColumnBuilder(estimatedRows);
  }

  static from(rows: Iterable<string>): StringColumn {
    const builder = new StringColumnBuilder();
    for (const row of rows) builder.append(row);
    return builder.build();
  }

  get rowCount(): number {
    return this.offsets.length - 1;
  }

  /** 第 row 行的字节长度（不解码）。 */
  byteLength(row: number): number {
    return this.offsets[row + 1] - this.offsets[row];
  }

  /**
   * 第 row 行的字节视图（零拷贝 subarray）。
   * 供向量化算子直接在紧凑字节数组上扫描。
   */
  rowBytes(row: number): Uint8Array {
    return this.data.subarray(this.offsets[row], this.offsets[row + 1]);
  }

  /** 仅在物化结果时才解码为 JS 字符串。 */
  get(row: number): string {
    return decoder.decode(this.rowBytes(row));
  }

  /** 按位图物化命中行为字符串数组（演示/调试用）。 */
  collect(bitmap: { indices(): Generator<number> }): string[] {
    const out: string[] = [];
    for (const row of bitmap.indices()) out.push(this.get(row));
    return out;
  }
}

export class StringColumnBuilder {
  private bytes: Uint8Array;
  private used = 0;
  private offsets: number[] = [0];

  constructor(estimatedRows = 1024) {
    this.bytes = new Uint8Array(Math.max(64, estimatedRows * 16));
  }

  append(value: string): void {
    const encoded = encoder.encode(value);
    this.ensureCapacity(encoded.length);
    this.bytes.set(encoded, this.used);
    this.used += encoded.length;
    this.offsets.push(this.used);
  }

  build(): StringColumn {
    return new StringColumn(
      this.bytes.slice(0, this.used),
      Uint32Array.from(this.offsets),
    );
  }

  private ensureCapacity(extra: number): void {
    if (this.used + extra <= this.bytes.length) return;
    let capacity = this.bytes.length * 2;
    while (capacity < this.used + extra) capacity *= 2;
    const grown = new Uint8Array(capacity);
    grown.set(this.bytes.subarray(0, this.used));
    this.bytes = grown;
  }
}
