/**
 * LSB-first bit stream primitives, as required by RFC 1951:
 * data elements are packed starting with the least-significant bit of a byte,
 * while Huffman codes are packed starting with the most-significant code bit.
 */

export class BitWriter {
  private bytes: number[] = [];
  private bitBuf = 0;
  private bitCnt = 0;

  /** Write the `count` least-significant bits of `value` (count <= 24). */
  writeBits(value: number, count: number): void {
    this.bitBuf |= (value & ((1 << count) - 1)) << this.bitCnt;
    this.bitCnt += count;
    while (this.bitCnt >= 8) {
      this.bytes.push(this.bitBuf & 0xff);
      this.bitBuf >>>= 8;
      this.bitCnt -= 8;
    }
  }

  /** Write a Huffman code: `code` is the canonical (MSB-first) code of length `len`. */
  writeCode(code: number, len: number): void {
    let reversed = 0;
    for (let i = 0; i < len; i++) {
      reversed = (reversed << 1) | ((code >>> i) & 1);
    }
    this.writeBits(reversed, len);
  }

  /** Pad with zero bits to the next byte boundary. */
  alignToByte(): void {
    if (this.bitCnt > 0) {
      this.bytes.push(this.bitBuf & 0xff);
      this.bitBuf = 0;
      this.bitCnt = 0;
    }
  }

  writeBytes(data: Uint8Array): void {
    this.alignToByte();
    for (const b of data) this.bytes.push(b);
  }

  get length(): number {
    return this.bytes.length + (this.bitCnt > 0 ? 1 : 0);
  }

  toUint8Array(): Uint8Array {
    this.alignToByte();
    return Uint8Array.from(this.bytes);
  }
}

export class BitReader {
  private bytePos = 0;
  private bitBuf = 0;
  private bitCnt = 0;

  private readonly data: Uint8Array;

  constructor(data: Uint8Array) {
    this.data = data;
  }

  /** Read the `count` (<= 24) next bits, LSB-first. */
  readBits(count: number): number {
    while (this.bitCnt < count) {
      if (this.bytePos >= this.data.length) {
        throw new Error("inflate: unexpected end of input");
      }
      this.bitBuf |= this.data[this.bytePos++] << this.bitCnt;
      this.bitCnt += 8;
    }
    const value = this.bitBuf & ((1 << count) - 1);
    this.bitBuf >>>= count;
    this.bitCnt -= count;
    return value;
  }

  readBit(): number {
    return this.readBits(1);
  }

  /** Discard bits up to the next byte boundary. */
  alignToByte(): void {
    this.bitBuf = 0;
    this.bitCnt = 0;
  }

  readByte(): number {
    if (this.bytePos >= this.data.length) {
      throw new Error("inflate: unexpected end of input");
    }
    return this.data[this.bytePos++];
  }

  readBytes(count: number): Uint8Array {
    if (this.bytePos + count > this.data.length) {
      throw new Error("inflate: unexpected end of input");
    }
    const out = this.data.subarray(this.bytePos, this.bytePos + count);
    this.bytePos += count;
    return out;
  }

  get bytesRemaining(): number {
    return this.data.length - this.bytePos;
  }
}
