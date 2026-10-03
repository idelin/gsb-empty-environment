/**
 * Varint (7-bit compressed) and ZigZag codecs.
 * Pure TypeScript, zero dependencies, BigInt-based for full 64-bit safety.
 */
import { ProtobufDecodeError, TruncatedMessageError } from './errors.ts';

/** A varint never exceeds 10 bytes (64 bits / 7 bits per byte, rounded up). */
export const MAX_VARINT_BYTES = 10;

const MASK_64 = (1n << 64n) - 1n;
const SEVEN_BITS = 0x7fn;

/** Number of bytes `value` (unsigned) occupies in varint form. */
export function varintLength(value: bigint): number {
  if (value < 0n) {
    // Negative int64 values are sign-extended to 64 bits on the wire.
    value &= MASK_64;
  }
  let length = 1;
  while (value >= 0x80n) {
    value >>= 7n;
    length += 1;
  }
  return length;
}

/** Encode an unsigned 64-bit value as varint. Negative inputs are masked to 64 bits. */
export function encodeVarint(value: bigint): Uint8Array {
  value &= MASK_64;
  const out = new Uint8Array(varintLength(value));
  let i = 0;
  while (value >= 0x80n) {
    out[i] = Number(value & SEVEN_BITS) | 0x80;
    value >>= 7n;
    i += 1;
  }
  out[i] = Number(value);
  return out;
}

export interface VarintResult {
  value: bigint;
  bytesRead: number;
}

/**
 * Decode a varint starting at `offset`.
 * Throws TruncatedMessageError if the buffer ends mid-varint (safe bounds guard),
 * and ProtobufDecodeError if the varint exceeds 10 bytes (overflow guard).
 */
export function decodeVarint(buf: Uint8Array, offset = 0): VarintResult {
  let result = 0n;
  let shift = 0n;
  for (let i = 0; i < MAX_VARINT_BYTES; i += 1) {
    const pos = offset + i;
    if (pos >= buf.length) {
      throw new TruncatedMessageError('varint', i + 1, buf.length - offset);
    }
    const byte = buf[pos];
    if (i === MAX_VARINT_BYTES - 1 && byte > 1) {
      // 10th byte may only carry the single top bit of a 64-bit value.
      throw new ProtobufDecodeError('varint overflows 64 bits');
    }
    result |= BigInt(byte & 0x7f) << shift;
    if ((byte & 0x80) === 0) {
      return { value: result, bytesRead: i + 1 };
    }
    shift += 7n;
  }
  throw new ProtobufDecodeError('varint exceeds 10 bytes');
}

/** Reinterpret an unsigned 64-bit varint result as a signed two's-complement int64. */
export function asSigned64(value: bigint): bigint {
  value &= MASK_64;
  return value >= 1n << 63n ? value - (1n << 64n) : value;
}

/** ZigZag: map signed integers to unsigned so small magnitudes stay small. */
export function zigZagEncode32(value: number): number {
  return ((value << 1) ^ (value >> 31)) >>> 0;
}

export function zigZagDecode32(value: number): number {
  return (value >>> 1) ^ -(value & 1);
}

export function zigZagEncode64(value: bigint): bigint {
  return ((value << 1n) ^ (value >> 63n)) & MASK_64;
}

export function zigZagDecode64(value: bigint): bigint {
  value &= MASK_64;
  return (value >> 1n) ^ -(value & 1n);
}
