// Shared binary format primitives for the compact segment index.
// See FORMAT.md for the full on-disk specification.

export const MAGIC = 0x43494458; // "CIDX"
export const VERSION = 1;
export const HEADER_SIZE = 28;
export const BLOCK_MAGIC = 0x424c4b30; // "BLK0"
export const BLOCK_HEADER_SIZE = 24;
export const DIGEST_SIZE = 4;
export const MAX_DOC_ID = 0xffffffff;
export const DEFAULT_BLOCK_SIZE = 128;
export const MAX_TERM_BYTES = 0xffff;

export class FormatError extends Error {}

// Unsigned LEB128, at most 5 bytes, value must fit in u32.
export function encodeVarint(value: number): Buffer {
  if (!Number.isInteger(value) || value < 0 || value > MAX_DOC_ID) {
    throw new RangeError(`varint value out of u32 range: ${value}`);
  }
  const bytes: number[] = [];
  let v = value;
  do {
    let b = v % 128;
    v = Math.floor(v / 128);
    if (v > 0) b |= 0x80;
    bytes.push(b);
  } while (v > 0);
  return Buffer.from(bytes);
}

// Decodes a varint from buf within [pos, end). Throws FormatError on
// truncation or when the value would exceed u32 (integer overflow).
export function decodeVarint(buf: Buffer, pos: number, end: number): { value: number; next: number } {
  let value = 0;
  let shift = 0;
  let p = pos;
  for (;;) {
    if (p >= end) throw new FormatError(`truncated varint at offset ${p}`);
    const b = buf[p];
    if (shift === 28 && b > 0x0f) {
      throw new FormatError(`varint overflows u32 at offset ${pos}`);
    }
    value += (b & 0x7f) * 2 ** shift;
    p++;
    if ((b & 0x80) === 0) break;
    shift += 7;
    if (shift > 28) throw new FormatError(`varint overflows u32 at offset ${pos}`);
  }
  if (value > MAX_DOC_ID) throw new FormatError(`varint overflows u32 at offset ${pos}`);
  return { value, next: p };
}

const CRC_TABLE = new Uint32Array(256);
for (let n = 0; n < 256; n++) {
  let c = n;
  for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
  CRC_TABLE[n] = c >>> 0;
}

// CRC-32 (IEEE 802.3) over buf[start, end).
export function crc32(buf: Buffer, start = 0, end = buf.length): number {
  let c = 0xffffffff;
  for (let i = start; i < end; i++) {
    c = CRC_TABLE[(c ^ buf[i]) & 0xff] ^ (c >>> 8);
  }
  return (c ^ 0xffffffff) >>> 0;
}

// FNV-1a 32-bit over the UTF-8 bytes of a term. Binds a posting block
// header to its dictionary entry.
export function termHash(term: string): number {
  const bytes = Buffer.from(term, "utf8");
  let h = 0x811c9dc5;
  for (const b of bytes) {
    h ^= b;
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return h >>> 0;
}

// Bytewise comparison of UTF-8 encodings; defines dictionary order.
export function compareTerms(a: string, b: string): number {
  return Buffer.compare(Buffer.from(a, "utf8"), Buffer.from(b, "utf8"));
}
