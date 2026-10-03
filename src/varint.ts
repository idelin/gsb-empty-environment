/**
 * Varint (7 位压缩) 与 ZigZag 有符号转换原语。
 * 64 位值统一用 bigint 运算, 避免 Number 精度丢失。
 */

import { MalformedVarintError, TruncatedFieldError } from './errors.ts';

const MASK_64 = (1n << 64n) - 1n;
const MAX_VARINT_BYTES = 10; // ceil(64 / 7)

/** 无符号 64 位 varint 编码。 */
export function encodeVarint(value: bigint): Uint8Array {
  let v = value & MASK_64;
  const out: number[] = [];
  do {
    let byte = Number(v & 0x7fn);
    v >>= 7n;
    if (v !== 0n) byte |= 0x80;
    out.push(byte);
  } while (v !== 0n);
  return Uint8Array.from(out);
}

export interface VarintResult {
  value: bigint;
  /** 消费的字节数 (1..10)。 */
  bytesRead: number;
}

/**
 * 从 buf[offset] 解码 varint。
 * 越界防护: 缓冲区耗尽抛 TruncatedFieldError; 超过 10 字节未终止抛
 * MalformedVarintError, 绝不越界读取。
 */
export function decodeVarint(
  buf: Uint8Array,
  offset: number,
): VarintResult {
  let result = 0n;
  let shift = 0n;
  for (let i = 0; i < MAX_VARINT_BYTES; i++) {
    const pos = offset + i;
    if (pos >= buf.length) {
      throw new TruncatedFieldError(i + 1, buf.length - offset, offset, 'varint');
    }
    const byte = buf[pos];
    result |= BigInt(byte & 0x7f) << shift;
    if ((byte & 0x80) === 0) {
      return { value: result & MASK_64, bytesRead: i + 1 };
    }
    shift += 7n;
  }
  throw new MalformedVarintError(offset);
}

/** ZigZag: 有符号 -> 无符号。 (n << 1) ^ (n >> 63) */
export function zigzagEncode(value: bigint): bigint {
  return ((value << 1n) ^ (value >> 63n)) & MASK_64;
}

/** ZigZag: 无符号 -> 有符号。 */
export function zigzagDecode(value: bigint): bigint {
  return (value >> 1n) ^ -(value & 1n);
}

/** 有符号 64 位 -> 无符号 64 位(补码)。 */
export function int64ToUint64(value: bigint): bigint {
  return value & MASK_64;
}

/** 无符号 64 位 -> 有符号 64 位(补码解释)。 */
export function uint64ToInt64(value: bigint): bigint {
  const v = value & MASK_64;
  return v >= 1n << 63n ? v - (1n << 64n) : v;
}

/** 无符号 64 位 varint 编码后长度。 */
export function varintLength(value: bigint): number {
  let v = value & MASK_64;
  let n = 1;
  while (v >= 0x80n) {
    v >>= 7n;
    n++;
  }
  return n;
}
