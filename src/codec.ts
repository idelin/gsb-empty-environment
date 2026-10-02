/** 变长整数与定长浮点的二进制读写，零依赖。 */

export class ByteWriter {
  private bytes: number[] = [];

  get length(): number {
    return this.bytes.length;
  }

  pushByte(b: number): void {
    this.bytes.push(b & 0xff);
  }

  pushBytes(data: Uint8Array): void {
    for (const b of data) this.bytes.push(b);
  }

  /** 无符号 varint，支持 < 2^53 的安全整数。 */
  pushUvarint(value: number): void {
    let n = value;
    while (n >= 0x80) {
      this.bytes.push((n % 0x80) | 0x80);
      n = Math.floor(n / 0x80);
    }
    this.bytes.push(n);
  }

  /** zigzag 编码的有符号 varint。 */
  pushSvarint(value: number): void {
    this.pushUvarint(value >= 0 ? 2 * value : -2 * value - 1);
  }

  pushF64(value: number): void {
    const buf = new ArrayBuffer(8);
    new DataView(buf).setFloat64(0, value, false);
    this.pushBytes(new Uint8Array(buf));
  }

  toUint8Array(): Uint8Array {
    return Uint8Array.from(this.bytes);
  }
}

export class ByteReader {
  private offset = 0;
  private readonly data: Uint8Array;

  constructor(data: Uint8Array) {
    this.data = data;
  }

  get remaining(): number {
    return this.data.length - this.offset;
  }

  readByte(): number {
    if (this.offset >= this.data.length) throw new RangeError('buffer underflow');
    return this.data[this.offset++];
  }

  readUvarint(): number {
    let result = 0;
    let shift = 1;
    for (;;) {
      const b = this.readByte();
      result += (b & 0x7f) * shift;
      if ((b & 0x80) === 0) return result;
      shift *= 0x80;
    }
  }

  readSvarint(): number {
    const n = this.readUvarint();
    return n % 2 === 0 ? n / 2 : -(n + 1) / 2;
  }

  readF64(): number {
    if (this.remaining < 8) throw new RangeError('buffer underflow');
    const view = new DataView(
      this.data.buffer,
      this.data.byteOffset + this.offset,
      8,
    );
    this.offset += 8;
    return view.getFloat64(0, false);
  }
}
