/**
 * Varint encoding (LEB128-style, 7 bits per byte).
 * Implemented with arithmetic (not bitwise ops) so values up to 2^53
 * are safe, which lets us encode 64-bit file offsets as well as lengths.
 */

export function varintLength(value: number): number {
  let n = 1;
  let v = Math.floor(value);
  while (v >= 0x80) {
    v = Math.floor(v / 128);
    n++;
  }
  return n;
}

export function encodeVarint(value: number): Buffer {
  const out = Buffer.allocUnsafe(varintLength(value));
  let i = 0;
  let v = Math.floor(value);
  while (v >= 0x80) {
    out[i++] = (v % 128) | 0x80;
    v = Math.floor(v / 128);
  }
  out[i++] = v;
  return out;
}

/** Decodes a varint at `offset`; returns [value, offsetAfterVarint]. */
export function decodeVarint(buf: Buffer, offset: number): [number, number] {
  let result = 0;
  let shift = 1;
  let pos = offset;
  for (;;) {
    const byte = buf[pos++];
    result += (byte & 0x7f) * shift;
    if ((byte & 0x80) === 0) break;
    shift *= 128;
  }
  return [result, pos];
}
