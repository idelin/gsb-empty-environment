/**
 * Offline fixture factory: generates Ed25519 roots, content-addressed stores
 * and signed manifests entirely in-process. No network or filesystem access.
 */

import {
  InMemoryContentStore,
  buildManifest,
  contentIdOf,
  exportPublicKeySpki,
  generateRootKeyPair,
  signManifest,
  verifyManifest,
} from "../src/index.ts";
import type {
  AllowedContextEntry,
  ContentId,
  ContextObject,
  SignedManifestEnvelope,
  TrustManifest,
} from "../src/index.ts";

export interface Harness {
  readonly store: InMemoryContentStore;
  readonly rootKeyId: string;
  readonly rootPublicKeySpki: string;
  readonly privateKey: CryptoKey;
  readonly manifest: TrustManifest;
  readonly envelope: SignedManifestEnvelope;
  readonly allowed: ReadonlyMap<ContentId, AllowedContextEntry>;
  readonly manifestCid: ContentId;
  ingest(context: ContextObject): ContentId;
  /** Re-sign after adding late contexts (e.g. malicious unlisted content). */
  resign(): Promise<Harness>;
}

export async function buildHarness(
  contexts: readonly ContextObject[],
  rootKeyId = "test-root-key",
): Promise<Harness> {
  const store = new InMemoryContentStore();
  const keyPair = await generateRootKeyPair();
  const spki = await exportPublicKeySpki(keyPair.publicKey);
  const indexed: Array<{ cid: ContentId; label: string }> = [];
  const seen = new Set<ContentId>();
  for (const context of contexts) {
    const cid = store.ingest(context);
    if (seen.has(cid)) continue;
    seen.add(cid);
    indexed.push({ cid, label: `ctx-${seen.size - 1}` });
  }
  const manifest = buildManifest(
    rootKeyId,
    spki,
    indexed,
    "2026-11-26T00:00:00Z",
  );
  const envelope = await signManifest(manifest, keyPair.privateKey);
  const verified = await verifyManifest(envelope);

  const harness: Harness = {
    store,
    rootKeyId,
    rootPublicKeySpki: spki,
    privateKey: keyPair.privateKey,
    manifest,
    envelope,
    allowed: verified.allowed,
    manifestCid: contentIdOf(manifest),
    ingest(context: ContextObject): ContentId {
      return store.ingest(context);
    },
    async resign(): Promise<Harness> {
      const next = buildManifest(
        rootKeyId,
        spki,
        store.ids().map((cid, index) => ({ cid, label: `ctx-${index}` })),
        "2026-11-26T00:00:00Z",
      );
      const nextEnvelope = await signManifest(next, keyPair.privateKey);
      const nextVerified = await verifyManifest(nextEnvelope);
      return {
        ...harness,
        manifest: next,
        envelope: nextEnvelope,
        allowed: nextVerified.allowed,
        manifestCid: contentIdOf(next),
      };
    },
  };
  return harness;
}

export function expectReject(
  fn: () => unknown,
  expectedCode: string,
): { readonly code: string; readonly diagnostic: unknown } {
  try {
    fn();
  } catch (error) {
    const diagnostic = (error as { diagnostic?: unknown }).diagnostic;
    if (
      diagnostic === null ||
      typeof diagnostic !== "object" ||
      (diagnostic as { code?: unknown }).code !== expectedCode
    ) {
      throw new Error(
        `Expected code ${expectedCode}, got ${JSON.stringify(diagnostic)}`,
      );
    }
    return {
      code: expectedCode,
      diagnostic,
    };
  }
  throw new Error(`Expected rejection with ${expectedCode} but call succeeded`);
}

export async function expectRejectAsync(
  fn: () => Promise<unknown>,
  expectedCode: string,
): Promise<{ readonly code: string; readonly diagnostic: unknown }> {
  try {
    await fn();
  } catch (error) {
    const diagnostic = (error as { diagnostic?: unknown }).diagnostic;
    if (
      diagnostic === null ||
      typeof diagnostic !== "object" ||
      (diagnostic as { code?: unknown }).code !== expectedCode
    ) {
      throw new Error(
        `Expected code ${expectedCode}, got ${JSON.stringify(diagnostic)}`,
      );
    }
    return { code: expectedCode, diagnostic };
  }
  throw new Error(`Expected rejection with ${expectedCode} but call succeeded`);
}
