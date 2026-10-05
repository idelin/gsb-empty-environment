// LSB-first bit stream. The first bit of a stream is bit 0 of byte 0.
// Huffman codes are written most-significant-code-bit first (writeCode);
// extra bits are written least-significant-value-bit first (writeBits).

import { BlockError } from './format.ts';

export class BitWriter {
  private bytes: number[] = [];
  private bitBuf = 0;
  private bitCount = 0;

  // Appends `count` bits of `value`, least significant bit first.
  writeBits(value: number, count: number): void {
    for (let i = 0; i < count; i++) {
      this.bitBuf |= ((value >>> i) & 1) << this.bitCount;
      this.bitCount++;
      if (this.bitCount === 8) {
        this.bytes.push(this.bitBuf);
        this.bitBuf = 0;
        this.bitCount = 0;
      }
    }
  }

  // Appends a canonical Huffman code, most significant code bit first.
  writeCode(code: number, length: number): void {
    for (let i = length - 1; i >= 0; i--) {
      this.writeBits((code >>> i) & 1, 1);
    }
  }

  // Pads the final byte with zero bits and returns the accumulated bytes.
  finish(): Uint8Array {
    if (this.bitCount > 0) {
      this.bytes.push(this.bitBuf);
      this.bitBuf = 0;
      this.bitCount = 0;
    }
    return Uint8Array.from(this.bytes);
  }
}

export class BitReader {
  bitPos = 0;
  private data: Uint8Array;

  constructor(data: Uint8Array) {
    this.data = data;
  }

  get bitsRemaining(): number {
    return this.data.length * 8 - this.bitPos;
  }

  readBit(): number {
    if (this.bitPos >= this.data.length * 8) {
      throw new BlockError('CORRUPT', 'payload bitstream overrun');
    }
    const bit = (this.data[this.bitPos >>> 3] >>> (this.bitPos & 7)) & 1;
    this.bitPos++;
    return bit;
  }

  // Reads `count` bits, least significant bit first.
  readBits(count: number): number {
    let value = 0;
    for (let i = 0; i < count; i++) {
      value |= this.readBit() << i;
    }
    return value;
  }

  // Consumes bits up to the next byte boundary, requiring them to be zero.
  alignToByte(): void {
    while (this.bitPos & 7) {
      if (this.readBit() !== 0) {
        throw new BlockError('CORRUPT', 'non-zero padding bits at end of payload');
      }
    }
  }

  // Byte position; only meaningful right after alignToByte().
  get bytePos(): number {
    return this.bitPos >>> 3;
  }
}
