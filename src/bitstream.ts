/** Bit-level utilities: buffers are expanded to Uint8Array of 0/1 for easy corruption & scanning. */

export function bufferToBits(buf: Buffer): Uint8Array {
  const bits = new Uint8Array(buf.length * 8);
  for (let i = 0; i < buf.length; i++) {
    for (let j = 0; j < 8; j++) {
      bits[i * 8 + j] = (buf[i] >>> (7 - j)) & 1;
    }
  }
  return bits;
}

export function bitsToBuffer(bits: Uint8Array): Buffer {
  const out = Buffer.alloc(Math.ceil(bits.length / 8));
  for (let i = 0; i < bits.length; i++) {
    if (bits[i]) out[i >>> 3] |= 1 << (7 - (i & 7));
  }
  return out;
}

/** Delete `count` bits starting at bit `offset` (simulates bit loss on the channel). */
export function deleteBits(bits: Uint8Array, offset: number, count: number): Uint8Array {
  const out = new Uint8Array(bits.length - count);
  out.set(bits.subarray(0, offset), 0);
  out.set(bits.subarray(offset + count), offset);
  return out;
}

/** Flip a single bit (simulates a bit error on the channel). */
export function flipBit(bits: Uint8Array, offset: number): Uint8Array {
  const out = bits.slice();
  out[offset] ^= 1;
  return out;
}
