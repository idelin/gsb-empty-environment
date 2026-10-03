import { DecodeError } from "./errors.ts";

export const MAX_VARINT_BYTES = 10; // ceil(64 / 7)
export const MASK64 = 0xffff_ffff_ffff_ffffn;
export const MASK32 = 0xffff_ffffn;

/**
 * Encode an unsigned 64-bit value as a varint (7 bits per byte, LSB first,
 * continuation bit in the high bit). Negative inputs are interpreted as their
 * two's-complement 64-bit representation (matching protobuf int64 semantics).
 */
export function encodeVarint(value: bigint | number): Uint8Array {
  let v = BigInt(value) & MASK64;
  const out = new Uint8Array(MAX_VARINT_BYTES);
  let i = 0;
  while (v > 0x7fn) {
    out[i++] = Number(v & 0x7fn) | 0x80;
    v >>= 7n;
  }
  out[i++] = Number(v);
  return out.subarray(0, i);
}

export interface VarintResult {
  value: bigint;
  bytesRead: number;
}

/**
 * Decode a varint from `bytes` starting at `offset`.
 * Throws DecodeError on truncation or on >64-bit overflow.
 */
export function decodeVarint(bytes: Uint8Array, offset: number, limit = bytes.length): VarintResult {
  let result = 0n;
  let shift = 0n;
  for (let i = 0; i < MAX_VARINT_BYTES; i++) {
    const pos = offset + i;
    if (pos >= limit) {
      throw new DecodeError("truncated varint: buffer ended mid-sequence", pos);
    }
    const b = bytes[pos];
    if (i === MAX_VARINT_BYTES - 1 && b > 1) {
      throw new DecodeError("varint overflow: exceeds 64 bits", pos);
    }
    result |= BigInt(b & 0x7f) << shift;
    if ((b & 0x80) === 0) {
      return { value: result, bytesRead: i + 1 };
    }
    shift += 7n;
  }
  // Unreachable (loop returns or throws), kept for clarity.
  throw new DecodeError("varint overflow: exceeds 64 bits", offset + MAX_VARINT_BYTES);
}

/** ZigZag: map signed integers to unsigned so small magnitudes stay small. */
export function zigzagEncode(value: bigint | number): bigint {
  const v = BigInt(value);
  return ((v << 1n) ^ (v >> 63n)) & MASK64;
}

export function zigzagDecode(value: bigint | number): bigint {
  const v = BigInt(value) & MASK64;
  return (v >> 1n) ^ (-(v & 1n));
}

/** Reinterpret an unsigned 64-bit value as signed two's complement. */
export function toSigned64(value: bigint): bigint {
  const v = value & MASK64;
  return v >= 1n << 63n ? v - (1n << 64n) : v;
}

export function toSigned32(value: number): number {
  return value | 0;
}
