// Shared constants, error types and little-endian integer helpers for the
// lzarc block format. See FORMAT.md for the full specification.

export const MAGIC0 = 0x4c; // 'L'
export const MAGIC1 = 0x5a; // 'Z'
export const VERSION = 1;
export const FIXED_HEADER_SIZE = 20;

export const MIN_WINDOW_BITS = 4;
export const MAX_WINDOW_BITS = 20;

export const MAX_CODE_BITS = 15;

export const MIN_MATCH = 3;
export const MAX_MATCH = 511; // length slot 8: 256 + 255

export const NUM_LITERALS = 256;
export const END_SYMBOL = 264;
export const NUM_MAIN_SYMBOLS = 265; // 256 literals + 8 length slots + END

export const DEFAULT_MAX_BLOCK_SIZE = 64 * 1024 * 1024;
export const MAX_PAYLOAD_SIZE = 1 << 28; // sanity cap on declared payload length

export type ErrorCode =
  | 'TRUNCATED'
  | 'CORRUPT'
  | 'OVERSUBSCRIBED'
  | 'BAD_DISTANCE'
  | 'OVERFLOW'
  | 'CHECKSUM'
  | 'DECLARED_TOO_LARGE'
  | 'BAD_FORMAT';

export class BlockError extends Error {
  code: ErrorCode;
  // Total size of the offending block in bytes when it could be determined
  // from the header, otherwise null. Used by the streaming decoder to skip a
  // broken block and resume at the next independent block.
  blockBytes: number | null;

  constructor(code: ErrorCode, message: string, blockBytes: number | null = null) {
    super(message);
    this.name = 'BlockError';
    this.code = code;
    this.blockBytes = blockBytes;
  }
}

export function readU16(buf: Uint8Array, off: number): number {
  return buf[off] | (buf[off + 1] << 8);
}

export function readU32(buf: Uint8Array, off: number): number {
  return (buf[off] | (buf[off + 1] << 8) | (buf[off + 2] << 16) | (buf[off + 3] << 24)) >>> 0;
}

export function writeU16(buf: Uint8Array, off: number, value: number): void {
  buf[off] = value & 0xff;
  buf[off + 1] = (value >>> 8) & 0xff;
}

export function writeU32(buf: Uint8Array, off: number, value: number): void {
  buf[off] = value & 0xff;
  buf[off + 1] = (value >>> 8) & 0xff;
  buf[off + 2] = (value >>> 16) & 0xff;
  buf[off + 3] = (value >>> 24) & 0xff;
}

export function concatBytes(parts: Uint8Array[]): Uint8Array {
  let total = 0;
  for (const p of parts) total += p.length;
  const out = new Uint8Array(total);
  let pos = 0;
  for (const p of parts) {
    out.set(p, pos);
    pos += p.length;
  }
  return out;
}
