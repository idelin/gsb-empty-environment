import assert from "node:assert/strict";
import test from "node:test";
import {
  AuthenticatedAssetHost,
  PipelineError,
  RevocationSet,
  VerificationCache,
  identityCacheKeyFor,
  intermediateCacheKey,
  revocationRefs,
  shardDigest,
} from "../src/index.ts";
import { createManifest, encodeDeterministic } from "../src/index.ts";
import { fixture, text, utf8 } from "./helpers.ts";

test("same-identity cache hit issues a fresh credential", async () => {
  const { manifest, transport } = fixture({ decoded: text("cached") });
  const host = new AuthenticatedAssetHost({
    cache: new VerificationCache(),
    revocations: new RevocationSet(),
  });
  const request = {
    manifest,
    identity: { requestId: "r1", body: "body", responseType: "text" },
    shards: [[0, transport] as const],
  };
  const first = await host.publish(request);
  let callbackReads = 0;
  const second = await host.publish({
    ...request,
    onRead: (credential) => {
      callbackReads += 1;
      assert.equal(utf8(credential.read()), "cached");
    },
  });
  assert.equal(first.cacheHit, false);
  assert.equal(second.cacheHit, true);
  assert.equal(callbackReads, 1);
  assert.equal(utf8(second.bytes), "cached");
  assert.notEqual(first.credential, second.credential);
});

test("cache hits recheck the current revocation set", async () => {
  const decoded = text("revoked cache");
  const { manifest, transport } = fixture({ decoded });
  const cache = new VerificationCache();
  const revocations = new RevocationSet();
  const host = new AuthenticatedAssetHost({ cache, revocations });
  const request = {
    manifest,
    identity: { requestId: "r1", body: "body", responseType: "text" },
    shards: [[0, transport] as const],
  };
  const first = await host.publish(request);
  assert.equal(first.cacheHit, false);

  revocations.revokeShard(0, shardDigest(transport));
  await assert.rejects(
    host.publish(request),
    (error: unknown) =>
      error instanceof PipelineError && error.code === "REVOKED",
  );
});

test("shared shard revocation invalidates every cached identity", () => {
  const bytes = text("shared");
  const { manifest } = fixture({ decoded: bytes });
  const digest = shardDigest(bytes);
  const cache = new VerificationCache();
  const revocations = new RevocationSet();
  const refs = revocationRefs({
    manifest,
    contentCommitment: "sha256:shared",
    shardDigests: [{ index: 0, digest }],
  });
  const base = {
    bytes,
    contentCommitment: "sha256:shared",
    transformChain: manifest.transformChain,
    requestBodyDigest: "sha256:body",
    responseType: "text",
    placeholderBinding: "none",
    manifestFingerprint: "fingerprint",
    revocationRefs: refs,
    verifiedAt: Date.now(),
    decodedStage: "raw" as const,
  };
  const keyA = identityCacheKeyFor(manifest, { requestId: "A" }, "sha256:shared");
  const keyB = identityCacheKeyFor(manifest, { requestId: "B" }, "sha256:shared");
  cache.put({ ...base, key: keyA });
  cache.put({ ...base, key: keyB });

  assert.ok(cache.get(keyA, revocations));
  assert.ok(cache.get(keyB, revocations));
  revocations.revokeShard(0, digest);
  assert.equal(cache.get(keyA, revocations), null);
  assert.equal(cache.get(keyB, revocations), null);
});

test("cross-identity reuse only applies to uninstantiated intermediate data", () => {
  const cache = new VerificationCache();
  const key = intermediateCacheKey({
    transformChain: [{ id: "rle", version: 1 }],
    contentCommitment: "sha256:content",
  });
  cache.putIntermediate({
    key,
    bytes: text("intermediate"),
    contentCommitment: "sha256:content",
    transformChain: [{ id: "rle", version: 1 }],
    revocationRefs: ["content:sha256:content"],
    createdAt: Date.now(),
    instantiated: false,
  });

  const first = cache.acquireIntermediate({ key, identity: "A" });
  assert.ok(first);
  assert.equal(utf8(first), "intermediate");
  assert.equal(
    cache.acquireIntermediate({ key, identity: "B" }),
    null,
  );
  assert.ok(cache.acquireIntermediate({ key, identity: "A" }));
});

test("cache keys bind transport instance, transform chain, body, and response type", () => {
  const raw = text("cache identity");
  const { manifest } = fixture({ decoded: raw });
  const key = (identity: Record<string, unknown>, responseType: string) =>
    identityCacheKeyFor(
      manifest,
      { ...identity, responseType },
      "sha256:content",
    );
  assert.notEqual(
    key({ requestId: "A", body: "body" }, "text"),
    key({ requestId: "B", body: "body" }, "text"),
  );
  assert.notEqual(
    key({ requestId: "A", body: "body" }, "text"),
    key({ requestId: "A", body: "other" }, "text"),
  );
  assert.notEqual(
    key({ requestId: "A", body: "body" }, "text"),
    key({ requestId: "A", body: "body" }, "json"),
  );
  assert.notEqual(
    identityCacheKeyFor(manifest, { requestId: "A" }, "sha256:one"),
    identityCacheKeyFor(manifest, { requestId: "A" }, "sha256:two"),
  );
});

test("published credentials also honor the current revocation set", async () => {
  const { manifest, transport } = fixture({ decoded: text("credential") });
  const cache = new VerificationCache();
  const revocations = new RevocationSet();
  const host = new AuthenticatedAssetHost({ cache, revocations });
  const asset = await host.publish({
    manifest,
    identity: { requestId: "r1" },
    shards: [[0, transport] as const],
  });
  assert.equal(utf8(asset.credential.read()), "credential");
  revocations.revokeShard(0, shardDigest(transport));
  assert.throws(
    () => asset.credential.read(),
    (error: unknown) =>
      error instanceof PipelineError && error.code === "REVOKED",
  );
});

test("the same transport bytes with different instances never share an entry", async () => {
  const decoded = text("same wire, different transform");
  const transport = encodeDeterministic(decoded);
  const identity = { requestId: "r1", body: "body", responseType: "text" };
  const identityManifest = createManifest({
    transportBytes: transport,
    decodedBytes: transport,
    transport: { domain: "transport.v1" },
    decoded: { domain: "decoded.v1" },
    transform: ["identity"],
  });
  const rleManifest = createManifest({
    transportBytes: transport,
    decodedBytes: decoded,
    transport: { domain: "transport.v1" },
    decoded: { domain: "decoded.v1" },
    transform: ["rle"],
  });
  const host = new AuthenticatedAssetHost({
    cache: new VerificationCache(),
    revocations: new RevocationSet(),
  });

  const identityAsset = await host.publish({
    manifest: identityManifest,
    identity,
    shards: [[0, transport] as const],
  });
  const rleAsset = await host.publish({
    manifest: rleManifest,
    identity,
    shards: [[0, transport] as const],
  });
  assert.equal(identityAsset.cacheHit, false);
  assert.equal(rleAsset.cacheHit, false);
  assert.equal(utf8(identityAsset.bytes), utf8(transport));
  assert.equal(utf8(rleAsset.bytes), utf8(decoded));
});
