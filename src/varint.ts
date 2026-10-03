import { ProtoError } from './errors.ts';

const MASK_64 = (1n << 64n) - 1n;

/** 将无符号 64 位整数编码为 varint（每字节 7 位有效载荷，LSB 优先）。 */
export function encodeVarint(value: bigint | number): Uint8Array {
  let v = BigInt(value) & MASK_64;
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
  bytesRead: number;
}

/**
 * 从 buf[offset] 解码 varint。
 * 安全防护：
 *  - 缓冲区耗尽 -> TRUNCATED（绝不越界读取）
 *  - 超过 10 字节 / 第 10 字节载荷 >1 位 -> OVERFLOW
 */
export function decodeVarint(buf: Uint8Array, offset: number, end = buf.length): VarintResult {
  let result = 0n;
  let shift = 0n;
  for (let i = 0; i < 10; i++) {
    const pos = offset + i;
    if (pos >= end) {
      throw new ProtoError('TRUNCATED', `varint 在第 ${i} 字节处被截断`, offset);
    }
    const byte = buf[pos]!;
    if (i === 9 && byte > 0x01) {
      throw new ProtoError('OVERFLOW', 'varint 超过 64 位', offset);
    }
    result |= BigInt(byte & 0x7f) << shift;
    if ((byte & 0x80) === 0) {
      return { value: result & MASK_64, bytesRead: i + 1 };
    }
    shift += 7n;
  }
  throw new ProtoError('OVERFLOW', 'varint 超过 10 字节', offset);
}

/** varint 编码后的字节长度。 */
export function varintLength(value: bigint | number): number {
  let v = BigInt(value) & MASK_64;
  let n = 1;
  while (v >= 0x80n) {
    v >>= 7n;
    n++;
  }
  return n;
}

/** ZigZag：有符号 -> 无符号。(n << 1) ^ (n >> 63) */
export function zigzagEncode(value: bigint | number): bigint {
  const v = BigInt(value);
  return ((v << 1n) ^ (v >> 63n)) & MASK_64;
}

/** ZigZag 逆变换。 */
export function zigzagDecode(value: bigint | number): bigint {
  const v = BigInt(value) & MASK_64;
  return (v >> 1n) ^ -(v & 1n);
}

/** 将有符号数截断为 64 位补码对应的无符号表示（int64 编码用）。 */
export function toUnsigned64(value: bigint | number): bigint {
  return BigInt(value) & MASK_64;
}

/** 将无符号 64 位重新解释为有符号（int64 解码用）。 */
export function toSigned64(value: bigint): bigint {
  const v = value & MASK_64;
  return v >= 1n << 63n ? v - (1n << 64n) : v;
}
