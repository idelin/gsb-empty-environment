/**
 * Lightweight type-structure probe for environments without a TypeScript
 * compiler. Node's type stripping guarantees the syntax is valid; this probe
 * additionally imports every public and acceptance module, instantiates the
 * key classes and asserts the diagnostic/descriptor contracts at runtime.
 */

import {
  ContextResolutionError,
  InMemoryContentStore,
  SemanticCache,
  assertDiagnosticComplete,
  buildDescriptor,
  canonicalJson,
  computeCacheKey,
  contentIdOf,
  generateRootKeyPair,
  minimalClosure,
  resolveContext,
  signManifest,
  buildManifest,
  verifyManifest,
  exportPublicKeySpki,
} from "../src/index.ts";

const store = new InMemoryContentStore();
const context = {
  "@vocab": "https://probe.test/#",
  protected: { "@id": "https://probe.test/#p", "@protected": true },
};
const cid = store.ingest(context);
const keyPair = await generateRootKeyPair();
const spki = await exportPublicKeySpki(keyPair.publicKey);
const manifest = buildManifest("probe", spki, [{ cid }], "2026-11-26T00:00:00Z");
const envelope = await signManifest(manifest, keyPair.privateKey);
const verified = await verifyManifest(envelope);
const resolution = resolveContext({
  rootCid: cid,
  store,
  allowed: verified.allowed,
});
const descriptor = buildDescriptor(
  {
    manifestCid: contentIdOf(manifest),
    rootKeyId: "probe",
    rootPublicKeySpki: spki,
  },
  resolution,
);
const cacheKey = computeCacheKey(descriptor);
const cache = new SemanticCache();
cache.put({
  key: cacheKey,
  descriptor,
  resolution,
  closure: minimalClosure(descriptor),
  manifestCid: contentIdOf(manifest),
});
if (cache.lookup(descriptor) === null) throw new Error("probe cache miss");

try {
  resolveContext({
    rootCid: "sha256:0000000000000000000000000000000000000000000000000000000000000000",
    store,
    allowed: verified.allowed,
  });
  throw new Error("probe expected a rejection");
} catch (error) {
  if (!(error instanceof ContextResolutionError)) throw error;
  assertDiagnosticComplete(error.diagnostic);
  JSON.parse(canonicalJson(error.diagnostic));
}

console.log("typecheck-probe: ok");
