/**
 * Bit-level I/O helpers.
 *
 * BitWriter accumulates bits LSB-first into 32-bit words (this matches the
 * renormalisation order used by the tANS encoder: bits flushed first are
 * stored first). BitReader walks the same buffer backwards, which is what a
 * streaming ANS decoder does (ANS is a LIFO / stack structure).
 */

export class BitWriter {
  private words: number[] = [];
  private current = 0;
  private used = 0;

  /** Append the lowest `count` bits of `value` (LSB first). */
  write(value: number, count: number): void {
    if (count === 0) return;
    let v = value >>> 0;
    let n = count;
    while (n > 0) {
      const space = 32 - this.used;
      const take = Math.min(space, n);
      const mask = take === 32 ? 0xffffffff : (1 << take) - 1;
      this.current |= (v & mask) << this.used;
      this.used += take;
      v >>>= take;
      n -= take;
      if (this.used === 32) {
        this.words.push(this.current >>> 0);
        this.current = 0;
        this.used = 0;
      }
    }
  }

  get bitLength(): number {
    return this.words.length * 32 + this.used;
  }

  /** Finalise and return the packed bytes plus the exact bit count. */
  finish(): { bytes: Uint8Array; bitLength: number } {
    const all = this.used > 0 ? [...this.words, this.current >>> 0] : [...this.words];
    const bytes = new Uint8Array(all.length * 4);
    for (let i = 0; i < all.length; i++) {
      const w = all[i];
      bytes[i * 4] = w & 0xff;
      bytes[i * 4 + 1] = (w >>> 8) & 0xff;
      bytes[i * 4 + 2] = (w >>> 16) & 0xff;
      bytes[i * 4 + 3] = (w >>> 24) & 0xff;
    }
    return { bytes, bitLength: this.bitLength };
  }
}

export class BitReader {
  private bitPos: number;
  private forwardPos = 0;
  private readonly totalBits: number;
  private readonly bytes: Uint8Array;

  constructor(bytes: Uint8Array, bitLength: number) {
    this.bytes = bytes;
    this.bitPos = bitLength;
    this.totalBits = bitLength;
  }

  get remaining(): number {
    return this.bitPos;
  }

  /** Read `count` bits, consuming from the end of the stream backwards. */
  read(count: number): number {
    if (count === 0) return 0;
    if (count > this.bitPos) {
      throw new Error(`bitstream underflow: need ${count} bits, have ${this.bitPos}`);
    }
    let result = 0;
    let remaining = count;
    while (remaining > 0) {
      this.bitPos--;
      const word = (this.bitPos / 32) | 0;
      const off = this.bitPos % 32;
      const byte = this.bytes[word * 4 + ((off / 8) | 0)];
      const bit = (byte >>> (off % 8)) & 1;
      result = (result << 1) | bit;
      remaining--;
    }
    return result >>> 0;
  }

  /**
   * Read `count` bits in forward (write) order. Bits were packed LSB-first,
   * so the first bit read is the least significant bit of the result.
   */
  readForward(count: number): number {
    if (count > this.totalBits - this.forwardPos) {
      throw new Error("bitstream underflow (forward read)");
    }
    let result = 0;
    for (let i = 0; i < count; i++) {
      const word = (this.forwardPos / 32) | 0;
      const off = this.forwardPos % 32;
      const byte = this.bytes[word * 4 + ((off / 8) | 0)];
      result |= ((byte >>> (off % 8)) & 1) << i;
      this.forwardPos++;
    }
    return result >>> 0;
  }
}
