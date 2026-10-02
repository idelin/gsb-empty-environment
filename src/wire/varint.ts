import { WireError } from './errors.ts';

export const MAX_VARINT_BYTES = 10;
const MASK_64 = (1n << 64n) - 1n;

export function encodeVarint(value: bigint | number): Uint8Array {
  let v = typeof value === 'number' ? BigInt(value) : value;
  v &= MASK_64;
  const out = new Uint8Array(MAX_VARINT_BYTES);
  let i = 0;
  while (v > 0x7fn) {
    out[i++] = Number(v & 0x7fn) | 0x80;
    v >>= 7n;
  }
  out[i++] = Number(v);
  return out.subarray(0, i);
}

export function varintLength(value: bigint | number): number {
  let v = (typeof value === 'number' ? BigInt(value) : value) & MASK_64;
  let n = 1;
  while (v > 0x7fn) { v >>= 7n; n++; }
  return n;
}

export interface VarintResult {
  value: bigint;
  bytesRead: number;
}

export function decodeVarint(buf: Uint8Array, offset = 0): VarintResult {
  let result = 0n;
  let shift = 0n;
  for (let i = 0; i < MAX_VARINT_BYTES; i++) {
    const pos = offset + i;
    if (pos >= buf.length) {
      throw new WireError('TRUNCATED', `varint truncated after ${i} byte(s)`, offset);
    }
    const b = buf[pos];
    if (i === MAX_VARINT_BYTES - 1) {
      if (b > 0x01) {
        throw new WireError('VARINT_OVERFLOW', `varint exceeds 64 bits (10th byte=0x${b.toString(16)})`, pos);
      }
      result |= BigInt(b) << shift;
      return { value: result & MASK_64, bytesRead: i + 1 };
    }
    result |= BigInt(b & 0x7f) << shift;
    if ((b & 0x80) === 0) {
      return { value: result, bytesRead: i + 1 };
    }
    shift += 7n;
  }
  throw new WireError('MALFORMED_VARINT', 'unreachable varint state', offset);
}

export function zigzagEncode64(value: bigint): bigint {
  return ((value << 1n) ^ (value >> 63n)) & MASK_64;
}

export function zigzagDecode64(value: bigint): bigint {
  return (value >> 1n) ^ (-(value & 1n));
}

export function zigzagEncode32(value: number): number {
  return ((value << 1) ^ (value >> 31)) >>> 0;
}

export function zigzagDecode32(value: number): number {
  return (value >>> 1) ^ -(value & 1);
}

export function toSigned64(value: bigint): bigint {
  const v = value & MASK_64;
  return v >= 1n << 63n ? v - (1n << 64n) : v;
}
