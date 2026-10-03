/** Varint64 encoding (LevelDB-style, little-endian base-128). */

export function varintLength(value: number): number {
  let len = 1;
  let v = value;
  while (v >= 128) {
    v = Math.floor(v / 128);
    len++;
  }
  return len;
}

export function encodeVarint64(value: number): Buffer {
  const out = Buffer.allocUnsafe(varintLength(value));
  let i = 0;
  let v = value;
  while (v >= 128) {
    out[i++] = (v & 0x7f) | 0x80;
    v = Math.floor(v / 128);
  }
  out[i] = v;
  return out;
}

export function decodeVarint64(
  buf: Buffer,
  offset: number,
): { value: number; bytesRead: number } {
  let result = 0;
  let shift = 0;
  let pos = offset;
  for (;;) {
    const byte = buf[pos++];
    result += (byte & 0x7f) * 2 ** shift;
    if ((byte & 0x80) === 0) break;
    shift += 7;
    if (shift > 63) throw new Error('varint too long');
  }
  return { value: result, bytesRead: pos - offset };
}
