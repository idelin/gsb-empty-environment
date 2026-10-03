// LSB-first bit stream writer/reader as required by RFC 1951.

export class BitWriter {
  private bytes: number[] = [];
  private bitBuf = 0;
  private bitCnt = 0;

  // Write the `count` least-significant bits of `value`, LSB first.
  writeBits(value: number, count: number): void {
    this.bitBuf |= (value & ((1 << count) - 1)) << this.bitCnt;
    this.bitCnt += count;
    while (this.bitCnt >= 8) {
      this.bytes.push(this.bitBuf & 0xff);
      this.bitBuf >>>= 8;
      this.bitCnt -= 8;
    }
  }

  // Write a Huffman code: the most-significant bit of the code goes first.
  writeCode(code: number, length: number): void {
    let reversed = 0;
    for (let i = 0; i < length; i++) {
      reversed = (reversed << 1) | ((code >>> i) & 1);
    }
    this.writeBits(reversed, length);
  }

  alignToByte(): void {
    if (this.bitCnt > 0) {
      this.bytes.push(this.bitBuf & 0xff);
      this.bitBuf = 0;
      this.bitCnt = 0;
    }
  }

  writeBytes(data: Buffer | Uint8Array): void {
    this.alignToByte();
    for (const b of data) this.bytes.push(b);
  }

  toBuffer(): Buffer {
    this.alignToByte();
    return Buffer.from(this.bytes);
  }
}

export class BitReader {
  private pos = 0; // next byte index in `data`
  private bitBuf = 0;
  private bitCnt = 0;
  private readonly data: Buffer | Uint8Array;

  constructor(data: Buffer | Uint8Array) {
    this.data = data;
  }

  readBits(count: number): number {
    while (this.bitCnt < count) {
      if (this.pos >= this.data.length) {
        throw new Error('inflate: unexpected end of input');
      }
      this.bitBuf |= this.data[this.pos++] << this.bitCnt;
      this.bitCnt += 8;
    }
    const value = this.bitBuf & ((1 << count) - 1);
    this.bitBuf >>>= count;
    this.bitCnt -= count;
    return value;
  }

  // Discard bits up to the next byte boundary (used before stored blocks).
  alignToByte(): void {
    this.bitBuf = 0;
    this.bitCnt = 0;
  }

  readByte(): number {
    if (this.pos >= this.data.length) {
      throw new Error('inflate: unexpected end of input');
    }
    return this.data[this.pos++];
  }

  copyBytes(count: number): Buffer {
    if (this.pos + count > this.data.length) {
      throw new Error('inflate: unexpected end of input');
    }
    const out = Buffer.from(this.data.buffer, this.data.byteOffset + this.pos, count);
    this.pos += count;
    return out;
  }
}
