/** MSB-first bit writer. */
export class BitWriter {
  private bytes: number[] = [];
  private current = 0;
  private used = 0;

  writeBit(bit: 0 | 1): void {
    this.current = (this.current << 1) | bit;
    this.used++;
    if (this.used === 8) {
      this.bytes.push(this.current);
      this.current = 0;
      this.used = 0;
    }
  }

  /** Flush remaining bits (zero-padded) and return the byte buffer. */
  finish(): Buffer {
    if (this.used > 0) {
      this.bytes.push(this.current << (8 - this.used));
      this.current = 0;
      this.used = 0;
    }
    return Buffer.from(this.bytes);
  }
}

/** MSB-first bit reader; reads past the end yield 0 (standard for arithmetic decoding). */
export class BitReader {
  private byteIndex = 0;
  private bitIndex = 0;
  private readonly data: Buffer;

  constructor(data: Buffer) {
    this.data = data;
  }

  readBit(): 0 | 1 {
    if (this.byteIndex >= this.data.length) return 0;
    const bit = (this.data[this.byteIndex] >> (7 - this.bitIndex)) & 1;
    this.bitIndex++;
    if (this.bitIndex === 8) {
      this.bitIndex = 0;
      this.byteIndex++;
    }
    return bit as 0 | 1;
  }
}
