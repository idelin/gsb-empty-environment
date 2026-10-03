import { ProtobufDecodeError } from './errors.ts';

export const MAX_VARINT_BYTES = 10;
export const UINT64_MASK = 0xffff_ffff_ffff_ffffn;

export function varintLength(value: number | bigint): number {
  let v = BigInt.asUintN(64, typeof value === 'number' ? BigInt(Math.trunc(value)) : value);
  let len = 1;
  while (v >= 0x80n) {
    v >>= 7n;
    len += 1;
  }
  return len;
}

export function encodeVarintInto(out: Uint8Array, offset: number, value: number | bigint): number {
  let v = BigInt.asUintN(64, typeof value === 'number' ? BigInt(Math.trunc(value)) : value);
  let pos = offset;
  while (v > 0x7fn) {
    out[pos] = Number(v & 0x7fn) | 0x80;
    pos += 1;
    v >>= 7n;
  }
  out[pos] = Number(v);
  return pos + 1 - offset;
}

export function encodeVarint(value: number | bigint): Uint8Array {
  const out = new Uint8Array(MAX_VARINT_BYTES);
  const written = encodeVarintInto(out, 0, value);
  return out.slice(0, written);
}

export interface VarintResult {
  value: bigint;
  offset: number;
}

export function decodeVarint(buf: Uint8Array, offset: number, limit: number): VarintResult {
  let result = 0n;
  let shift = 0n;
  let pos = offset;
  for (let i = 0; i < MAX_VARINT_BYTES; i += 1) {
    if (pos >= limit) {
      throw new ProtobufDecodeError('TRUNCATED', 'varint runs past end of buffer', pos);
    }
    const byte = buf[pos];
    pos += 1;
    if (i === MAX_VARINT_BYTES - 1 && byte > 1) {
      throw new ProtobufDecodeError(
        'VARINT_OVERFLOW',
        'varint exceeds 64 bits (10th byte must be 0 or 1)',
        offset,
      );
    }
    result |= BigInt(byte & 0x7f) << shift;
    if ((byte & 0x80) === 0) {
      return { value: result, offset: pos };
    }
    shift += 7n;
  }
  throw new ProtobufDecodeError('VARINT_OVERFLOW', 'varint longer than 10 bytes', offset);
}

export function zigzagEncode32(n: number): number {
  return ((n << 1) ^ (n >> 31)) >>> 0;
}

export function zigzagDecode32(u: number): number {
  return ((u >>> 1) ^ -(u & 1)) | 0;
}

export function zigzagEncode64(v: bigint): bigint {
  return BigInt.asUintN(64, (v << 1n) ^ (v >> 63n));
}

export function zigzagDecode64(u: bigint): bigint {
  return BigInt.asIntN(64, (u >> 1n) ^ (-(u & 1n)));
}
