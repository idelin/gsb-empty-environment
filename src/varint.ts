/**
 * Low-level binary primitives: Varint (7-bit groups), ZigZag signed
 * mapping and little-endian Fixed32/Fixed64. Zero external dependencies.
 *
 * All decoders perform strict bounds checking and report structured
 * errors instead of reading out of range.
 */

export class ProtobufDecodeError extends Error {
  constructor(message: string) {
    super(message);
    this.name = new.target.name;
  }
}

/** A read would go past the end of the buffer (field truncated). */
export class TruncatedFieldError extends ProtobufDecodeError {
  readonly needed: number;
  readonly available: number;
  constructor(context: string, needed: number, available: number) {
    super(
      `truncated ${context}: need ${needed} byte(s), only ${available} available`,
    );
    this.needed = needed;
    this.available = available;
  }
}

/** Varint uses more than 10 bytes (max for 64-bit) or is unterminated. */
export class MalformedVarintError extends ProtobufDecodeError {}

/** Tag carries a wire type outside the valid range 0..5 (3/4 deprecated). */
export class InvalidWireTypeError extends ProtobufDecodeError {}

export const MAX_VARINT_BYTES = 10;
const MASK_64 = (1n << 64n) - 1n;

/** Encode an unsigned 64-bit value as a varint. */
export function encodeVarint(value: bigint): Uint8Array {
  let v = BigInt.asUintN(64, value);
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
  readonly value: bigint;
  readonly bytesRead: number;
}

/**
 * Decode a varint from `buf` starting at `offset`.
 * Throws TruncatedFieldError if the buffer ends mid-varint and
 * MalformedVarintError if more than 10 bytes are used.
 */
export function decodeVarint(buf: Uint8Array, offset = 0): VarintResult {
  let result = 0n;
  for (let i = 0; i < MAX_VARINT_BYTES; i++) {
    const pos = offset + i;
    if (pos >= buf.length) {
      throw new TruncatedFieldError("varint", i + 1, buf.length - offset);
    }
    const byte = buf[pos];
    // The 10th byte may only carry 1 bit of payload for a 64-bit value.
    if (i === MAX_VARINT_BYTES - 1 && byte > 0x01) {
      throw new MalformedVarintError(
        `varint overflows 64 bits at offset ${offset}`,
      );
    }
    result |= BigInt(byte & 0x7f) << BigInt(7 * i);
    if ((byte & 0x80) === 0) {
      return { value: result, bytesRead: i + 1 };
    }
  }
  throw new MalformedVarintError(
    `unterminated varint (>${MAX_VARINT_BYTES} bytes) at offset ${offset}`,
  );
}

/** Number of bytes `value` occupies as a varint. */
export function varintLength(value: bigint): number {
  let v = BigInt.asUintN(64, value);
  let n = 1;
  while (v > 0x7fn) {
    v >>= 7n;
    n++;
  }
  return n;
}

/** ZigZag: map signed integers to unsigned so small magnitudes stay small. */
export function zigzagEncode(value: bigint): bigint {
  return BigInt.asUintN(64, (value << 1n) ^ (value >> 63n));
}

export function zigzagDecode(value: bigint): bigint {
  return BigInt.asIntN(64, (value >> 1n) ^ -(value & 1n));
}

/** Little-endian Fixed32. */
export function encodeFixed32(value: number): Uint8Array {
  const out = new Uint8Array(4);
  const view = new DataView(out.buffer);
  view.setUint32(0, value >>> 0, true);
  return out;
}

export function decodeFixed32(buf: Uint8Array, offset = 0): number {
  if (offset + 4 > buf.length) {
    throw new TruncatedFieldError("fixed32", 4, buf.length - offset);
  }
  return new DataView(buf.buffer, buf.byteOffset + offset, 4).getUint32(0, true);
}

/** Little-endian Fixed64. */
export function encodeFixed64(value: bigint): Uint8Array {
  const out = new Uint8Array(8);
  const view = new DataView(out.buffer);
  view.setBigUint64(0, BigInt.asUintN(64, value), true);
  return out;
}

export function decodeFixed64(buf: Uint8Array, offset = 0): bigint {
  if (offset + 8 > buf.length) {
    throw new TruncatedFieldError("fixed64", 8, buf.length - offset);
  }
  return new DataView(buf.buffer, buf.byteOffset + offset, 8).getBigUint64(0, true);
}

export function toUint64(value: bigint | number): bigint {
  return BigInt.asUintN(64, typeof value === "number" ? BigInt(value) : value);
}

export function toInt64(value: bigint | number): bigint {
  return BigInt.asIntN(64, typeof value === "number" ? BigInt(value) : value);
}

export { MASK_64 };
