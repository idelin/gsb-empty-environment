import { MalformedMessageError, TruncatedMessageError } from './errors.ts';

/** A 64-bit varint occupies at most 10 bytes (ceil(64 / 7)). */
export const MAX_VARINT_BYTES = 10;

const MASK_64 = (1n << 64n) - 1n;

/** Encode an unsigned 64-bit value as a protobuf varint (7 bits per byte, LSB first). */
export function encodeVarint(value: number | bigint): Uint8Array {
  let v = BigInt(value);
  if (v < 0n) throw new RangeError(`encodeVarint expects an unsigned value, got ${v}`);
  v &= MASK_64;
  const out = new Uint8Array(MAX_VARINT_BYTES);
  let i = 0;
  do {
    let byte = Number(v & 0x7fn);
    v >>= 7n;
    if (v !== 0n) byte |= 0x80;
    out[i++] = byte;
  } while (v !== 0n);
  return out.subarray(0, i);
}

/** Encode a signed value the way protobuf `int32`/`int64` do: two's complement widened to 64 bits. */
export function encodeVarintSigned(value: number | bigint): Uint8Array {
  return encodeVarint(BigInt.asUintN(64, BigInt(value)));
}

export interface VarintResult {
  value: bigint;
  bytesRead: number;
}

/**
 * Decode a varint from `buf` starting at `offset`.
 * Throws TruncatedMessageError if the buffer ends mid-varint,
 * MalformedMessageError if the varint exceeds 64 bits (10 bytes).
 */
export function decodeVarint(buf: Uint8Array, offset: number): VarintResult {
  let result = 0n;
  for (let i = 0; i < MAX_VARINT_BYTES; i++) {
    const pos = offset + i;
    if (pos >= buf.length) {
      throw new TruncatedMessageError(offset, i + 1, buf.length - offset, 'varint');
    }
    const byte = buf[pos];
    if (i === MAX_VARINT_BYTES - 1 && byte > 1) {
      // The 10th byte may only carry the single remaining bit of a 64-bit value.
      throw new MalformedMessageError('varint overflows 64 bits', pos);
    }
    result |= BigInt(byte & 0x7f) << BigInt(7 * i);
    if ((byte & 0x80) === 0) {
      return { value: result, bytesRead: i + 1 };
    }
  }
  throw new MalformedMessageError('varint exceeds 10 bytes', offset);
}

/** ZigZag: map signed -> unsigned so small magnitudes stay small. 0->0, -1->1, 1->2, -2->3 ... */
export function zigZagEncode64(value: bigint): bigint {
  return ((value << 1n) ^ (value >> 63n)) & MASK_64;
}

export function zigZagDecode64(value: bigint): bigint {
  return (value >> 1n) ^ -(value & 1n);
}

export function zigZagEncode32(value: number): number {
  return ((value << 1) ^ (value >> 31)) >>> 0;
}

export function zigZagDecode32(value: number): number {
  return (value >>> 1) ^ -(value & 1);
}
