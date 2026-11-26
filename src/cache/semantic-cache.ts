/**
 * Semantic cache with soundness-enforced keys.
 *
 * A cache key is a digest over a descriptor that explicitly binds:
 *   1. the signed trust-manifest root (manifest cid + root key id),
 *   2. the full ordered context stack (every frame cid),
 *   3. the complete protected-term mapping,
 *   4. the active `@vocab`,
 *   5. the active `@base`.
 *
 * URL-only matching, missing stack frames or missing protected/vocab/base
 * bindings are rejected before any cache lookup. Entries additionally carry
 * the minimal sufficient source closure so a hit can be re-validated.
 */

import { canonicalJson } from "../canonical.ts";
import { contentIdOf } from "../content-address.ts";
import { raise } from "../diagnostic.ts";
import type {
  ContentId,
  ProtectedRegistration,
  ResolutionResult,
} from "../types.ts";

export interface TrustRootBinding {
  /** Content id of the canonical signed manifest. */
  readonly manifestCid: ContentId;
  readonly rootKeyId: string;
  readonly rootPublicKeySpki: string;
}

/**
 * Explicit, fully-populated key descriptor. Every field is required and empty
 * values are represented explicitly (`null` / empty arrays) so omission is
 * distinguishable from presence.
 */
export interface SemanticCacheKeyDescriptor {
  readonly version: 1;
  readonly trustRoot: TrustRootBinding;
  readonly contextStack: readonly ContentId[];
  readonly protectedMapping: readonly ProtectedMappingEntry[];
  readonly vocab: string | null;
  readonly base: string | null;
}

export interface ProtectedMappingEntry {
  readonly term: string;
  readonly iri: string;
  readonly reverse: boolean;
  readonly forcedType: string | null;
  readonly container: readonly string[];
  readonly definingCid: ContentId;
  readonly fingerprint: string;
}

export interface CacheEntry {
  readonly key: string;
  readonly descriptor: SemanticCacheKeyDescriptor;
  readonly resolution: ResolutionResult;
  /** Minimal sufficient source closure required to reproduce the entry. */
  readonly closure: readonly ContentId[];
  readonly manifestCid: ContentId;
}

export function buildDescriptor(
  trustRoot: TrustRootBinding,
  resolution: ResolutionResult,
): SemanticCacheKeyDescriptor {
  const protectedMapping = [...resolution.protectedTerms.values()]
    .map((entry: ProtectedRegistration): ProtectedMappingEntry => ({
      term: entry.term,
      iri: entry.iri,
      reverse: entry.reverse,
      forcedType: entry.forcedType,
      container: [...entry.container],
      definingCid: entry.definingCid,
      fingerprint: entry.definitionFingerprint,
    }))
    .sort((a, b) => a.term.localeCompare(b.term));

  return {
    version: 1,
    trustRoot,
    contextStack: resolution.stack.map((frame) => frame.cid),
    protectedMapping,
    vocab: resolution.vocab,
    base: resolution.base,
  };
}

/** Compute the semantic cache key from a validated descriptor. */
export function computeCacheKey(descriptor: SemanticCacheKeyDescriptor): string {
  validateDescriptor(descriptor);
  return contentIdOf(descriptor);
}

/**
 * Independent soundness check. This deliberately does not trust callers:
 * it rejects descriptors that rely on URLs, omit stack frames, drop protected
 * mappings or fail to bind vocab/base.
 *
 * `expectedStackDepth` lets a host pin the resolution depth it independently
 * observed; a descriptor that silently drops context layers is rejected.
 */
export function validateDescriptor(
  descriptor: SemanticCacheKeyDescriptor,
  expectedStackDepth?: number,
): void {
  if (descriptor === null || typeof descriptor !== "object") {
    raise({
      code: "CACHE_KEY_UNSOUND",
      message: "Cache descriptor is not an object",
    });
  }
  if (descriptor.version !== 1) {
    raise({
      code: "CACHE_KEY_UNSOUND",
      message: "Cache descriptor uses an unsupported version",
      detail: String(descriptor.version),
    });
  }
  validateTrustRoot(descriptor.trustRoot);

  if (!Array.isArray(descriptor.contextStack) || descriptor.contextStack.length === 0) {
    raise({
      code: "CACHE_KEY_UNSOUND",
      message: "Cache key must bind the full ordered context stack",
    });
  }
  descriptor.contextStack.forEach((cid, index) => {
    if (!isContentId(cid)) {
      raise({
        code: "CACHE_KEY_UNSOUND",
        message: "Context stack entry is not a content id; URL reuse is forbidden",
        detail: `index=${index}; value=${String(cid)}`,
      });
    }
  });

  if (new Set(descriptor.contextStack).size !== descriptor.contextStack.length) {
    raise({
      code: "CACHE_KEY_UNSOUND",
      message: "Context stack contains duplicate frames",
    });
  }
  if (
    expectedStackDepth !== undefined &&
    descriptor.contextStack.length !== expectedStackDepth
  ) {
    raise({
      code: "CACHE_KEY_UNSOUND",
      message: "Cache descriptor omits one or more context stack layers",
      detail: `expected=${expectedStackDepth}; actual=${descriptor.contextStack.length}`,
    });
  }

  if (!Array.isArray(descriptor.protectedMapping)) {
    raise({
      code: "CACHE_KEY_UNSOUND",
      message: "Cache key must bind the complete @protected mapping",
    });
  }
  for (const entry of descriptor.protectedMapping) {
    if (
      entry === null ||
      typeof entry !== "object" ||
      typeof entry.term !== "string" ||
      typeof entry.iri !== "string" ||
      typeof entry.fingerprint !== "string" ||
      !isContentId(entry.definingCid)
    ) {
      raise({
        code: "CACHE_KEY_UNSOUND",
        message: "Protected mapping entry is incomplete",
        detail: canonicalJson(entry),
      });
    }
  }

  if (!("vocab" in descriptor) || !("base" in descriptor)) {
    raise({
      code: "CACHE_KEY_UNSOUND",
      message: "Cache key must explicitly bind @vocab and @base",
    });
  }
  if (
    (descriptor.vocab !== null && typeof descriptor.vocab !== "string") ||
    (descriptor.base !== null && typeof descriptor.base !== "string")
  ) {
    raise({
      code: "CACHE_KEY_UNSOUND",
      message: "@vocab and @base must be strings or explicit null",
    });
  }
}

function validateTrustRoot(trustRoot: TrustRootBinding): void {
  if (trustRoot === null || typeof trustRoot !== "object") {
    raise({
      code: "CACHE_KEY_UNSOUND",
      message: "Cache key must bind a signed trust-manifest root",
    });
  }
  if (
    !isContentId(trustRoot.manifestCid) ||
    typeof trustRoot.rootKeyId !== "string" ||
    trustRoot.rootKeyId.length === 0 ||
    typeof trustRoot.rootPublicKeySpki !== "string" ||
    trustRoot.rootPublicKeySpki.length === 0
  ) {
    raise({
      code: "CACHE_KEY_UNSOUND",
      message: "Trust-root binding is incomplete",
      detail: canonicalJson(trustRoot),
    });
  }
}

function isContentId(value: unknown): value is ContentId {
  return (
    typeof value === "string" &&
    /^sha256:[0-9a-f]{64}$/.test(value)
  );
}

/** In-memory semantic cache; all reuse decisions flow through `lookup`. */
export class SemanticCache {
  readonly #entries = new Map<string, CacheEntry>();

  put(entry: CacheEntry): void {
    validateDescriptor(entry.descriptor);
    if (entry.key !== computeCacheKey(entry.descriptor)) {
      raise({
        code: "CACHE_KEY_UNSOUND",
        message: "Cache entry key disagrees with its descriptor",
      });
    }
    assertClosureSufficient(entry, entry.descriptor);
    this.#entries.set(entry.key, entry);
  }

  /**
   * Reuse is permitted only when the full descriptor digests equally. A URL,
   * a partial stack or a stale protected mapping yields a different key and
   * therefore a hard cache miss, never a silent hit.
   */
  lookup(descriptor: SemanticCacheKeyDescriptor): CacheEntry | null {
    const key = computeCacheKey(descriptor);
    const hit = this.#entries.get(key);
    if (hit === undefined) return null;
    assertClosureSufficient(hit, descriptor);
    return hit;
  }

  size(): number {
    return this.#entries.size;
  }

  keys(): readonly string[] {
    return [...this.#entries.keys()];
  }
}

/**
 * Closure sufficiency: every frame required by the requesting descriptor must
 * be contained in the stored entry's source closure.
 */
export function assertClosureSufficient(
  entry: CacheEntry,
  descriptor: SemanticCacheKeyDescriptor,
): void {
  const closure = new Set(entry.closure);
  const missing = descriptor.contextStack.filter((cid) => !closure.has(cid));
  if (missing.length > 0) {
    raise({
      code: "CACHE_CLOSURE_INSUFFICIENT",
      message: "Cached entry lacks the minimal sufficient source closure",
      detail: missing.join(","),
    });
  }
  for (const protectedEntry of descriptor.protectedMapping) {
    if (!closure.has(protectedEntry.definingCid)) {
      raise({
        code: "CACHE_CLOSURE_INSUFFICIENT",
        message: "Protected binding source is missing from cache closure",
        detail: protectedEntry.term,
      });
    }
  }
}

/**
 * Compute the minimal sufficient source closure for a resolution: the union
 * of the context stack and every protected binding's defining context.
 */
export function minimalClosure(
  descriptor: SemanticCacheKeyDescriptor,
): readonly ContentId[] {
  const closure = new Set<ContentId>(descriptor.contextStack);
  for (const entry of descriptor.protectedMapping) {
    closure.add(entry.definingCid);
  }
  return [...closure].sort();
}

/**
 * Explicit reuse guard used by host code that only has raw request fields.
 * Mirrors the lookup rule and returns a diagnostic-bearing boolean API.
 */
export function canReuse(
  request: SemanticCacheKeyDescriptor,
  candidate: CacheEntry,
): { readonly allowed: boolean; readonly reason: string } {
  try {
    validateDescriptor(request);
    const requestKey = computeCacheKey(request);
    if (requestKey !== candidate.key) {
      return { allowed: false, reason: "CACHE_REUSE_DENIED: semantic descriptors differ" };
    }
    assertClosureSufficient(candidate, request);
    return { allowed: true, reason: "" };
  } catch (error) {
    const reason =
      error instanceof Error && error.message.length > 0
        ? error.message
        : "CACHE_REUSE_DENIED: unsound request";
    return { allowed: false, reason };
  }
}
