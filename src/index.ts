/**
 * Public entry point for the offline credential-host context package.
 * Everything exported here works without network access.
 */

export {
  canonicalBytes,
  canonicalJson,
  base64UrlDecode,
  base64UrlEncode,
  fromHex,
  toHex,
  constantTimeEqualHex,
} from "./canonical.ts";

export {
  InMemoryContentStore,
  contentIdOf,
  digestHexOf,
  digestFromCid,
  verifyStoredDigest,
} from "./content-address.ts";

export {
  assertContextTrusted,
  buildManifest,
  exportPublicKeySpki,
  generateRootKeyPair,
  importRootPublicKey,
  signManifest,
  verifyManifest,
} from "./trust-manifest.ts";
export type { KeyPairMaterial, VerifiedManifest, VerifyOptions } from "./trust-manifest.ts";

export { resolveContext } from "./context-resolver.ts";
export type { ResolveInput } from "./context-resolver.ts";

export { expandSmallGraph } from "./small-graph.ts";
export type { ExpandedNode, ExpandedValue } from "./small-graph.ts";

export {
  assertDiagnosticComplete,
  raise,
  safeDiagnosticJson,
} from "./diagnostic.ts";

export {
  MAX_IMPORT_DEPTH,
  ContextResolutionError,
} from "./types.ts";
export type {
  AllowedContextEntry,
  ContentId,
  ContextDiagnostic,
  ContextObject,
  ContentStore,
  ErrorCode,
  ProtectedRegistration,
  ResolutionResult,
  ResolvedTerm,
  SignedManifestEnvelope,
  TrustManifest,
} from "./types.ts";
export type { CandidateBinding as EngineCandidateBinding } from "./engine-state.ts";

export {
  SemanticCache,
  assertClosureSufficient,
  buildDescriptor,
  canReuse,
  computeCacheKey,
  minimalClosure,
  validateDescriptor,
} from "./cache/semantic-cache.ts";
export type {
  CacheEntry,
  ProtectedMappingEntry,
  SemanticCacheKeyDescriptor,
  TrustRootBinding,
} from "./cache/semantic-cache.ts";
