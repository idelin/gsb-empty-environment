import { MalformedError, TruncationError } from "./errors.ts";

/** Maximum bytes a 64-bit varint may occupy (10 * 7 = 70 bits, top 4 unused). */
export const MAX_VARINT_BYTES = 10;
const MASK_64 = (1n << 64n) - 1n;

/** Number of bytes `value` (unsigned 64-bit) occupies as a varint. */
export function varintLength(value: bigint | number): number {
  let v = BigInt(value) & MASK_64;
  let n = 1;
  while (v >= 0x80n) {
    v >>= 7n;
    n++;
  }
  return n;
}

/** Encode an unsigned 64-bit integer as a varint (7 bits per byte, LSB first). */
export function encodeVarint(value: bigint | number): Uint8Array {
  let v = BigInt(value) & MASK_64;
  const out = new Uint8Array(varintLength(v));
  let i = 0;
  while (v >= 0x80n) {
    out[i++] = Number(v & 0x7fn) | 0x80;
    v >>= 7n;
  }
  out[i] = Number(v);
  return out;
}

export interface VarintResult {
  /** Unsigned 64-bit value. */
  value: bigint;
  /** Bytes consumed from the buffer. */
  bytesRead: number;
}

/**
 * Decode a varint from `buf` starting at `offset`.
 * Bounds-safe: throws {@link TruncationError} when the buffer ends mid-varint
 * and {@link MalformedError} when the encoding exceeds 64 bits.
 */
export function decodeVarint(
  buf: Uint8Array,
  offset = 0,
  limit = buf.length,
): VarintResult {
  let result = 0n;
  let shift = 0n;
  let pos = offset;
  for (;;) {
    if (pos >= limit) {
      throw new TruncationError("varint", 1, 0, pos);
    }
    const byte = buf[pos]!;
    pos++;
    if (shift === 63n) {
      // 10th byte: only 1 payload bit is legal for a 64-bit value.
      if (byte > 0x01) {
        throw new MalformedError(
          `varint overflows 64 bits (10th byte = 0x${byte.toString(16)})`,
          offset,
        );
      }
      result |= BigInt(byte) << shift;
      return { value: result, bytesRead: pos - offset };
    }
    result |= BigInt(byte & 0x7f) << shift;
    if ((byte & 0x80) === 0) {
      return { value: result, bytesRead: pos - offset };
    }
    shift += 7n;
  }
}

/** ZigZag: map signed integers to unsigned so small magnitudes stay small. */
export function zigzagEncode(value: bigint | number): bigint {
  const v = BigInt(value);
  return ((v << 1n) ^ (v >> 63n)) & MASK_64;
}

export function zigzagDecode(value: bigint | number): bigint {
  const v = BigInt(value) & MASK_64;
  return (v >> 1n) ^ -(v & 1n);
}

/** Signed 64-bit -> two's-complement unsigned 64-bit (for int64/enum). */
export function toUnsigned64(value: bigint | number): bigint {
  return BigInt(value) & MASK_64;
}

/** Unsigned 64-bit -> signed 64-bit two's-complement interpretation. */
export function toSigned64(value: bigint | number): bigint {
  const v = BigInt(value) & MASK_64;
  return v >= 1n << 63n ? v - (1n << 64n) : v;
}
