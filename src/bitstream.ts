/**
 * LSB-first bit stream.
 *
 * The encoder appends bits forward (low positions first); the decoder reads
 * the finished byte string backwards, so bits come out in strict LIFO order —
 * exactly what a finite-state entropy coder needs (last state flushed is the
 * first thing the decoder recovers).
 *
 * Stream layout: [ byte0, byte1, ..., byteN-1 ] where byteN-1 carries a
 * single "end mark" 1-bit above the final payload bits; bits above the mark
 * are zero padding.
 */

// Precomputed powers of two: `2 ** k` with a variable exponent is a
// surprisingly expensive float pow on the hot path; a table lookup is ~5x.
const POW2 = new Float64Array(64);
for (let i = 0; i < POW2.length; i++) POW2[i] = 2 ** i;

export class BitWriter {
  bytes: number[] = [];
  container = 0; // pending bits, value < 2^bitPos
  bitPos = 0;

  /** Append the low `nbBits` bits of `value` (0 <= nbBits <= 24). */
  writeBits(value: number, nbBits: number): void {
    this.container += value * POW2[this.bitPos];
    this.bitPos += nbBits;
    while (this.bitPos >= 8) {
      this.bytes.push(this.container % 256);
      this.container = Math.floor(this.container / 256);
      this.bitPos -= 8;
    }
  }

  /** Append the end mark and flush. Returns the finished byte string. */
  finish(): Uint8Array {
    this.writeBits(1, 1); // end mark: lets the reader find the exact bit end
    if (this.bitPos > 0) {
      this.bytes.push(this.container);
      this.container = 0;
      this.bitPos = 0;
    }
    return Uint8Array.from(this.bytes);
  }
}

export class ReverseBitReader {
  bytes: Uint8Array;
  bytePos: number;
  container = 0; // unread bits; newest bits sit at the top
  bitPos = 0;

  constructor(bytes: Uint8Array) {
    this.bytes = bytes;
    if (bytes.length === 0) throw new Error("empty bitstream");
    const last = bytes[bytes.length - 1];
    if (last === 0) throw new Error("corrupt bitstream: missing end mark");
    const hb = 31 - Math.clz32(last); // position of the end mark
    this.container = last & ((1 << hb) - 1); // payload bits below the mark
    this.bitPos = hb;
    this.bytePos = bytes.length - 1;
  }

  /** Consume and return the `nbBits` most recently written bits (LIFO). */
  readBits(nbBits: number): number {
    while (this.bitPos < nbBits) {
      if (this.bytePos === 0) throw new Error("bitstream underflow");
      this.bytePos -= 1;
      this.container = this.container * 256 + this.bytes[this.bytePos];
      this.bitPos += 8;
    }
    this.bitPos -= nbBits;
    const scale = POW2[this.bitPos];
    const value = Math.floor(this.container / scale);
    this.container -= value * scale;
    return value;
  }
}
