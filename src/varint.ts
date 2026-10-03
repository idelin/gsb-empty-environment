/**
 * Low-level binary codecs: Varint (7-bit groups), ZigZag, Fixed32/64.
 * All 64-bit values are handled as bigint to avoid precision loss.
 * Zero external dependencies.
 */

export const MAX_VARINT_BYTES = 10; // ceil(64 / 7)

export class VarintOverflowError extends Error {
  constructor() {
    super('varint exceeds 64 bits (more than 10 bytes or excess bits set)');
    this.name = 'VarintOverflowError';
  }
}

export class TruncatedFieldError extends Error {
  constructor(what: string, needed: number, available: number) {
    super(`truncated ${what}: need ${needed} byte(s), only ${available} available`);
    this.name = 'TruncatedFieldError';
  }
}

const MASK_64 = (1n << 64n) - 1n;

/** Encode an unsigned 64-bit value as varint. */
export function encodeVarint(value: bigint | number): Uint8Array {
  let v = BigInt(value) & MASK_64;
  const out = new Uint8Array(MAX_VARINT_BYTES);
  let i = 0;
  while (v > 0x7fn) {
    out[i++] = Number(v & 0x7fn) | 0x80;
    v >>= 7n;
  }
  out[i++] = Number(v);
  return out.subarray(0, i);
}

/**
 * Decode a varint from `buf` starting at `offset`.
 * Throws TruncatedFieldError if the buffer ends mid-varint and
 * VarintOverflowError if more than 64 bits are encoded.
 */
export function decodeVarint(
  buf: Uint8Array,
  offset: number,
): { value: bigint; bytesRead: number } {
  let result = 0n;
  for (let i = 0; i < MAX_VARINT_BYTES; i++) {
    const pos = offset + i;
    if (pos >= buf.length) {
      throw new TruncatedFieldError('varint', i + 1, buf.length - offset);
    }
    const byte = buf[pos];
    if (i === 9 && byte > 0x01) {
      // 10th byte may only carry 1 bit for a 64-bit value
      throw new VarintOverflowError();
    }
    result |= BigInt(byte & 0x7f) << BigInt(7 * i);
    if ((byte & 0x80) === 0) {
      return { value: result, bytesRead: i + 1 };
    }
  }
  throw new VarintOverflowError();
}

/** ZigZag: map signed integers to unsigned so small magnitudes stay small. */
export function zigzagEncode(value: bigint | number): bigint {
  const v = BigInt(value);
  return ((v << 1n) ^ (v >> 63n)) & MASK_64;
}

export function zigzagDecode(value: bigint): bigint {
  return (value >> 1n) ^ -(value & 1n);
}

export function encodeFixed32(value: number): Uint8Array {
  const out = new Uint8Array(4);
  const view = new DataView(out.buffer);
  view.setUint32(0, value >>> 0, true);
  return out;
}

export function decodeFixed32(buf: Uint8Array, offset: number): number {
  if (offset + 4 > buf.length) {
    throw new TruncatedFieldError('fixed32', 4, buf.length - offset);
  }
  const view = new DataView(buf.buffer, buf.byteOffset + offset, 4);
  return view.getUint32(0, true);
}

export function encodeFixed64(value: bigint | number): Uint8Array {
  const out = new Uint8Array(8);
  const view = new DataView(out.buffer);
  view.setBigUint64(0, BigInt(value) & MASK_64, true);
  return out;
}

export function decodeFixed64(buf: Uint8Array, offset: number): bigint {
  if (offset + 8 > buf.length) {
    throw new TruncatedFieldError('fixed64', 8, buf.length - offset);
  }
  const view = new DataView(buf.buffer, buf.byteOffset + offset, 8);
  return view.getBigUint64(0, true);
}

/** Reinterpret an unsigned 64-bit pattern as signed two's complement. */
export function toSigned64(value: bigint): bigint {
  const v = value & MASK_64;
  return v >= 1n << 63n ? v - (1n << 64n) : v;
}

/** Reinterpret an unsigned 32-bit pattern as signed two's complement. */
export function toSigned32(value: number): number {
  return value | 0;
}
