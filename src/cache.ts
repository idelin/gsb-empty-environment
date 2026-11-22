import { canonicalJson, cloneBytes } from "./canonical.ts";
import { ErrorCode, PipelineError } from "./errors.ts";
import {
  canonicalAlgorithm,
  digestHex,
  normalizeDigestValue,
  type AlgorithmName,
} from "./hash.ts";
import type { AssetManifest, DigestGroup } from "./manifest.ts";
import { manifestFingerprint } from "./manifest.ts";
import type { RequestIdentity } from "./placeholders.ts";
import {
  readIdentityField,
  requestBodyDigest,
  requestResponseType,
} from "./placeholders.ts";
import type { TransformStep } from "./manifest.ts";

function addGroupRefs(
  refs: Set<string>,
  kind: string,
  groups: readonly DigestGroup[],
): void {
  for (const group of groups) {
    for (const entry of group.entries) {
      refs.add(`digest:${entry.algorithm}:${entry.digest}`);
      refs.add(`${kind}:${entry.algorithm}:${entry.digest}`);
      refs.add(`${kind}-digest:${entry.digest}`);
    }
  }
}

export interface RevocationRefsInput {
  readonly manifest?: AssetManifest;
  readonly contentCommitment?: string;
  readonly shardDigests?: readonly {
    index: number;
    digest: string;
  }[];
  readonly extra?: Iterable<string>;
}

export function revocationRefs(input: RevocationRefsInput): string[] {
  const refs = new Set<string>();
  if (input.manifest !== undefined) {
    addGroupRefs(refs, "transport", input.manifest.transport.groups);
    addGroupRefs(refs, "decoded", input.manifest.decoded.groups);
    if (input.manifest.final !== undefined) {
      addGroupRefs(refs, "final", input.manifest.final.groups);
    }
    refs.add(`manifest:${manifestFingerprint(input.manifest)}`);
  }
  if (input.contentCommitment !== undefined) {
    refs.add(`content:${input.contentCommitment}`);
    if (input.contentCommitment.includes(":")) {
      refs.add(`content:${input.contentCommitment.split(":").slice(1).join(":")}`);
    }
  }
  for (const shard of input.shardDigests ?? []) {
    refs.add(`shard:${shard.index}`);
    refs.add(`shard:${shard.index}:${shard.digest}`);
    refs.add(`shard-digest:${shard.digest}`);
  }
  for (const extra of input.extra ?? []) {
    refs.add(extra);
  }
  return [...refs].sort();
}

/**
 * A small, explicit revocation set.  Verification cache hits always consult
 * the current set rather than trusting the state captured when an entry was
 * originally verified.
 */
export class RevocationSet {
  #keys = new Set<string>();
  #listeners = new Set<(key: string) => void>();

  get size(): number {
    return this.#keys.size;
  }

  revoke(key: string): void {
    if (typeof key !== "string" || key.length === 0) {
      throw new PipelineError(
        ErrorCode.INVALID_ARGUMENT,
        "revocation key must be a non-empty string",
      );
    }
    if (this.#keys.has(key)) {
      return;
    }
    this.#keys.add(key);
    for (const listener of this.#listeners) {
      listener(key);
    }
  }

  add(key: string): void {
    this.revoke(key);
  }

  revokeDigest(algorithm: AlgorithmName, digest: string): void {
    const canonical = canonicalAlgorithm(algorithm);
    if (canonical === null) {
      throw new PipelineError(
        ErrorCode.UNSUPPORTED_ALGORITHM,
        `unsupported revocation algorithm: ${String(algorithm)}`,
        { algorithm },
      );
    }
    const normalized = normalizeDigestValue(digest, canonical);
    this.revoke(`digest:${canonical}:${normalized}`);
    this.revoke(`transport:${canonical}:${normalized}`);
    this.revoke(`decoded:${canonical}:${normalized}`);
    this.revoke(`final:${canonical}:${normalized}`);
  }

  revokeShard(index: number, digest?: string): void {
    if (!Number.isSafeInteger(index) || index < 0) {
      throw new PipelineError(
        ErrorCode.INVALID_ARGUMENT,
        "shard index must be a non-negative safe integer",
        { index },
      );
    }
    this.revoke(`shard:${index}`);
    if (digest !== undefined) {
      const normalized = normalizeDigestValue(digest, "sha256");
      this.revoke(`shard:${index}:${normalized}`);
      this.revoke(`shard-digest:${normalized}`);
    }
  }

  revokeContent(commitment: string): void {
    this.revoke(`content:${commitment}`);
    if (commitment.includes(":")) {
      this.revoke(`content:${commitment.split(":").slice(1).join(":")}`);
    }
  }

  has(key: string): boolean {
    return this.#keys.has(key);
  }

  anyRevoked(keys: Iterable<string>): boolean {
    for (const key of keys) {
      if (this.#keys.has(key)) {
        return true;
      }
    }
    return false;
  }

  firstRevoked(keys: Iterable<string>): string | null {
    for (const key of keys) {
      if (this.#keys.has(key)) {
        return key;
      }
    }
    return null;
  }

  assertUsable(keys: Iterable<string>): void {
    const revoked = this.firstRevoked(keys);
    if (revoked !== null) {
      throw new PipelineError(
        ErrorCode.REVOKED,
        "the asset or one of its shared shards has been revoked",
        { key: revoked },
      );
    }
  }

  clear(): void {
    this.#keys.clear();
  }

  values(): string[] {
    return [...this.#keys].sort();
  }

  snapshot(): RevocationSet {
    const copy = new RevocationSet();
    for (const key of this.#keys) {
      copy.revoke(key);
    }
    return copy;
  }

  onRevoke(listener: (key: string) => void): () => void {
    this.#listeners.add(listener);
    return () => {
      this.#listeners.delete(listener);
    };
  }
}

export const RevocationRegistry = RevocationSet;

export function asRevocationSet(
  value: RevocationSet | Iterable<string> | undefined,
): RevocationSet | undefined {
  if (value === undefined || value instanceof RevocationSet) {
    return value;
  }
  const set = new RevocationSet();
  if (typeof value === "string") {
    set.revoke(value);
    return set;
  }
  for (const key of value) {
    set.revoke(key);
  }
  return set;
}

export interface CacheIdentity {
  readonly transformChain: readonly TransformStep[];
  readonly contentCommitment: string;
  readonly requestBody: string | Uint8Array | ArrayBuffer;
  readonly responseType: string;
  readonly request?: RequestIdentity;
  readonly placeholderBinding?: string;
  readonly manifestFingerprint?: string;
  readonly instanceId?: string;
}

export function createCacheKey(identity: CacheIdentity): string {
  const bodyDigest = requestBodyDigest({
    ...(identity.request ?? {}),
    body: identity.requestBody,
  });
  const normalizedBody =
    typeof identity.requestBody === "string"
      ? { text: identity.requestBody, digest: bodyDigest }
      : { digest: bodyDigest };
  return digestHex(
    "sha256",
    "gsb.cache.identity.v1",
    canonicalJson({
      transformChain: identity.transformChain,
      contentCommitment: identity.contentCommitment,
      requestBody: normalizedBody,
      responseType: identity.responseType,
      requestId: identity.request
        ? (readIdentityField(identity.request, "requestId") ??
          readIdentityField(identity.request, "id") ??
          readIdentityField(identity.request, "request.id") ??
          null)
        : null,
      placeholderBinding: identity.placeholderBinding ?? null,
      manifestFingerprint: identity.manifestFingerprint ?? null,
      instanceId: identity.instanceId ?? null,
    }),
  );
}

export function intermediateCacheKey(input: {
  transformChain: readonly TransformStep[];
  contentCommitment: string;
  manifestFingerprint?: string;
}): string {
  return digestHex(
    "sha256",
    "gsb.cache.intermediate.v1",
    canonicalJson({
      transformChain: input.transformChain,
      contentCommitment: input.contentCommitment,
      manifestFingerprint: input.manifestFingerprint ?? null,
    }),
  );
}

export interface VerifiedCacheRecord {
  readonly key: string;
  readonly bytes: Uint8Array;
  readonly contentCommitment: string;
  readonly transformChain: readonly TransformStep[];
  readonly requestBodyDigest: string;
  readonly responseType: string;
  readonly placeholderBinding: string;
  readonly manifestFingerprint: string;
  readonly revocationRefs: readonly string[];
  readonly verifiedAt: number;
  readonly decodedStage: "raw" | "final";
  readonly metadata?: Readonly<Record<string, unknown>>;
}

export interface IntermediateCacheRecord {
  readonly key: string;
  readonly bytes: Uint8Array;
  readonly contentCommitment: string;
  readonly transformChain: readonly TransformStep[];
  readonly revocationRefs: readonly string[];
  readonly createdAt: number;
  instantiated: boolean;
  instantiatedBy?: string;
}

export interface CacheGetResult {
  readonly record: VerifiedCacheRecord;
  readonly cacheHit: true;
}

export class VerificationCache {
  readonly maxEntries: number;
  readonly revocations?: RevocationSet;

  #verified = new Map<string, VerifiedCacheRecord>();
  #intermediate = new Map<string, IntermediateCacheRecord>();

  constructor(
    options: { maxEntries?: number; revocations?: RevocationSet } = {},
  ) {
    this.maxEntries = options.maxEntries ?? 256;
    this.revocations = options.revocations;
  }

  get size(): number {
    return this.#verified.size;
  }

  get intermediateSize(): number {
    return this.#intermediate.size;
  }

  put(record: VerifiedCacheRecord): void {
    this.#verified.set(record.key, {
      ...record,
      bytes: cloneBytes(record.bytes),
      revocationRefs: [...record.revocationRefs],
    });
    this.#evict();
  }

  /**
   * A cache hit is only valid if every recorded revocation reference is still
   * clear in the *current* revocation set.
   */
  get(
    key: string,
    revocations?: RevocationSet,
  ): VerifiedCacheRecord | null {
    const currentRevocations = revocations ?? this.revocations;
    const record = this.#verified.get(key);
    if (record === undefined) {
      return null;
    }
    if (currentRevocations?.anyRevoked(record.revocationRefs)) {
      this.#verified.delete(key);
      return null;
    }
    return {
      ...record,
      bytes: cloneBytes(record.bytes),
      revocationRefs: [...record.revocationRefs],
    };
  }

  has(key: string, revocations?: RevocationSet): boolean {
    return this.get(key, revocations) !== null;
  }

  delete(key: string): boolean {
    return this.#verified.delete(key);
  }

  clear(): void {
    this.#verified.clear();
    this.#intermediate.clear();
  }

  pruneRevoked(revocations: RevocationSet): number {
    let removed = 0;
    for (const [key, record] of this.#verified) {
      if (revocations.anyRevoked(record.revocationRefs)) {
        this.#verified.delete(key);
        removed += 1;
      }
    }
    for (const [key, record] of this.#intermediate) {
      if (revocations.anyRevoked(record.revocationRefs)) {
        this.#intermediate.delete(key);
        removed += 1;
      }
    }
    return removed;
  }

  putIntermediate(record: IntermediateCacheRecord): void {
    const existing = this.#intermediate.get(record.key);
    if (existing !== undefined && existing.instantiated) {
      return;
    }
    this.#intermediate.set(record.key, {
      ...record,
      bytes: cloneBytes(record.bytes),
      revocationRefs: [...record.revocationRefs],
      instantiated: false,
    });
  }

  peekIntermediate(
    key: string,
    revocations?: RevocationSet,
  ): IntermediateCacheRecord | null {
    revocations = revocations ?? this.revocations;
    const record = this.#intermediate.get(key);
    if (record === undefined) {
      return null;
    }
    if (revocations?.anyRevoked(record.revocationRefs)) {
      this.#intermediate.delete(key);
      return null;
    }
    return {
      ...record,
      bytes: cloneBytes(record.bytes),
    };
  }

  /**
   * Reuse across identities is limited to verified intermediate data that has
   * not yet been instantiated.  Once an identity turns the intermediate into a
   * final object, another identity must not share that object.
   */
  acquireIntermediate(input: {
    key: string;
    identity: string;
    revocations?: RevocationSet;
  }): Uint8Array | null {
    const revocations = input.revocations ?? this.revocations;
    const record = this.#intermediate.get(input.key);
    if (record === undefined) {
      return null;
    }
    if (revocations?.anyRevoked(record.revocationRefs)) {
      this.#intermediate.delete(input.key);
      return null;
    }
    if (
      record.instantiated &&
      record.instantiatedBy !== undefined &&
      record.instantiatedBy !== input.identity
    ) {
      return null;
    }
    record.instantiated = true;
    record.instantiatedBy = input.identity;
    return cloneBytes(record.bytes);
  }

  markIntermediateInstantiated(key: string, identity: string): boolean {
    const record = this.#intermediate.get(key);
    if (record === undefined) {
      return false;
    }
    record.instantiated = true;
    record.instantiatedBy = identity;
    return true;
  }

  #evict(): void {
    while (this.#verified.size > this.maxEntries) {
      const oldest = this.#verified.keys().next().value as string | undefined;
      if (oldest === undefined) {
        break;
      }
      this.#verified.delete(oldest);
    }
  }
}

export function identityCacheKeyFor(
  manifest: AssetManifest,
  identity: RequestIdentity,
  contentCommitment: string,
  options: {
    instanceId?: string;
    placeholderBinding?: string;
    transformChain?: readonly TransformStep[];
    manifestFingerprint?: string;
  } = {},
): string {
  return createCacheKey({
    transformChain: options.transformChain ?? manifest.transformChain,
    contentCommitment,
    requestBody: identity.body ?? "",
    responseType: requestResponseType(identity),
    request: identity,
    placeholderBinding: options.placeholderBinding,
    manifestFingerprint:
      options.manifestFingerprint ?? manifestFingerprint(manifest),
    instanceId: options.instanceId,
  });
}

export function makeCacheKey(identity: CacheIdentity): string {
  return createCacheKey(identity);
}
