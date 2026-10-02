import { ProtobufTruncationError, ProtobufWireError } from './errors.ts';

/** Maximum number of bytes a 64-bit varint may occupy. */
export const MAX_VARINT_BYTES = 10;

/**
 * Encode an unsigned 64-bit integer as a varint (7 bits per byte, LSB first,
 * high bit set on every byte except the last).
 */
export function encodeVarint(value: bigint): Uint8Array {
  let v = BigInt.asUintN(64, value);
  const out: number[] = [];
  do {
    let byte = Number(v & 0x7fn);
    v >>= 7n;
    if (v !== 0n) byte |= 0x80;
    out.push(byte);
  } while (v !== 0n);
  return Uint8Array.from(out);
}

/**
 * Decode a varint starting at `offset`.
 * Returns the decoded unsigned 64-bit value and the offset just past it.
 *
 * Safety: throws ProtobufTruncationError when the buffer ends mid-varint and
 * ProtobufWireError when the varint exceeds 64 bits — the buffer is never
 * read out of bounds.
 */
export function decodeVarint(
  buf: Uint8Array,
  offset: number
): { value: bigint; offset: number } {
  let result = 0n;
  let shift = 0n;
  let pos = offset;
  for (let i = 0; i < MAX_VARINT_BYTES; i++) {
    if (pos >= buf.length) {
      throw new ProtobufTruncationError(pos, 1, buf.length - pos);
    }
    const byte = buf[pos++];
    if (i === MAX_VARINT_BYTES - 1 && byte > 1) {
      throw new ProtobufWireError(`varint at offset ${offset} exceeds 64 bits`);
    }
    result |= BigInt(byte & 0x7f) << shift;
    if ((byte & 0x80) === 0) {
      return { value: result, offset: pos };
    }
    shift += 7n;
  }
  throw new ProtobufWireError(`varint at offset ${offset} is too long`);
}
