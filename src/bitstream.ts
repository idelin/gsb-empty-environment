/**
 * MSB-first bit stream writer/reader (same bit order as CCITT T.4 fax).
 *
 * Both classes buffer bits in a small integer accumulator kept within
 * int32 range, so the hot encode/decode loops run on fast bitwise ops.
 * A single operation may move at most MAX_CHUNK_BITS (24) bits; larger
 * requests are split recursively.
 */

const POW2 = new Float64Array(64);
for (let i = 0; i < POW2.length; i++) POW2[i] = 2 ** i;

export const MAX_CHUNK_BITS = 24;

export class BitWriter {
  private bytes: number[] = [];
  private acc = 0;
  private accLen = 0;

  /** Total number of bits written so far. */
  get bitsWritten(): number {
    return this.bytes.length * 8 + this.accLen;
  }

  /** Append the `count` least-significant bits of `value`, MSB first. */
  writeBits(value: number, count: number): void {
    if (count <= 0) return;
    if (count > MAX_CHUNK_BITS) {
      this.writeBits(Math.floor(value / POW2[MAX_CHUNK_BITS]), count - MAX_CHUNK_BITS);
      this.writeBits(value % POW2[MAX_CHUNK_BITS], MAX_CHUNK_BITS);
      return;
    }
    // Invariant: accLen < 8 hereafter, so acc stays below 2^31.
    this.acc = (this.acc << count) | value;
    this.accLen += count;
    while (this.accLen >= 8) {
      const shift = this.accLen - 8;
      this.bytes.push((this.acc >>> shift) & 0xff);
      this.acc &= (1 << shift) - 1;
      this.accLen = shift;
    }
  }

  /** Flush to a byte array, zero-padding the final partial byte. */
  toBytes(): Uint8Array {
    const out = this.bytes.slice();
    if (this.accLen > 0) {
      out.push((this.acc << (8 - this.accLen)) & 0xff);
    }
    return Uint8Array.from(out);
  }
}

export class BitReader {
  private bytePos = 0;
  private acc = 0;
  private accLen = 0;
  private readonly data: Uint8Array;

  constructor(data: Uint8Array) {
    this.data = data;
  }

  /** Bits remaining to read. */
  get remainingBits(): number {
    return (this.data.length - this.bytePos) * 8 + this.accLen;
  }

  private ensure(count: number): void {
    // Keeps acc below 2^31 while guaranteeing accLen >= count afterwards
    // (as long as the stream has enough bytes and count <= MAX_CHUNK_BITS).
    while (this.accLen < count && this.accLen <= MAX_CHUNK_BITS - 1 && this.bytePos < this.data.length) {
      this.acc = (this.acc << 8) | this.data[this.bytePos++];
      this.accLen += 8;
    }
  }

  /**
   * Return the next `count` bits (MSB-first) without consuming them.
   * Bits past the end of the stream read as zero.
   */
  peekBits(count: number): number {
    if (count > MAX_CHUNK_BITS) {
      throw new Error(`BitReader: cannot peek ${count} bits at once`);
    }
    this.ensure(count);
    if (this.accLen >= count) {
      return this.acc >>> (this.accLen - count);
    }
    return this.acc * POW2[count - this.accLen];
  }

  /** Drop `count` previously peeked bits. */
  consume(count: number): void {
    if (count > this.accLen) {
      throw new Error('BitReader: consume past end of stream');
    }
    this.accLen -= count;
    this.acc &= (1 << this.accLen) - 1;
  }

  /** Read `count` bits MSB-first into an unsigned integer. */
  readBits(count: number): number {
    if (this.remainingBits < count) {
      throw new Error('BitReader: unexpected end of stream');
    }
    const value = this.peekBits(count);
    this.consume(count);
    return value;
  }

  /** Read a single bit (0 or 1). */
  readBit(): number {
    return this.readBits(1);
  }
}
