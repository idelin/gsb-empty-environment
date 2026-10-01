// Bit-level stream I/O. Bits are packed MSB-first within each byte.

export class BitWriter {
  private bytes: number[] = [];
  private current: number = 0;
  private used: number = 0;

  writeBit(bit: number): void {
    this.current = (this.current << 1) | (bit & 1);
    this.used += 1;
    if (this.used === 8) {
      this.bytes.push(this.current);
      this.current = 0;
      this.used = 0;
    }
  }

  get bitLength(): number {
    return this.bytes.length * 8 + this.used;
  }

  finish(): Uint8Array {
    const out: number[] = this.bytes.slice();
    if (this.used > 0) {
      out.push(this.current << (8 - this.used));
    }
    return Uint8Array.from(out);
  }
}

export class BitReader {
  private data: Uint8Array;
  private pos: number = 0;

  constructor(data: Uint8Array) {
    this.data = data;
  }

  get bitsRead(): number {
    return this.pos;
  }

  // Reads beyond the end of the stream return 0 (zero padding).
  readBit(): number {
    const byteIndex: number = this.pos >> 3;
    const bitIndex: number = this.pos & 7;
    this.pos += 1;
    if (byteIndex >= this.data.length) {
      return 0;
    }
    return (this.data[byteIndex] >> (7 - bitIndex)) & 1;
  }
}
