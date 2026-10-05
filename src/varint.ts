// Unsigned LEB128 varint for 32-bit values (max 5 bytes).

export function uvarintSize(value: number): number {
  let v = value >>> 0;
  let n = 1;
  while (v >= 0x80) {
    v = Math.floor(v / 128);
    n++;
  }
  return n;
}

export function writeUvarint(buf: Buffer, offset: number, value: number): number {
  let v = value >>> 0;
  // 0xffffffff >>> 0 === 0xffffffff, safe for the full u32 range.
  while (v >= 0x80) {
    buf.writeUInt8((v % 128) | 0x80, offset);
    offset++;
    v = Math.floor(v / 128);
  }
  buf.writeUInt8(v, offset);
  return offset + 1;
}

export type UvarintResult =
  | { ok: true; value: number; next: number }
  | { ok: false; error: 'truncated' | 'overflow' };

export function readUvarint(buf: Buffer, offset: number, end: number): UvarintResult {
  let result = 0;
  let shift = 0;
  for (let i = 0; i < 5; i++) {
    if (offset + i >= end) return { ok: false, error: 'truncated' };
    const byte = buf[offset + i];
    if (i === 4 && byte > 0x0f) return { ok: false, error: 'overflow' };
    // Multiply instead of bit-shifting: shifts would sign-extend at bit 31.
    result += (byte & 0x7f) * 2 ** shift;
    if (byte < 0x80) return { ok: true, value: result, next: offset + i + 1 };
    shift += 7;
  }
  return { ok: false, error: 'overflow' };
}
