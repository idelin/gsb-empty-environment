/**
 * Content addressing and the in-memory offline content store.
 *
 * The content id is `sha256:<hex>` over the *canonical* JSON bytes, so two
 * structurally identical contexts always share one id regardless of key order
 * or whitespace. Nothing in this module performs network I/O.
 */

import { createHash } from "node:crypto";

import {
  canonicalBytes,
  canonicalJson,
  constantTimeEqualHex,
  fail,
  toHex,
} from "./canonical.ts";
import { CONTENT_ID_ALGORITHM } from "./types.ts";
import type {
  ContentId,
  ContextObject,
  ContentStore,
} from "./types.ts";

export function digestHexOf(value: unknown): string {
  return createHash("sha256").update(canonicalBytes(value)).digest("hex");
}

export function contentIdOf(value: unknown): ContentId {
  return `${CONTENT_ID_ALGORITHM}:${digestHexOf(value)}`;
}

export function digestFromCid(cid: ContentId): string {
  const prefix = `${CONTENT_ID_ALGORITHM}:`;
  if (!cid.startsWith(prefix)) {
    fail("CONTENT_MALFORMED", "Content id uses an unsupported algorithm", cid);
  }
  return cid.slice(prefix.length);
}

interface StoredEntry {
  readonly object: ContextObject;
  readonly bytes: Uint8Array;
}

/**
 * Immutable offline store. Objects are ingested once; the store refuses any
 * attempt to register different bytes under an existing id.
 */
export class InMemoryContentStore implements ContentStore {
  readonly #entries = new Map<ContentId, StoredEntry>();

  ingest(object: ContextObject): ContentId {
    const bytes = canonicalBytes(object);
    const cid = contentIdOf(object);
    const existing = this.#entries.get(cid);
    if (existing !== undefined) {
      const existingHex = toHex(sha256(existing.bytes));
      const incomingHex = toHex(sha256(bytes));
      if (!constantTimeEqualHex(existingHex, incomingHex)) {
        fail("CONTENT_DIGEST_MISMATCH", "Conflicting content registered under one id", cid);
      }
      return cid;
    }
    this.#entries.set(cid, { object, bytes });
    return cid;
  }

  has(cid: ContentId): boolean {
    return this.#entries.has(cid);
  }

  get(cid: ContentId): ContextObject | undefined {
    return this.#entries.get(cid)?.object;
  }

  canonicalBytesOf(cid: ContentId): Uint8Array | undefined {
    return this.#entries.get(cid)?.bytes;
  }

  canonicalBytes(cid: ContentId): Uint8Array | undefined {
    return this.canonicalBytesOf(cid);
  }

  ids(): readonly ContentId[] {
    return [...this.#entries.keys()];
  }
}

function sha256(bytes: Uint8Array): Uint8Array {
  return createHash("sha256").update(bytes).digest();
}

/** Recompute and verify the digest of stored canonical bytes. */
export function verifyStoredDigest(store: ContentStore, cid: ContentId): boolean {
  const bytes = store.canonicalBytes(cid);
  if (bytes === undefined) return false;
  const expected = digestFromCid(cid);
  return constantTimeEqualHex(toHex(sha256(bytes)), expected);
}

export { canonicalJson };
