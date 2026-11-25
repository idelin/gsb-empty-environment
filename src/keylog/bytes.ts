import { formatDot, type Dot } from "../dot.ts";

/**
 * Canonical, length-prefixed byte representation.
 *
 * Everything that is signed or used to detect equivocation is encoded with
 * these helpers so that a given logical value always maps to the exact same
 * sequence of bytes on every replica. Numbers are always bigints and are
 * encoded as their ASCII decimal form, which keeps uint64 values beyond
 * Number.MAX_SAFE_INTEGER exactly representable.
 */

const encoder = new TextEncoder();

/** Length-prefixed UTF-8 blob: 4-byte big-endian length followed by bytes. */
export function lpBytes(out: number[], value: Uint8Array): void {
  u32(out, value.length);
  for (const byte of value) out.push(byte);
}

/** Length-prefixed UTF-8 string. */
export function lpString(out: number[], value: string): void {
  lpBytes(out, encoder.encode(value));
}

/** Unsigned 32-bit big-endian integer. */
export function u32(out: number[], value: number): void {
  if (!Number.isInteger(value) || value < 0 || value > 0xffffffff) {
    throw new Error("u32 out of range");
  }
  out.push((value >>> 24) & 0xff, (value >>> 16) & 0xff, (value >>> 8) & 0xff, value & 0xff);
}

/** Unsigned bigint encoded as a length-prefixed ASCII decimal string. */
export function uBig(out: number[], value: bigint): void {
  if (value < 0n) throw new Error("negative unsigned integer");
  lpString(out, value.toString(10));
}

/** A domain tag disambiguates record kinds even when their bodies align. */
export function tag(out: number[], value: string): void {
  lpString(out, value);
}

export function concat(parts: number[][]): Uint8Array {
  let total = 0;
  for (const part of parts) total += part.length;
  const result = new Uint8Array(total);
  let offset = 0;
  for (const part of parts) {
    result.set(part, offset);
    offset += part.length;
  }
  return result;
}

/** Hex of a byte array, used to render content hashes in summaries. */
export function toHex(value: Uint8Array): string {
  let out = "";
  for (const byte of value) out += byte.toString(16).padStart(2, "0");
  return out;
}

export function fromHex(value: string): Uint8Array {
  if (value.length % 2 !== 0 || /[^0-9a-fA-F]/.test(value)) throw new Error("bad hex");
  const out = new Uint8Array(value.length / 2);
  for (let i = 0; i < out.length; i++) out[i] = parseInt(value.slice(i * 2, i * 2 + 2), 16);
  return out;
}

/** Constant-time equality for signature/MAC comparisons. */
export function timingSafeEqual(left: Uint8Array, right: Uint8Array): boolean {
  if (left.length !== right.length) return false;
  let diff = 0;
  for (let i = 0; i < left.length; i++) diff |= left[i] ^ right[i];
  return diff === 0;
}

/**
 * Persistent event identifier: the event's local dot, rendered with the same
 * API and textual/byte representation as the rest of the repository.
 */
export function eventId(dot: Dot): string {
  return formatDot(dot);
}

/** Sorted observed-prefix encoding. compareDots is only used for byte stability. */
export function encodeObserved(out: number[], observed: readonly Dot[]): void {
  const sorted = [...observed].sort((a, b) => compareForBytes(a, b));
  u32(out, sorted.length);
  for (const dot of sorted) lpString(out, formatDot(dot));
}

// Local copy of compareDots semantics so bytes never depend on storage layout.
function compareForBytes(left: Dot, right: Dot): number {
  if (left.actor !== right.actor) return left.actor < right.actor ? -1 : 1;
  return left.sequence < right.sequence ? -1 : left.sequence > right.sequence ? 1 : 0;
}
