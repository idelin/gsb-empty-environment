/**
 * In-memory acceptance fixtures. Everything is generated locally; no network
 * or persistent state is involved.
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
  ContentId,
  ContextObject,
  SignedManifestEnvelope,
  TrustManifest,
} from "../src/index.ts";

export interface AcceptanceHarness {
  readonly store: InMemoryContentStore;
  readonly manifest: TrustManifest;
  readonly envelope: SignedManifestEnvelope;
  readonly allowed: Awaited<ReturnType<typeof verifyManifest>>["allowed"];
  readonly manifestCid: ContentId;
  readonly rootKeyId: string;
  readonly rootPublicKeySpki: string;
}

export async function buildAcceptanceHarness(
  contexts: readonly ContextObject[],
): Promise<AcceptanceHarness> {
  const store = new InMemoryContentStore();
  const keyPair = await generateRootKeyPair();
  const rootPublicKeySpki = await exportPublicKeySpki(keyPair.publicKey);
  const indexed: Array<{ cid: ContentId; label: string }> = [];
  const seen = new Set<ContentId>();
  for (const context of contexts) {
    const cid = store.ingest(context);
    if (!seen.has(cid)) {
      seen.add(cid);
      indexed.push({ cid, label: `acceptance-${seen.size - 1}` });
    }
  }
  const manifest = buildManifest(
    "acceptance-root",
    rootPublicKeySpki,
    indexed,
    "2026-11-26T00:00:00Z",
  );
  const envelope = await signManifest(manifest, keyPair.privateKey);
  const verified = await verifyManifest(envelope);
  return {
    store,
    manifest,
    envelope,
    allowed: verified.allowed,
    manifestCid: contentIdOf(manifest),
    rootKeyId: manifest.rootKeyId,
    rootPublicKeySpki,
  };
}

/** Scenario contexts shared by the acceptance runner and the README examples. */
export function nestConflictContexts(): {
  readonly contexts: readonly ContextObject[];
  readonly root: ContextObject;
} {
  const protectedContext: ContextObject = {
    "@protected": true,
    level: { "@id": "https://policy.test/#level", "@nest": "wrapper" },
  };
  const root: ContextObject = {
    "@import": contentIdOf(protectedContext),
    aliasLevel: {
      "@id": "https://policy.test/#level",
      "@nest": "wrapper",
    },
  };
  return { contexts: [protectedContext, root], root };
}

export function propagateContexts(): {
  readonly contexts: readonly ContextObject[];
  readonly root: ContextObject;
} {
  const scoped: ContextObject = {
    "@propagate": false,
    "@vocab": "https://scoped.test/#",
    "@base": "https://scoped.test/base/",
    scopedOnly: "https://scoped.test/#term",
  };
  const root: ContextObject = {
    "@vocab": "https://root.test/#",
    "@base": "https://root.test/base/",
    "@import": contentIdOf(scoped),
    rootOnly: "https://root.test/#term",
  };
  return { contexts: [scoped, root], root };
}

export function diamondContexts(divergent: boolean): {
  readonly contexts: readonly ContextObject[];
  readonly root: ContextObject;
} {
  const leaf: ContextObject = {
    "@protected": true,
    shared: "https://policy.test/#shared",
  };
  const left: ContextObject = { "@import": contentIdOf(leaf) };
  const right: ContextObject = divergent
    ? { otherName: "https://policy.test/#shared" }
    : { "@import": contentIdOf(leaf) };
  const diamond: ContextObject = {
    "@import": [contentIdOf(left), contentIdOf(right)],
  };
  const root: ContextObject = { "@import": contentIdOf(diamond) };
  return { contexts: [leaf, left, right, diamond, root], root };
}
