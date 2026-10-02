/** Varint（7 位小端压缩）与 ZigZag（有符号映射）原语。零依赖，64 位精度走 BigInt。 */

import { MalformedError, TruncationError } from './errors.ts';

export const MAX_VARINT_BYTES = 10;
const MASK64 = (1n << 64n) - 1n;

/** 编码为 varint。负数按 protobuf 语义取低 64 位二进制补码。 */
export function encodeVarint(value: bigint | number): Uint8Array {
  let v = BigInt(value) & MASK64;
  const out = new Uint8Array(MAX_VARINT_BYTES);
  let i = 0;
  while (v > 0x7fn) {
    out[i++] = Number((v & 0x7fn) | 0x80n);
    v >>= 7n;
  }
  out[i++] = Number(v);
  return out.slice(0, i);
}

export interface VarintResult {
  value: bigint;
  bytesRead: number;
}

/**
 * 从 buf[offset] 起解码 varint，绝不越过 end。
 * 截断抛 TruncationError；超过 10 字节或第 10 字节溢出抛 MalformedError。
 */
export function decodeVarint(buf: Uint8Array, offset = 0, end = buf.length): VarintResult {
  let result = 0n;
  for (let i = 0; i < MAX_VARINT_BYTES; i++) {
    const idx = offset + i;
    if (idx >= end) {
      throw new TruncationError(
        'truncated varint: continuation bit set but buffer exhausted',
        offset,
        i + 1,
        Math.max(0, end - offset),
      );
    }
    const b = buf[idx];
    if (i === MAX_VARINT_BYTES - 1 && b > 1) {
      throw new MalformedError('varint overflows 64 bits', offset);
    }
    result |= BigInt(b & 0x7f) << BigInt(7 * i);
    if ((b & 0x80) === 0) {
      return { value: result, bytesRead: i + 1 };
    }
  }
  throw new MalformedError('varint exceeds 10 bytes', offset);
}

export function zigzagEncode32(n: number): number {
  if (!Number.isInteger(n) || n < -0x80000000 || n > 0x7fffffff) {
    throw new RangeError(`zigzag32 out of int32 range: ${n}`);
  }
  return ((n << 1) ^ (n >> 31)) >>> 0;
}

export function zigzagDecode32(n: number): number {
  return ((n >>> 1) ^ -(n & 1)) | 0;
}

export function zigzagEncode64(v: bigint): bigint {
  return ((v << 1n) ^ (v >> 63n)) & MASK64;
}

export function zigzagDecode64(v: bigint): bigint {
  const x = v & MASK64;
  return (x >> 1n) ^ (-(x & 1n));
}
