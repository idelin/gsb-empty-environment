/** 位级写入器：按 MSB-first 顺序把任意宽度的整数写入比特流。 */
export class BitWriter {
  private bytes: number[] = [];
  private current = 0;
  private used = 0;

  get bitLength(): number {
    return this.bytes.length * 8 + this.used;
  }

  writeBit(bit: number): void {
    this.current = (this.current << 1) | (bit & 1);
    this.used += 1;
    if (this.used === 8) {
      this.bytes.push(this.current);
      this.current = 0;
      this.used = 0;
    }
  }

  /** 写入 value 的低 count 位（高位先写），count 取值 0..32。 */
  writeBits(value: number, count: number): void {
    if (count < 0 || count > 32) {
      throw new RangeError(`writeBits: count ${count} out of range`);
    }
    for (let i = count - 1; i >= 0; i -= 1) {
      this.writeBit((value >>> i) & 1);
    }
  }

  /** 用 0 填充到字节边界。 */
  alignToByte(): void {
    if (this.used !== 0) {
      this.bytes.push(this.current << (8 - this.used));
      this.current = 0;
      this.used = 0;
    }
  }

  /** 收尾（自动对齐到字节）并取出字节序列。 */
  toUint8Array(): Uint8Array {
    this.alignToByte();
    return Uint8Array.from(this.bytes);
  }
}

/** 位级读取器：支持任意比特位置寻址，便于损坏后逐比特扫描同步字。 */
export class BitReader {
  bitPos = 0;

  private readonly data: Uint8Array;

  constructor(data: Uint8Array) {
    this.data = data;
  }

  get remaining(): number {
    return this.data.length * 8 - this.bitPos;
  }

  readBit(): number {
    if (this.remaining <= 0) {
      throw new RangeError('BitReader: end of stream');
    }
    const bit = (this.data[this.bitPos >> 3] >> (7 - (this.bitPos & 7))) & 1;
    this.bitPos += 1;
    return bit;
  }

  readBits(count: number): number {
    let value = 0;
    for (let i = 0; i < count; i += 1) {
      value = (value << 1) | this.readBit();
    }
    return value >>> 0;
  }

  peekBits(count: number): number {
    const saved = this.bitPos;
    const value = this.readBits(count);
    this.bitPos = saved;
    return value;
  }

  seek(bitPos: number): void {
    if (bitPos < 0 || bitPos > this.data.length * 8) {
      throw new RangeError(`seek: bit position ${bitPos} out of range`);
    }
    this.bitPos = bitPos;
  }
}
