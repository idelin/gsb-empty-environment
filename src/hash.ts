import { createHash, timingSafeEqual } from 'node:crypto';
import type { Event, Sha256Fn } from './types.ts';
import { serializeCanonicalEvent } from './canonical.ts';

/** Synchronous SHA-256 via node:crypto, returning raw bytes. */
export function nodeSha256(data: Uint8Array): Uint8Array {
  return createHash('sha256').update(data).digest();
}

function bytesToLowerHex(bytes: Uint8Array): string {
  let hex = '';
  for (const byte of bytes) {
    hex += byte.toString(16).padStart(2, '0');
  }
  return hex;
}

/**
 * Computes the NIP-01 event id: lowercase hex of
 * sha256(canonical serialization of [0,pubkey,created_at,kind,tags,content]).
 * The hash function can be injected (e.g. a WebCrypto-backed implementation).
 */
export async function computeEventId(
  event: Pick<Event, 'pubkey' | 'created_at' | 'kind' | 'tags' | 'content'>,
  sha256: Sha256Fn = nodeSha256,
): Promise<string> {
  const serialized = serializeCanonicalEvent([
    0,
    event.pubkey,
    event.created_at,
    event.kind,
    event.tags,
    event.content,
  ]);
  const digest = await sha256(new TextEncoder().encode(serialized));
  return bytesToLowerHex(digest);
}

/**
 * Constant-time comparison of two lowercase hex strings. Length is checked
 * first (which is fine to leak; it is fixed by the format anyway).
 */
export function safeHexEqual(a: string, b: string): boolean {
  if (a.length !== b.length) {
    return false;
  }
  return timingSafeEqual(
    new TextEncoder().encode(a),
    new TextEncoder().encode(b),
  );
}
