import { ProtobufDecodeError } from './errors.ts';

/** varint 最大字节数（64 位整数 -> 10 字节）。 */
export const MAX_VARINT_BYTES = 10;

const MASK_64 = (1n << 64n) - 1n;

/**
 * 将无符号整数编码为 varint（7 位/字节，小端序组，MSB 为续位标志）。
 * 负数按 protobuf 语义视为 64 位无符号补码。
 */
export function encodeVarint(value: number | bigint): Uint8Array {
  let v = typeof value === 'bigint' ? value : BigInt(value);
  if (v < 0n) v &= MASK_64;
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
 * 从 buf[offset] 处解码 varint。
 * 截断（缓冲区耗尽）或超过 10 字节（溢出）时抛出 ProtobufDecodeError。
 */
export function decodeVarint(buf: Uint8Array, offset = 0): VarintResult {
  let result = 0n;
  let shift = 0n;
  for (let i = 0; i < MAX_VARINT_BYTES; i++) {
    const pos = offset + i;
    if (pos >= buf.length) {
      throw new ProtobufDecodeError('Truncated varint: buffer exhausted', pos);
    }
    const byte = buf[pos];
    result |= BigInt(byte & 0x7f) << shift;
    if ((byte & 0x80) === 0) {
      return { value: result & MASK_64, bytesRead: i + 1 };
    }
    shift += 7n;
  }
  throw new ProtobufDecodeError('Malformed varint: exceeds 10 bytes (64-bit overflow)', offset + MAX_VARINT_BYTES);
}

/** ZigZag 编码：有符号 -> 无符号（0->0, -1->1, 1->2, -2->3 ...）。 */
export function zigzagEncode32(value: number): number {
  return ((value << 1) ^ (value >> 31)) >>> 0;
}

export function zigzagDecode32(value: number): number {
  return (value >>> 1) ^ -(value & 1);
}

export function zigzagEncode64(value: bigint): bigint {
  return ((value << 1n) ^ (value >> 63n)) & MASK_64;
}

export function zigzagDecode64(value: bigint): bigint {
  return (value >> 1n) ^ -(value & 1n);
}
