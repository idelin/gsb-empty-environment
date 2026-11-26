import { test } from "node:test";
import assert from "node:assert/strict";

import {
  SemanticCache,
  buildDescriptor,
  canReuse,
  computeCacheKey,
  contentIdOf,
  minimalClosure,
  resolveContext,
  validateDescriptor,
} from "../src/index.ts";
import { buildHarness, expectReject } from "./helpers.ts";
import type {
  CacheEntry,
  SemanticCacheKeyDescriptor,
} from "../src/index.ts";

async function buildResolved() {
  const leaf = {
    "@protected": true,
    policy: "https://policy.test/#field",
  };
  const root = {
    "@vocab": "https://root.test/#",
    "@base": "https://root.test/base/",
    "@import": contentIdOf(leaf),
    local: "https://root.test/#local",
  };
  const harness = await buildHarness([leaf, root]);
  const resolution = resolveContext({
    rootCid: contentIdOf(root),
    store: harness.store,
    allowed: harness.allowed,
  });
  const trustRoot = {
    manifestCid: harness.manifestCid,
    rootKeyId: harness.rootKeyId,
    rootPublicKeySpki: harness.rootPublicKeySpki,
  };
  return { harness, resolution, trustRoot };
}

test("descriptor binds trust root, stack, protected map, vocab and base", async () => {
  const { resolution, trustRoot } = await buildResolved();
  const descriptor = buildDescriptor(trustRoot, resolution);
  validateDescriptor(descriptor);
  assert.equal(descriptor.trustRoot.manifestCid, trustRoot.manifestCid);
  assert.deepEqual(descriptor.contextStack, resolution.stack.map((f) => f.cid));
  assert.equal(descriptor.protectedMapping.length, 1);
  assert.equal(descriptor.protectedMapping[0]?.term, "policy");
  assert.equal(descriptor.vocab, "https://root.test/#");
  assert.equal(descriptor.base, "https://root.test/base/");
});

test("equal descriptors produce equal keys; changed trust root changes key", async () => {
  const { resolution, trustRoot } = await buildResolved();
  const descriptor = buildDescriptor(trustRoot, resolution);
  const key1 = computeCacheKey(descriptor);
  const key2 = computeCacheKey(buildDescriptor(trustRoot, resolution));
  assert.equal(key1, key2);

  const otherRoot: SemanticCacheKeyDescriptor = {
    ...descriptor,
    trustRoot: { ...trustRoot, rootKeyId: "other-root" },
  };
  assert.notEqual(computeCacheKey(otherRoot), key1);
});

test("dropping a stack frame is unsound and rejected before lookup", async () => {
  const { resolution, trustRoot } = await buildResolved();
  const descriptor = buildDescriptor(trustRoot, resolution);
  assert.ok(descriptor.contextStack.length >= 2);
  const truncated: SemanticCacheKeyDescriptor = {
    ...descriptor,
    contextStack: [descriptor.contextStack[descriptor.contextStack.length - 1] as string],
  };
  expectReject(
    () => validateDescriptor(truncated, descriptor.contextStack.length),
    "CACHE_KEY_UNSOUND",
  );
});

test("URL string instead of content id in the stack is rejected", async () => {
  const { resolution, trustRoot } = await buildResolved();
  const descriptor = buildDescriptor(trustRoot, resolution);
  const urlBased: SemanticCacheKeyDescriptor = {
    ...descriptor,
    contextStack: ["https://attacker.test/context.jsonld"],
  };
  expectReject(() => validateDescriptor(urlBased), "CACHE_KEY_UNSOUND");
});

test("missing vocab/base bindings are rejected", async () => {
  const { resolution, trustRoot } = await buildResolved();
  const descriptor = buildDescriptor(trustRoot, resolution);
  const noVocab = { ...descriptor } as Record<string, unknown>;
  delete noVocab.vocab;
  expectReject(
    () => validateDescriptor(noVocab as SemanticCacheKeyDescriptor),
    "CACHE_KEY_UNSOUND",
  );
});

test("cache hit requires full semantic equality, never URL-only reuse", async () => {
  const { resolution, trustRoot } = await buildResolved();
  const descriptor = buildDescriptor(trustRoot, resolution);
  const cache = new SemanticCache();
  const entry: CacheEntry = {
    key: computeCacheKey(descriptor),
    descriptor,
    resolution,
    closure: minimalClosure(descriptor),
    manifestCid: trustRoot.manifestCid,
  };
  cache.put(entry);
  assert.notEqual(cache.lookup(descriptor), null);

  const changedBase: SemanticCacheKeyDescriptor = {
    ...descriptor,
    base: "https://other.test/base/",
  };
  assert.equal(cache.lookup(changedBase), null);
  const decision = canReuse(changedBase, entry);
  assert.equal(decision.allowed, false);
  assert.match(decision.reason, /CACHE_REUSE_DENIED/);
});

test("insufficient closure is rejected even when the key matches", async () => {
  const { resolution, trustRoot } = await buildResolved();
  const descriptor = buildDescriptor(trustRoot, resolution);
  const entry: CacheEntry = {
    key: computeCacheKey(descriptor),
    descriptor,
    resolution,
    closure: [descriptor.contextStack[0] as string],
    manifestCid: trustRoot.manifestCid,
  };
  expectReject(() => new SemanticCache().put(entry), "CACHE_CLOSURE_INSUFFICIENT");
});

test("minimal closure covers stack plus protected defining contexts", async () => {
  const { resolution, trustRoot } = await buildResolved();
  const descriptor = buildDescriptor(trustRoot, resolution);
  const closure = minimalClosure(descriptor);
  for (const cid of descriptor.contextStack) assert.ok(closure.includes(cid));
  for (const protectedEntry of descriptor.protectedMapping) {
    assert.ok(closure.includes(protectedEntry.definingCid));
  }
});

test("two descriptors differing in protected mapping never share a key", async () => {
  const { resolution, trustRoot } = await buildResolved();
  const descriptor = buildDescriptor(trustRoot, resolution);
  const altered: SemanticCacheKeyDescriptor = {
    ...descriptor,
    protectedMapping: descriptor.protectedMapping.map((entry) => ({
      ...entry,
      fingerprint: `${entry.fingerprint}-mutated`,
    })),
  };
  assert.notEqual(computeCacheKey(altered), computeCacheKey(descriptor));
});
