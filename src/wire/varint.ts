import { ProtobufDecodeError } from './errors.ts';

export const MAX_VARINT_BYTES = 10; // ceil(64 / 7)
const MASK_64 = (1n << 64n) - 1n;

/**
 * Decode an unsigned 64-bit varint (7 bits per byte, little-endian groups,
 * MSB = continuation bit). Pure hand-rolled parsing, no external deps.
 *
 * Throws ProtobufDecodeError on truncation (buffer ends mid-varint) or on
 * varints longer than 10 bytes (64-bit overflow guard).
 */
export function readVarint(buf: Uint8Array, offset: number): { value: bigint; offset: number } {
  let result = 0n;
  let shift = 0n;
  let pos = offset;

  for (let i = 0; i < MAX_VARINT_BYTES; i++) {
    if (pos >= buf.length) {
      throw new ProtobufDecodeError(
        `truncated varint: buffer ended after ${i} byte(s) of varint starting at ${offset}`,
        offset,
      );
    }
    const byte = buf[pos];
    pos++;

    if (i === 9) {
      // 10th byte may only carry a single payload bit for a 64-bit value.
      if (byte > 0x01) {
        throw new ProtobufDecodeError('varint exceeds 64 bits (10th byte payload > 1)', offset);
      }
      result |= BigInt(byte) << shift;
      if (byte & 0x80) {
        throw new ProtobufDecodeError('varint exceeds 64 bits (continuation past 10th byte)', offset);
      }
      return { value: result, offset: pos };
    }

    result |= BigInt(byte & 0x7f) << shift;
    if ((byte & 0x80) === 0) {
      return { value: result, offset: pos };
    }
    shift += 7n;
  }
  // Unreachable, but keeps the control flow explicit.
  throw new ProtobufDecodeError('varint exceeds 64 bits', offset);
}

/** Encode an unsigned 64-bit value as a varint. Accepts number or bigint. */
export function writeVarint(value: number | bigint): Uint8Array {
  let v = typeof value === 'bigint' ? value : BigInt(value);
  if (v < 0n) throw new RangeError('writeVarint expects an unsigned value');
  v &= MASK_64;
  const out: number[] = [];
  do {
    let byte = Number(v & 0x7fn);
    v >>= 7n;
    if (v !== 0n) byte |= 0x80;
    out.push(byte);
  } while (v !== 0n);
  return Uint8Array.from(out);
}

/** Number of bytes the varint encoding of `value` occupies. */
export function varintLength(value: number | bigint): number {
  let v = typeof value === 'bigint' ? value : BigInt(value);
  let n = 1;
  while (v >= 0x80n) {
    v >>= 7n;
    n++;
  }
  return n;
}

/** ZigZag-encode a signed 64-bit integer: (n << 1) ^ (n >> 63). */
export function zigzagEncode(value: bigint | number): bigint {
  const v = typeof value === 'bigint' ? value : BigInt(value);
  return ((v << 1n) ^ (v >> 63n)) & MASK_64;
}

/** ZigZag-decode an unsigned 64-bit integer back to signed. */
export function zigzagDecode(value: bigint): bigint {
  return (value >> 1n) ^ (-(value & 1n));
}

/** Reinterpret an unsigned 64-bit value as two's-complement signed. */
export function toSigned64(value: bigint): bigint {
  const v = value & MASK_64;
  return v >= 1n << 63n ? v - (1n << 64n) : v;
}

/** Reinterpret a signed 64-bit value as its unsigned bit pattern. */
export function toUnsigned64(value: bigint): bigint {
  return value & MASK_64;
}

/** Reinterpret an unsigned 32-bit value as two's-complement signed. */
export function toSigned32(value: number): number {
  return value | 0;
}
