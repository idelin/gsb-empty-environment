export class ProtobufDecodeError extends Error {
  readonly offset: number;
  constructor(message: string, offset: number) {
    super(`${message} (offset=${offset})`);
    this.name = 'ProtobufDecodeError';
    this.offset = offset;
  }
}

export const MAX_VARINT_BYTES = 10;

const MASK_64 = (1n << 64n) - 1n;

export function varintByteLength(value: bigint): number {
  let v = value & MASK_64;
  let n = 1;
  while (v >= 0x80n) {
    v >>= 7n;
    n++;
  }
  return n;
}

export function encodeVarintInto(
  out: Uint8Array,
  offset: number,
  value: bigint,
): number {
  let v = value & MASK_64;
  let i = offset;
  while (v >= 0x80n) {
    out[i++] = Number(v & 0x7fn) | 0x80;
    v >>= 7n;
  }
  out[i++] = Number(v);
  return i - offset;
}

export function encodeVarint(value: bigint | number): Uint8Array {
  const v = typeof value === 'number' ? BigInt(value) : value;
  const out = new Uint8Array(varintByteLength(v));
  encodeVarintInto(out, 0, v);
  return out;
}

export interface VarintResult {
  value: bigint;
  bytesRead: number;
}

export function decodeVarint(
  buf: Uint8Array,
  offset = 0,
  limit: number = buf.length,
): VarintResult {
  let result = 0n;
  let shift = 0n;
  for (let i = 0; i < MAX_VARINT_BYTES; i++) {
    const pos = offset + i;
    if (pos >= limit) {
      throw new ProtobufDecodeError(
        'truncated varint: buffer ended before a terminating byte',
        pos,
      );
    }
    const byte = buf[pos];
    if (i === 9 && byte > 0x01) {
      throw new ProtobufDecodeError(
        'malformed varint: 10th byte overflows 64 bits',
        pos,
      );
    }
    result |= BigInt(byte & 0x7f) << shift;
    if ((byte & 0x80) === 0) {
      return { value: result & MASK_64, bytesRead: i + 1 };
    }
    shift += 7n;
  }
  throw new ProtobufDecodeError(
    'malformed varint: exceeds 10 bytes',
    offset + MAX_VARINT_BYTES,
  );
}

export function zigzagEncode(value: bigint): bigint {
  return ((value << 1n) ^ (value >> 63n)) & MASK_64;
}

export function zigzagDecode(value: bigint): bigint {
  const v = value & MASK_64;
  return (v >> 1n) ^ -(v & 1n);
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

export function toSigned32(value: number): number {
  return value | 0;
}
