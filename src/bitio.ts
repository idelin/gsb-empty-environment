/** MSB-first bit writer with byte packing. */
export class BitWriter {
  private bytes: number[] = [];
  private cur = 0;
  private used = 0;
  private total = 0;

  putBit(bit: 0 | 1): void {
    this.cur = (this.cur << 1) | bit;
    this.used++;
    this.total++;
    if (this.used === 8) {
      this.bytes.push(this.cur);
      this.cur = 0;
      this.used = 0;
    }
  }

  get totalBits(): number {
    return this.total;
  }

  /** Pad the partial byte (if any) with zero bits. */
  alignToByte(): void {
    if (this.used > 0) {
      this.bytes.push(this.cur << (8 - this.used));
      this.cur = 0;
      this.used = 0;
    }
  }

  toBytes(): Uint8Array {
    return Uint8Array.from(this.bytes);
  }
}

/** MSB-first bit reader; reads past the end yield 0 bits (flush padding). */
export class BitReader {
  private pos = 0;
  private readonly data: Uint8Array;

  constructor(data: Uint8Array) {
    this.data = data;
  }

  getBit(): 0 | 1 {
    const byteIdx = this.pos >> 3;
    if (byteIdx >= this.data.length) {
      this.pos++;
      return 0;
    }
    const bit = (this.data[byteIdx] >> (7 - (this.pos & 7))) & 1;
    this.pos++;
    return bit as 0 | 1;
  }

  getBits(n: number): number {
    let v = 0;
    for (let i = 0; i < n; i++) v = (v << 1) | this.getBit();
    return v;
  }
}
