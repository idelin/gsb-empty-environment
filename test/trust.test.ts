import { test } from "node:test";
import assert from "node:assert/strict";

import {
  ContextResolutionError,
  InMemoryContentStore,
  buildManifest,
  canonicalJson,
  contentIdOf,
  digestHexOf,
  exportPublicKeySpki,
  generateRootKeyPair,
  signManifest,
  verifyManifest,
} from "../src/index.ts";
import { base64UrlDecode, base64UrlEncode } from "../src/canonical.ts";
import { buildHarness, expectReject, expectRejectAsync } from "./helpers.ts";

test("canonical JSON sorts keys and is stable across key order", () => {
  const a = canonicalJson({ b: 1, a: { z: 1, y: 2 }, c: [3, 2, 1] });
  const b = canonicalJson({ c: [3, 2, 1], a: { y: 2, z: 1 }, b: 1 });
  assert.equal(a, b);
  assert.equal(a, '{"a":{"y":2,"z":1},"b":1,"c":[3,2,1]}');
});

test("content ids derive from canonical bytes and survive reordering", () => {
  const store = new InMemoryContentStore();
  const cid1 = store.ingest({ "@vocab": "https://a.test/#", term: "https://a.test/#term" });
  const cid2 = store.ingest({ term: "https://a.test/#term", "@vocab": "https://a.test/#" });
  assert.equal(cid1, cid2);
  assert.match(cid1, /^sha256:[0-9a-f]{64}$/);
});

test("signed manifest verifies with the embedded Ed25519 anchor", async () => {
  const harness = await buildHarness([{ "@vocab": "https://a.test/#" }]);
  const verified = await verifyManifest(harness.envelope, {
    expectedRootKeyId: harness.rootKeyId,
    expectedRootPublicKeySpki: harness.rootPublicKeySpki,
  });
  assert.equal(verified.manifest.rootKeyId, harness.rootKeyId);
  assert.equal(verified.allowed.size, 1);
});

test("tampered manifest signature is hard rejected", async () => {
  const harness = await buildHarness([{ "@vocab": "https://a.test/#" }]);
  const tamperedSignature = base64UrlEncode(
    base64UrlDecode(harness.envelope.signature).fill(7),
  );
  await expectRejectAsync(
    () =>
      verifyManifest({
        manifestCanonical: harness.envelope.manifestCanonical,
        signature: tamperedSignature,
      }),
    "TRUST_MANIFEST_SIGNATURE_INVALID",
  );
});

test("pinned key mismatch rejects a different trust root", async () => {
  const harness = await buildHarness([{ "@vocab": "https://a.test/#" }]);
  const otherPair = await generateRootKeyPair();
  const otherSpki = await exportPublicKeySpki(otherPair.publicKey);
  await expectRejectAsync(
    () =>
      verifyManifest(harness.envelope, {
        expectedRootKeyId: harness.rootKeyId,
        expectedRootPublicKeySpki: otherSpki,
      }),
    "TRUST_MANIFEST_ROOT_MISMATCH",
  );
});

test("context absent from the signed allow-list cannot resolve", async () => {
  const harness = await buildHarness([{ "@vocab": "https://a.test/#" }]);
  const unlistedCid = harness.ingest({ "@vocab": "https://evil.test/#" });
  const { resolveContext } = await import("../src/index.ts");
  expectReject(
    () =>
      resolveContext({
        rootCid: unlistedCid,
        store: harness.store,
        allowed: harness.allowed,
      }),
    "CONTENT_NOT_IN_TRUST_LIST",
  );
});

test("digest tampering is detected even when id stays the same", async () => {
  const context = { "@vocab": "https://a.test/#", safe: "https://a.test/#safe" };
  const harness = await buildHarness([context]);
  const cid = contentIdOf(context);
  const malicious = { "@vocab": "https://evil.test/#", safe: "https://a.test/#safe" };
  const maliciousCid = digestHexOf(malicious);
  assert.notEqual(maliciousCid, digestHexOf(context));

  const rogueStore = new MapBackedStore([[cid, malicious]]);
  const { resolveContext } = await import("../src/index.ts");
  expectReject(
    () =>
      resolveContext({
        rootCid: cid,
        store: rogueStore,
        allowed: harness.allowed,
      }),
    "CONTENT_DIGEST_MISMATCH",
  );
});

/** Test double presenting attacker bytes under a victim content id. */
class MapBackedStore {
  readonly #map = new Map<string, ContextObjectLike>();

  constructor(entries: ReadonlyArray<readonly [string, ContextObjectLike]>) {
    for (const [cid, object] of entries) this.#map.set(cid, object);
  }

  has(cid: string): boolean {
    return this.#map.has(cid);
  }

  get(cid: string): ContextObjectLike | undefined {
    return this.#map.get(cid);
  }

  canonicalBytes(cid: string): Uint8Array | undefined {
    const object = this.#map.get(cid);
    return object === undefined ? undefined : new TextEncoder().encode(canonicalJson(object));
  }

  ids(): readonly string[] {
    return [...this.#map.keys()];
  }
}

interface ContextObjectLike {
  readonly [key: string]: unknown;
}

test("URL-looking import strings cannot substitute for content ids", async () => {
  const base = { "@vocab": "https://a.test/#" };
  const importer = {
    "@import": "https://attacker.test/context.jsonld",
  };
  const harness = await buildHarness([base, importer]);
  const { resolveContext } = await import("../src/index.ts");
  expectReject(
    () =>
      resolveContext({
        rootCid: contentIdOf(importer),
        store: harness.store,
        allowed: harness.allowed,
      }),
    "CONTENT_NOT_IN_TRUST_LIST",
  );
});

test("diagnostics never carry undefined fields", async () => {
  const harness = await buildHarness([{ "@vocab": "https://a.test/#" }]);
  const unlistedCid = harness.ingest({ "@vocab": "https://evil.test/#" });
  const { resolveContext, safeDiagnosticJson } = await import("../src/index.ts");
  try {
    resolveContext({
      rootCid: unlistedCid,
      store: harness.store,
      allowed: harness.allowed,
    });
    assert.fail("expected rejection");
  } catch (error) {
    assert.ok(error instanceof ContextResolutionError);
    const text = safeDiagnosticJson(error.diagnostic);
    assert.doesNotMatch(text, /undefined/);
    const parsed = JSON.parse(text) as Record<string, unknown>;
    for (const key of [
      "code",
      "message",
      "offendingTerm",
      "offendingCid",
      "importPath",
      "depth",
      "detail",
    ]) {
      assert.notEqual(parsed[key], undefined, `field ${key} must be present`);
    }
    assert.deepEqual(parsed.importPath, []);
  }
});

test("re-signed manifest extends the allow-list under the same trust anchor", async () => {
  const harness = await buildHarness([{ "@vocab": "https://a.test/#" }]);
  const added = harness.ingest({ "@base": "https://b.test/" });
  const resigned = await harness.resign();
  assert.ok(resigned.allowed.has(added));
  const verified = await verifyManifest(resigned.envelope);
  assert.equal(verified.allowed.size, 2);
});

test("manifest with duplicate content ids is malformed", async () => {
  const keyPair = await generateRootKeyPair();
  const spki = await exportPublicKeySpki(keyPair.publicKey);
  const cid = `sha256:${"0a".repeat(32)}`;
  const manifest = buildManifest(
    "dup",
    spki,
    [
      { cid, label: "one" },
      { cid, label: "two" },
    ],
    "2026-11-26T00:00:00Z",
  );
  const envelope = await signManifest(manifest, keyPair.privateKey);
  await expectRejectAsync(() => verifyManifest(envelope), "TRUST_MANIFEST_MALFORMED");
});
