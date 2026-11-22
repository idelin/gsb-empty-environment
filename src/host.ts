import { ErrorCode, PipelineError } from "./errors.ts";
import {
  normalizeManifest,
  contentCommitmentFromManifest,
  type AssetManifest,
} from "./manifest.ts";
import {
  placeholderIdentityDigest,
  normalizeRequestIdentity,
  type RequestIdentity,
} from "./placeholders.ts";
import {
  AuthenticatedPipeline,
  cacheTransformChainFor,
  type AuthenticatedPipelineOptions,
  type PublishedAsset,
} from "./pipeline.ts";
import {
  VerificationCache,
  asRevocationSet,
  identityCacheKeyFor,
  RevocationSet,
  type VerifiedCacheRecord,
} from "./cache.ts";
import {
  PublicationGate,
  createVerificationProof,
  isPublicationCredential,
  type PublicationCredential,
  type ReadCallback,
} from "./publication.ts";
import {
  verifyRepresentation,
  type RepresentationVerification,
} from "./verify.ts";
import type { ShardInput } from "./shards.ts";

export interface AssetHostOptions {
  readonly cache?: VerificationCache;
  readonly revocations?: RevocationSet | Iterable<string>;
  readonly instanceId?: string;
  readonly pipeline?: Partial<AuthenticatedPipelineOptions>;
}

export interface PublishRequest {
  readonly manifest: AssetManifest | unknown;
  readonly identity?: RequestIdentity | string | number;
  readonly requestBody?: string | Uint8Array;
  readonly responseType?: string;
  readonly shards?:
    | Iterable<ShardInput | readonly [number, Uint8Array] | Uint8Array>
    | AsyncIterable<ShardInput | readonly [number, Uint8Array] | Uint8Array>;
  readonly signal?: AbortSignal;
  readonly onRead?: ReadCallback;
  readonly useCache?: boolean;
  readonly cache?: VerificationCache;
  readonly revocations?: RevocationSet | Iterable<string>;
  readonly instanceId?: string;
  readonly pipeline?: Partial<AuthenticatedPipelineOptions>;
}

function withRequestFields(
  identity: RequestIdentity,
  requestBody: string | Uint8Array | undefined,
  responseType: string | undefined,
): RequestIdentity {
  return {
    ...identity,
    ...(requestBody === undefined ? {} : { body: requestBody }),
    ...(responseType === undefined ? {} : { responseType }),
  };
}

/**
 * Host facade: combines the pipeline, verification cache, and current
 * revocation set.  A cache hit still rechecks revocation before a fresh
 * credential is issued.
 */
export class AuthenticatedAssetHost {
  readonly cache?: VerificationCache;
  readonly revocations: RevocationSet;
  readonly instanceId?: string;
  readonly defaults: Partial<AuthenticatedPipelineOptions>;

  constructor(options: AssetHostOptions = {}) {
    this.cache = options.cache;
    this.revocations = asRevocationSet(options.revocations) ?? new RevocationSet();
    this.instanceId = options.instanceId;
    this.defaults = options.pipeline ?? {};
  }

  createPipeline(
    manifest: AssetManifest | unknown,
    options: AuthenticatedPipelineOptions = {},
  ): AuthenticatedPipeline {
    return new AuthenticatedPipeline(manifest, {
      ...this.defaults,
      cache: this.cache,
      revocations: this.revocations,
      instanceId: this.instanceId,
      ...options,
    });
  }

  async publish(request: PublishRequest): Promise<PublishedAsset>;
  async publish(
    manifest: AssetManifest | unknown,
    identity: RequestIdentity | string | number,
    shards?: PublishRequest["shards"],
    options?: Omit<PublishRequest, "manifest" | "identity" | "shards">,
  ): Promise<PublishedAsset>;
  async publish(
    requestOrManifest: PublishRequest | AssetManifest | unknown,
    positionalIdentity?: RequestIdentity | string | number,
    positionalShards?: PublishRequest["shards"],
    positionalOptions?: Omit<PublishRequest, "manifest" | "identity" | "shards">,
  ): Promise<PublishedAsset> {
    const request: PublishRequest =
      positionalIdentity !== undefined &&
      (requestOrManifest === null ||
        typeof requestOrManifest !== "object" ||
        !("manifest" in (requestOrManifest as Record<string, unknown>)))
        ? {
            ...(positionalOptions ?? {}),
            manifest: requestOrManifest,
            identity: positionalIdentity,
            shards: positionalShards,
          }
        : (requestOrManifest as PublishRequest);

    if (request.signal?.aborted === true) {
      throw new PipelineError(
        ErrorCode.CANCELLED,
        "asset publication was cancelled",
        { reason: request.signal.reason },
      );
    }

    const manifest = normalizeManifest(request.manifest);
    const identity = withRequestFields(
      normalizeRequestIdentity(request.identity),
      request.requestBody,
      request.responseType,
    );
    const cache = request.cache ?? this.cache;
    const revocations =
      asRevocationSet(request.revocations) ?? this.revocations;
    const instanceId = request.instanceId ?? this.instanceId;
    const contentCommitment = contentCommitmentFromManifest(manifest);
    const transformChain = cacheTransformChainFor(
      manifest,
      { ...this.defaults, ...(request.pipeline ?? {}) },
    );
    const placeholderBinding = placeholderIdentityDigest(
      identity,
      manifest.placeholders,
    );

    if (cache !== undefined && request.useCache !== false) {
      const key = identityCacheKeyFor(manifest, identity, contentCommitment, {
        instanceId,
        placeholderBinding,
        transformChain,
      });
      const record = cache.get(key, revocations);
      if (record !== null) {
        const cached = this.#publishCached(
          manifest,
          identity,
          record,
          revocations,
          request.onRead,
        );
        if (cached !== null) {
          return cached;
        }
      }
    }

    const pipeline = this.createPipeline(manifest, {
      ...(request.pipeline ?? {}),
      identity,
      cache,
      revocations,
      instanceId,
      signal: request.signal,
    });
    if (request.onRead !== undefined) {
      pipeline.onRead(request.onRead);
    }
    if (request.shards !== undefined) {
      for await (const shard of request.shards) {
        pipeline.addShard(shard);
      }
    }
    return pipeline.end();
  }

  async publishCredential(
    request: PublishRequest,
  ): Promise<PublicationCredential> {
    return (await this.publish(request)).credential;
  }

  #publishCached(
    manifest: AssetManifest,
    identity: RequestIdentity,
    record: VerifiedCacheRecord,
    revocations: RevocationSet,
    onRead?: ReadCallback,
  ): PublishedAsset | null {
    // get() already checked revocation; this is a second guard against a
    // concurrent revocation between lookup and publication.
    if (revocations.anyRevoked(record.revocationRefs)) {
      this.cache?.delete(record.key);
      return null;
    }

    const meta = (record.metadata ?? {}) as Record<string, unknown>;
    const transportVerification = meta.transportVerification as
      | RepresentationVerification
      | undefined;
    if (transportVerification === undefined) {
      this.cache?.delete(record.key);
      return null;
    }
    let decodedVerification: RepresentationVerification;
    const cachedDecoded = meta.decodedVerification as
      | RepresentationVerification
      | undefined;
    if (cachedDecoded !== undefined) {
      decodedVerification = cachedDecoded;
    } else {
      const candidate = verifyRepresentation(manifest.decoded, record.bytes, {
        kind: "decoded",
      });
      if (!candidate.ok) {
        // Raw-stage cached bytes are post-replacement and therefore cannot be
        // checked against the raw digest.  Without the original verification
        // metadata we must treat the entry as a miss rather than trust it.
        this.cache?.delete(record.key);
        return null;
      }
      decodedVerification = candidate;
    }
    let finalVerification =
      (meta.finalVerification as RepresentationVerification | undefined) ??
      undefined;
    if (manifest.final !== undefined && finalVerification === undefined) {
      const candidate = verifyRepresentation(manifest.final, record.bytes, {
        kind: "final",
      });
      if (!candidate.ok) {
        this.cache?.delete(record.key);
        return null;
      }
      finalVerification = candidate;
    }

    const proof = createVerificationProof({
      manifest,
      bytes: record.bytes,
      transport: transportVerification,
      decoded: decodedVerification,
      ...(finalVerification === undefined ? {} : { final: finalVerification }),
      decodedStage: record.decodedStage,
      contentCommitment: record.contentCommitment,
      transportDigest:
        typeof meta.transportDigest === "string"
          ? meta.transportDigest
          : "",
      decodedDigest:
        typeof meta.decodedDigest === "string" ? meta.decodedDigest : "",
      revocationRefs: record.revocationRefs,
      placeholderBinding: record.placeholderBinding,
      responseType: record.responseType,
      requestId: identity.requestId ?? identity.id ?? null,
    });

    const gate = new PublicationGate(manifest, { revocations });
    if (onRead !== undefined) {
      gate.onRead(onRead);
    }
    const credential = gate.commit(proof, record.bytes);
    const replacements =
      (meta.replacements as PublishedAsset["replacements"] | undefined) ?? [];
    return Object.freeze({
      credential,
      bytes: credential.read(),
      byteLength: record.bytes.byteLength,
      digest: credential.digest,
      read: (offset = 0, length?: number) => credential.read(offset, length),
      manifest,
      identity,
      transportDigest: proof.transportDigest,
      decodedDigest: proof.decodedDigest,
      contentCommitment: record.contentCommitment,
      decodedStage: record.decodedStage,
      transportVerification,
      decodedVerification,
      ...(finalVerification === undefined ? {} : { finalVerification }),
      replacements,
      cacheHit: true,
    });
  }

  read(
    credential: PublicationCredential,
    offset = 0,
    length?: number,
  ): Uint8Array {
    if (!isPublicationCredential(credential)) {
      throw new PipelineError(
        ErrorCode.CREDENTIAL_INVALID,
        "expected a publication credential",
      );
    }
    return credential.read(offset, length);
  }

  revoke(key: string): void {
    this.revocations.revoke(key);
  }

  revokeDigest(algorithm: string, digest: string): void {
    this.revocations.revokeDigest(algorithm, digest);
  }

  revokeShard(index: number, digest?: string): void {
    this.revocations.revokeShard(index, digest);
  }

  revokeContent(commitment: string): void {
    this.revocations.revokeContent(commitment);
  }
}

export const AssetHost = AuthenticatedAssetHost;

export function createHost(options: AssetHostOptions = {}): AuthenticatedAssetHost {
  return new AuthenticatedAssetHost(options);
}
