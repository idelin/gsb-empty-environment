import { DecodeError, EncodeError } from './errors.ts';

const MASK_64 = (1n << 64n) - 1n;
const MAX_VARINT_BYTES = 10; // ceil(64 / 7)

/**
 * Encode an unsigned 64-bit integer as a protobuf varint (7 bits per byte,
 * little-endian group order, MSB = continuation bit).
 * Negative bigints are reinterpreted as two's-complement uint64 (protobuf int64 semantics).
 */
export function encodeVarint(value: bigint | number): Uint8Array {
  let v = typeof value === 'number' ? BigInt(value) : value;
  if (v > MASK_64 || v < -(1n << 63n)) {
    throw new EncodeError(`varint value out of 64-bit range: ${v}`);
  }
  v &= MASK_64; // two's-complement reinterpretation for negatives
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
  /** Unsigned 64-bit interpretation of the decoded varint. */
  value: bigint;
  bytesRead: number;
}

/**
 * Decode a varint from `buf` starting at `offset`.
 * Throws DecodeError on truncation (buffer ends mid-varint) or on
 * overflow (more than 10 bytes / bits beyond 64).
 */
export function decodeVarint(buf: Uint8Array, offset: number): VarintResult {
  if (offset < 0 || offset >= buf.length) {
    throw new DecodeError('varint: offset out of bounds', offset);
  }
  let result = 0n;
  for (let i = 0; i < MAX_VARINT_BYTES; i++) {
    const pos = offset + i;
    if (pos >= buf.length) {
      throw new DecodeError('varint: truncated (buffer ended mid-varint)', pos);
    }
    const byte = buf[pos];
    if (i === MAX_VARINT_BYTES - 1) {
      // 10th byte may carry at most 1 payload bit (bit 63).
      if (byte > 0x01) {
        throw new DecodeError('varint: exceeds 64 bits', pos);
      }
    }
    result |= BigInt(byte & 0x7f) << BigInt(7 * i);
    if ((byte & 0x80) === 0) {
      return { value: result, bytesRead: i + 1 };
    }
  }
  throw new DecodeError('varint: too many continuation bytes', offset + MAX_VARINT_BYTES);
}

/** ZigZag-encode a signed 64-bit integer into an unsigned 64-bit varint input. */
export function zigzagEncode(value: bigint | number): bigint {
  const v = typeof value === 'number' ? BigInt(value) : value;
  if (v > (1n << 63n) - 1n || v < -(1n << 63n)) {
    throw new EncodeError(`zigzag value out of int64 range: ${v}`);
  }
  return ((v << 1n) ^ (v >> 63n)) & MASK_64;
}

/** ZigZag-decode an unsigned 64-bit integer back to a signed value. */
export function zigzagDecode(value: bigint): bigint {
  return (value >> 1n) ^ (-(value & 1n));
}
