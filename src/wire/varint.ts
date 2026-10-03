import { MalformedVarintError, TruncatedFieldError } from './errors.ts';

export const UINT32_MASK = 0xffffffffn;
export const UINT64_MASK = 0xffffffffffffffffn;
export const MAX_VARINT_BYTES = 10;

export function encodeVarint(value: bigint): Uint8Array {
  let v = value & UINT64_MASK;
  const out = new Uint8Array(MAX_VARINT_BYTES);
  let i = 0;
  while (v > 0x7fn) {
    out[i++] = Number((v & 0x7fn) | 0x80n);
    v >>= 7n;
  }
  out[i++] = Number(v);
  return out.subarray(0, i);
}

export interface VarintResult {
  value: bigint;
  bytesRead: number;
}

export function decodeVarint(
  buf: Uint8Array,
  offset: number,
  end: number = buf.length,
): VarintResult {
  let result = 0n;
  for (let i = 0; i < MAX_VARINT_BYTES; i++) {
    const pos = offset + i;
    if (pos >= end) {
      throw new TruncatedFieldError('varint', offset, i + 1, end - offset);
    }
    const byte = buf[pos];
    if (i === MAX_VARINT_BYTES - 1 && byte > 1) {
      throw new MalformedVarintError(
        `varint at offset ${offset} overflows 64 bits (10th byte = 0x${byte.toString(16)})`,
      );
    }
    result |= BigInt(byte & 0x7f) << BigInt(7 * i);
    if ((byte & 0x80) === 0) {
      return { value: result, bytesRead: i + 1 };
    }
  }
  throw new MalformedVarintError(`varint at offset ${offset} exceeds ${MAX_VARINT_BYTES} bytes`);
}

export function zigzagEncode32(n: number): number {
  return ((n << 1) ^ (n >> 31)) >>> 0;
}

export function zigzagDecode32(u: number): number {
  return (u >>> 1) ^ -(u & 1);
}

export function zigzagEncode64(n: bigint): bigint {
  return ((n << 1n) ^ (n >> 63n)) & UINT64_MASK;
}

export function zigzagDecode64(u: bigint): bigint {
  const x = u & UINT64_MASK;
  return (x >> 1n) ^ -(x & 1n);
}
