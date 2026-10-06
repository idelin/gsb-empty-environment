/**
 * Byte-polynomial hash (FNV-1a, 32-bit) over UTF-8 bytes.
 *
 * h = offset_basis
 * for each byte b: h = (h XOR b) * prime   (mod 2^32)
 *
 * Buckets are derived as hash % 10000, giving a stable bucket in [0, 9999].
 */

export const FNV1A_32_OFFSET_BASIS = 0x811c9dc5;
export const FNV1A_32_PRIME = 0x01000193;
export const BUCKET_COUNT = 10000;

/** Encode a string as UTF-8 bytes (never UTF-16 code units). */
export function utf8Bytes(input: string): Uint8Array {
  return new TextEncoder().encode(input);
}

/** FNV-1a 32-bit hash of a byte sequence. */
export function fnv1a32(bytes: Uint8Array): number {
  let hash = FNV1A_32_OFFSET_BASIS;
  for (let i = 0; i < bytes.length; i++) {
    hash ^= bytes[i];
    hash = Math.imul(hash, FNV1A_32_PRIME) >>> 0;
  }
  return hash >>> 0;
}

/** FNV-1a 32-bit hash of the UTF-8 encoding of `input`. */
export function hashString(input: string): number {
  return fnv1a32(utf8Bytes(input));
}

/** Unit-separator join so key parts can never blur together ("a|bc" vs "ab|c"). */
const KEY_SEPARATOR = "\u001f";

export function bucketKey(parts: ReadonlyArray<string | number>): string {
  return parts.join(KEY_SEPARATOR);
}

/** Stable bucket in [0, BUCKET_COUNT - 1] for an arbitrary key string. */
export function bucketOf(key: string): number {
  return hashString(key) % BUCKET_COUNT;
}
