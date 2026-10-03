/**
 * MSB-first bit stream writer/reader (same bit order as Fax Group 3/4).
 */
export class BitWriter {
  private buffer = new Uint8Array(1 << 16);
  private size = 0;
  private accumulator = 0;
  private bitCount = 0;

  get bitLength(): number {
    return this.size * 8 + this.bitCount;
  }

  writeBits(value: number, count: number): void {
    if (count <= 0) return;
    const masked = count >= 32 ? value >>> 0 : value & ((1 << count) - 1);
    // arithmetic (not bitwise) accumulation stays exact beyond 32 bits
    this.accumulator = this.accumulator * 2 ** count + masked;
    this.bitCount += count;
    while (this.bitCount >= 8) {
      const shift = this.bitCount - 8;
      const byte = Math.floor(this.accumulator / 2 ** shift);
      this.accumulator -= byte * 2 ** shift;
      this.bitCount = shift;
      this.pushByte(byte);
    }
  }

  private pushByte(byte: number): void {
    if (this.size === this.buffer.length) {
      const grown = new Uint8Array(this.buffer.length * 2);
      grown.set(this.buffer);
      this.buffer = grown;
    }
    this.buffer[this.size] = byte;
    this.size += 1;
  }

  toUint8Array(): Uint8Array {
    const out = new Uint8Array(this.size + (this.bitCount > 0 ? 1 : 0));
    out.set(this.buffer.subarray(0, this.size));
    if (this.bitCount > 0) {
      out[out.length - 1] = this.accumulator * 2 ** (8 - this.bitCount);
    }
    return out;
  }
}

export class BitReader {
  private bytePos: number;
  private bitPos = 0;
  private readonly data: Uint8Array;

  constructor(data: Uint8Array, byteOffset = 0) {
    this.data = data;
    this.bytePos = byteOffset;
  }

  get bytesConsumed(): number {
    return this.bytePos + (this.bitPos > 0 ? 1 : 0);
  }

  readBit(): number {
    if (this.bytePos >= this.data.length) {
      throw new Error('BitReader: unexpected end of data');
    }
    const bit = (this.data[this.bytePos] >>> (7 - this.bitPos)) & 1;
    this.bitPos += 1;
    if (this.bitPos === 8) {
      this.bitPos = 0;
      this.bytePos += 1;
    }
    return bit;
  }

  readBits(count: number): number {
    let value = 0;
    for (let i = 0; i < count; i++) {
      value = (value << 1) | this.readBit();
    }
    return value >>> 0;
  }
}
