/**
 * Low-level Protocol Buffers wire-format primitives.
 * Zero-dependency, hand-rolled: varint (7-bit groups), ZigZag, fixed32/64.
 */

export const WireType = {
  Varint: 0,
  I64: 1,
  Len: 2,
  SGroup: 3,
  EGroup: 4,
  I32: 5,
} as const;
export type WireTypeValue = (typeof WireType)[keyof typeof WireType];

export class WireError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'WireError';
  }
}

/** Raised when a read would cross the buffer boundary (safe out-of-bounds guard). */
export class TruncationError extends WireError {
  readonly offset: number;
  readonly needed: number;
  readonly available: number;
  constructor(message: string, offset: number, needed: number, available: number) {
    super(message);
    this.name = 'TruncationError';
    this.offset = offset;
    this.needed = needed;
    this.available = available;
  }
}

export const MAX_VARINT_BYTES = 10;
export const MASK_64 = (1n << 64n) - 1n;
export const MAX_UINT32 = 0xffffffffn;
export const MAX_LENGTH_PREFIX = 0x7fffffffn;

/** Encode a non-negative bigint / number as a base-128 varint. Negative values wrap to uint64 two's complement. */
export function encodeVarint(value: number | bigint): Uint8Array {
  let v = typeof value === 'bigint' ? value : BigInt(Math.trunc(value));
  if (v < 0n) v &= MASK_64;
  const out: number[] = [];
  do {
    let byte = Number(v & 0x7fn);
    v >>= 7n;
    if (v !== 0n) byte |= 0x80;
    out.push(byte);
  } while (v !== 0n);
  return Uint8Array.from(out);
}

/** Decode a varint from `buf[offset..limit)`. Throws TruncationError / WireError(overflow). */
export function decodeVarint(
  buf: Uint8Array,
  offset: number,
  limit: number,
): { value: bigint; next: number } {
  let result = 0n;
  let shift = 0n;
  let pos = offset;
  while (pos < limit) {
    const byte = buf[pos];
    if (shift === 63n) {
      // 10th byte carries a single payload bit for a 64-bit varint.
      if (byte > 1) {
        throw new WireError(`varint overflow: 10th byte 0x${byte.toString(16)} at offset ${offset}`);
      }
      result |= BigInt(byte) << shift;
      pos += 1;
      return { value: result & MASK_64, next: pos };
    }
    result |= BigInt(byte & 0x7f) << shift;
    pos += 1;
    if ((byte & 0x80) === 0) return { value: result, next: pos };
    shift += 7n;
  }
  throw new TruncationError(
    `truncated varint at offset ${offset} (buffer ends at ${limit})`,
    offset,
    1,
    Math.max(0, limit - offset),
  );
}

/** ZigZag: map signed ints to unsigned so small magnitudes stay small. */
export function zigzagEncode(value: number | bigint): bigint {
  const v = typeof value === 'bigint' ? value : BigInt(Math.trunc(value));
  return ((v << 1n) ^ (v >> 63n)) & MASK_64;
}

export function zigzagDecode(u: bigint): bigint {
  return (u >> 1n) ^ (-(u & 1n));
}

export function encodeFixed32(value: number): Uint8Array {
  const v = value >>> 0;
  return Uint8Array.of(v & 0xff, (v >>> 8) & 0xff, (v >>> 16) & 0xff, (v >>> 24) & 0xff);
}

export function decodeFixed32(
  buf: Uint8Array,
  offset: number,
  limit: number,
): { value: number; next: number } {
  if (offset + 4 > limit) {
    throw new TruncationError(`truncated fixed32 at offset ${offset}`, offset, 4, Math.max(0, limit - offset));
  }
  const value =
    (buf[offset] | (buf[offset + 1] << 8) | (buf[offset + 2] << 16) | (buf[offset + 3] << 24)) >>> 0;
  return { value, next: offset + 4 };
}

export function encodeFixed64(value: number | bigint): Uint8Array {
  let v = typeof value === 'bigint' ? value : BigInt(Math.trunc(value));
  v &= MASK_64;
  const out = new Uint8Array(8);
  for (let i = 0; i < 8; i += 1) {
    out[i] = Number(v & 0xffn);
    v >>= 8n;
  }
  return out;
}

export function decodeFixed64(
  buf: Uint8Array,
  offset: number,
  limit: number,
): { value: bigint; next: number } {
  if (offset + 8 > limit) {
    throw new TruncationError(`truncated fixed64 at offset ${offset}`, offset, 8, Math.max(0, limit - offset));
  }
  let value = 0n;
  for (let i = 7; i >= 0; i -= 1) {
    value = (value << 8n) | BigInt(buf[offset + i]);
  }
  return { value, next: offset + 8 };
}
