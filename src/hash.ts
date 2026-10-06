/**
 * Custom byte-polynomial hash (FNV-1a, 32-bit) over UTF-8 bytes.
 *
 *   h = 2166136261
 *   for each UTF-8 byte b of the input: h = (h XOR b) * 16777619 (mod 2^32)
 *
 * Bucketing reduces the hash modulo 10_000, giving buckets [0, 9999].
 */
export const FNV1A_32_OFFSET_BASIS = 0x811c9dc5;
export const FNV1A_32_PRIME = 0x01000193;
export const BUCKET_COUNT = 10_000;

export function hashBytes(bytes: Uint8Array): number {
  let h = FNV1A_32_OFFSET_BASIS;
  for (let i = 0; i < bytes.length; i++) {
    h ^= bytes[i];
    h = Math.imul(h, FNV1A_32_PRIME);
  }
  return h >>> 0;
}

const encoder = new TextEncoder();

export function fnv1a32(input: string): number {
  return hashBytes(encoder.encode(input));
}

/** Bucket in [0, 9999] for an arbitrary key string. */
export function bucketOf(key: string): number {
  return fnv1a32(key) % BUCKET_COUNT;
}

/** Canonical bucketing key: salt (if any) scopes the flag/user pair. */
export function bucketKey(flagId: string, userId: string, salt?: string): string {
  return salt !== undefined ? `${salt}:${flagId}:${userId}` : `${flagId}:${userId}`;
}
