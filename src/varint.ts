/**
 * Varint (7-bit groups, LSB-first) and ZigZag primitives.
 * Zero-allocation hot paths: decode operates directly on a Uint8Array window.
 */
import {
  MalformedVarintError,
  TruncatedFieldError,
  VarintOverflowError,
} from "./errors.ts";

export const MAX_VARINT_BYTES = 10; // ceil(64 / 7)

/**
 * Decode an unsigned varint from `buf[pos..end)` as a JS number.
 * Throws VarintOverflowError when the value exceeds 2^53 - 1.
 * Returns [value, nextPos].
 */
export function decodeVarint(
  buf: Uint8Array,
  pos: number,
  end: number,
): [number, number] {
  let lo = 0; // bits 0..31  (kept as int32, read back with >>> 0)
  let hi = 0; // bits 32..63
  let i = 0;
  const start = pos;
  while (pos < end) {
    const b = buf[pos++];
    if (i < 4) {
      lo |= (b & 0x7f) << (i * 7);
    } else if (i === 4) {
      lo |= (b & 0x0f) << 28;
      hi = (b >> 4) & 0x07;
    } else {
      hi |= (b & 0x7f) << (i * 7 - 32);
    }
    if ((b & 0x80) === 0) {
      const hiU = hi >>> 0;
      if (hiU > 0x1fffff) throw new VarintOverflowError(); // > 2^53 - 1
      return [hiU * 0x100000000 + (lo >>> 0), pos];
    }
    if (++i === MAX_VARINT_BYTES) throw new MalformedVarintError(start);
  }
  throw new TruncatedFieldError(start, 1, pos - start, "varint");
}

/**
 * Decode an unsigned varint as a bigint (full 64-bit range).
 * Returns [value, nextPos].
 */
export function decodeVarint64(
  buf: Uint8Array,
  pos: number,
  end: number,
): [bigint, number] {
  let lo = 0;
  let hi = 0;
  let i = 0;
  const start = pos;
  while (pos < end) {
    const b = buf[pos++];
    if (i < 4) {
      lo |= (b & 0x7f) << (i * 7);
    } else if (i === 4) {
      lo |= (b & 0x0f) << 28;
      hi = (b >> 4) & 0x07;
    } else {
      hi |= (b & 0x7f) << (i * 7 - 32);
    }
    if ((b & 0x80) === 0) {
      return [(BigInt(hi >>> 0) << 32n) | BigInt(lo >>> 0), pos];
    }
    if (++i === MAX_VARINT_BYTES) throw new MalformedVarintError(start);
  }
  throw new TruncatedFieldError(start, 1, pos - start, "varint64");
}

/** Encode an unsigned integer (number or bigint, 0 <= v < 2^64) as varint. */
export function encodeVarintInto(
  value: number | bigint,
  out: Uint8Array,
  pos: number,
): number {
  let v = typeof value === "bigint" ? value : BigInt(value);
  if (v < 0n) v = BigInt.asUintN(64, v); // two's-complement wrap, protobuf int64 semantics
  while (v > 0x7fn) {
    out[pos++] = Number(v & 0x7fn) | 0x80;
    v >>= 7n;
  }
  out[pos++] = Number(v);
  return pos;
}

/** Byte length of `value` when varint-encoded. */
export function varintLength(value: number | bigint): number {
  let v = typeof value === "bigint" ? value : BigInt(value);
  if (v < 0n) return MAX_VARINT_BYTES; // negative int64 always takes 10 bytes
  let n = 1;
  while (v > 0x7fn) {
    v >>= 7n;
    n++;
  }
  return n;
}

/* ---------------- ZigZag ---------------- */

export function zigzagEncode32(n: number): number {
  return (n << 1) ^ (n >> 31);
}

export function zigzagDecode32(n: number): number {
  return (n >>> 1) ^ -(n & 1);
}

export function zigzagEncode64(n: bigint): bigint {
  return (n << 1n) ^ (n >> 63n);
}

export function zigzagDecode64(n: bigint): bigint {
  return (n >> 1n) ^ -(n & 1n);
}
