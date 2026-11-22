import {
  cloneBytes,
  concatBytes,
} from "./canonical.ts";
import {
  ErrorCode,
  PipelineError,
  asPipelineError,
} from "./errors.ts";
import {
  MultiHasher,
  isSupportedAlgorithm,
} from "./hash.ts";
import {
  normalizeManifest,
  manifestFingerprint,
  contentCommitmentFromManifest,
  transportCommitment,
  type AssetManifest,
  type TransformStep,
} from "./manifest.ts";
import {
  assertRepresentation,
  verifyRepresentation,
  type RepresentationVerification,
} from "./verify.ts";
import {
  replacePlaceholders,
  placeholderIdentityDigest,
  normalizeRequestIdentity,
  requestBodyDigest,
  requestResponseType,
  type PlaceholderResult,
  type RequestIdentity,
} from "./placeholders.ts";
import {
  createDecoder,
  decoderChainId,
  type ByteDecoder,
  type DeterministicDecoderOptions,
} from "./decoder.ts";
import {
  ShardAssembler,
  normalizeShard,
  type ShardAddResult,
  type ShardInput,
} from "./shards.ts";
import {
  VerificationCache,
  asRevocationSet,
  intermediateCacheKey,
  identityCacheKeyFor,
  revocationRefs,
  type RevocationSet,
} from "./cache.ts";
import {
  PublicationGate,
  createVerificationProof,
  type PublicationCredential,
} from "./publication.ts";

export type PipelineState =
  | "open"
  | "ending"
  | "published"
  | "failed"
  | "cancelled";

export interface AuthenticatedPipelineOptions {
  readonly identity?: RequestIdentity | string | number;
  readonly decoder?: ByteDecoder;
  readonly decoderId?: string;
  readonly decoderOptions?: DeterministicDecoderOptions;
  /** "stream" feeds contiguous shards through the decoder immediately. */
  readonly decodeMode?: "stream" | "buffered";
  readonly streaming?: boolean;
  /** Pause accepting decoder output after this many staged bytes. */
  readonly highWaterMark?: number;
  readonly maxStagedBytes?: number;
  readonly maxChunkBytes?: number;
  readonly revocations?: RevocationSet | Iterable<string>;
  readonly cache?: VerificationCache;
  readonly instanceId?: string;
  readonly signal?: AbortSignal;
  readonly requireIdentityBinding?: boolean;
  readonly verifyShardDigests?: boolean;
  readonly onCallbackError?: (error: unknown) => void;
}

export function cacheTransformChainFor(
  manifest: AssetManifest,
  options: {
    readonly decoder?: ByteDecoder;
    readonly decoderId?: string;
  } = {},
): readonly TransformStep[] {
  if (options.decoder === undefined && options.decoderId === undefined) {
    return manifest.transformChain;
  }
  const decoderName =
    options.decoderId ??
    (options.decoder as { constructor?: { name?: string } } | undefined)
      ?.constructor?.name ??
    "custom-decoder";
  return [
    ...manifest.transformChain,
    { id: "custom-decoder", version: decoderName },
  ];
}

export interface ShardIngestResult {
  readonly status: "accepted" | "duplicate" | "ignored";
  readonly index: number;
  readonly digest: string;
  readonly drained: ShardAddResult["drained"];
  readonly complete: boolean;
  readonly backpressured: boolean;
}

export interface PublishedAsset {
  readonly credential: PublicationCredential;
  readonly bytes: Uint8Array;
  readonly byteLength: number;
  readonly digest: string;
  read(offset?: number, length?: number): Uint8Array;
  readonly manifest: AssetManifest;
  readonly identity: RequestIdentity;
  readonly transportDigest: string;
  readonly decodedDigest: string;
  readonly contentCommitment: string;
  readonly decodedStage: "raw" | "final";
  readonly transportVerification: RepresentationVerification;
  readonly decodedVerification: RepresentationVerification;
  readonly finalVerification?: RepresentationVerification;
  readonly replacements: PlaceholderResult["replacements"];
  readonly cacheHit: boolean;
}

function representativeDigest(
  verification: RepresentationVerification,
): string {
  for (const group of verification.groups) {
    for (const check of group.checks) {
      if (check.matched) {
        return `${check.algorithm}:${check.actual}`;
      }
    }
  }
  return "";
}

function drainDecoder(
  decoder: ByteDecoder,
  chunks: Uint8Array[],
  maxChunkBytes: number,
  maxStagedBytes: number,
  force: boolean,
  initialStaged = 0,
): { appended: number; backpressured: boolean; staged: number } {
  let appended = 0;
  let backpressured = false;
  let staged = initialStaged;
  while (true) {
    let available = decoder.bufferedOutput;
    if (available === 0) {
      break;
    }
    if (!force) {
      const budget = maxStagedBytes - staged;
      if (budget <= 0) {
        backpressured = true;
        break;
      }
      available = Math.min(available, budget);
    }
    const chunk = decoder.read(Math.min(available, maxChunkBytes));
    if (chunk.byteLength === 0) {
      break;
    }
    chunks.push(chunk);
    appended += chunk.byteLength;
    staged += chunk.byteLength;
  }
  return {
    appended,
    backpressured,
    staged,
  };
}

function feedDecoderChunk(
  decoder: ByteDecoder,
  input: Uint8Array,
  chunks: Uint8Array[],
  maxChunkBytes: number,
  maxStagedBytes: number,
  force: boolean,
  initialStaged = 0,
): number {
  if (input.byteLength === 0) {
    return initialStaged;
  }
  let staged = initialStaged;
  let offset = 0;
  let iterations = 0;
  while (offset < input.byteLength) {
    iterations += 1;
    if (iterations > 1_000_000) {
      throw new PipelineError(
        ErrorCode.DECODER_BACKPRESSURE,
        "decoder did not consume its input",
        { offset, total: input.byteLength },
      );
    }
    const result = decoder.feed(input.subarray(offset));
    offset += result.consumed;
    const drained = drainDecoder(
      decoder,
      chunks,
      maxChunkBytes,
      maxStagedBytes,
      force,
      staged,
    );
    staged = drained.staged;
    if (result.consumed === 0) {
      if (drained.appended > 0) {
        continue;
      }
      if (decoder.finished) {
        break;
      }
      throw new PipelineError(
        ErrorCode.DECODER_BACKPRESSURE,
        "decoder consumed zero bytes without pending output",
        {
          offset,
          remaining: input.byteLength - offset,
          pendingInput: decoder.pendingInput,
        },
      );
    }
  }
  return staged;
}

function decodeTransport(
  decoder: ByteDecoder,
  transportChunks: readonly Uint8Array[],
  maxChunkBytes: number,
): Uint8Array {
  const chunks: Uint8Array[] = [];
  for (const transportChunk of transportChunks) {
    feedDecoderChunk(
      decoder,
      transportChunk,
      chunks,
      maxChunkBytes,
      Number.POSITIVE_INFINITY,
      true,
      0,
    );
  }
  decoder.finish();
  drainDecoder(
    decoder,
    chunks,
    maxChunkBytes,
    Number.POSITIVE_INFINITY,
    true,
    0,
  );
  return concatBytes(chunks);
}

/**
 * Importable authenticated pipeline.
 *
 * Security invariant: no bytes, callbacks, or credentials are exposed until
 * the transport and decoded representations (and every independent digest
 * group) have been verified, and the current revocation set is clear.
 */
export class AuthenticatedPipeline {
  readonly manifest: AssetManifest;
  readonly identity: RequestIdentity;
  readonly gate: PublicationGate;
  readonly cache?: VerificationCache;
  readonly revocations?: RevocationSet;
  readonly decodeMode: "stream" | "buffered";
  readonly highWaterMark: number;
  readonly maxChunkBytes: number;
  readonly instanceId?: string;
  readonly cacheTransformChain: readonly TransformStep[];

  #assembler: ShardAssembler;
  #decoder: ByteDecoder;
  #state: PipelineState = "open";
  #failure?: PipelineError;
  #credential?: PublicationCredential;
  #published?: PublishedAsset;
  #pendingShards: ReturnType<ShardAssembler["add"]>["drained"][number][] = [];
  #transportChunks: Uint8Array[] = [];
  #transportBytes = 0;
  #transportHashers: MultiHasher;
  #decodedChunks: Uint8Array[] = [];
  #stagedBytes = 0;
  #backpressured = false;
  #autoShardIndex = 0;
  #signal?: AbortSignal;
  #abortListener?: () => void;
  #requireIdentityBinding: boolean;
  #maxStagedBytes: number;

  constructor(
    manifestInput: AssetManifest | unknown,
    options: AuthenticatedPipelineOptions = {},
  ) {
    this.manifest = normalizeManifest(manifestInput);
    this.identity = normalizeRequestIdentity(options.identity);
    this.decodeMode =
      options.decodeMode ?? (options.streaming === false ? "buffered" : "stream");
    this.highWaterMark = options.highWaterMark ?? 64 * 1024;
    this.#maxStagedBytes = options.maxStagedBytes ?? 8 * 1024 * 1024;
    this.maxChunkBytes = options.maxChunkBytes ?? 64 * 1024;
    this.cache = options.cache;
    this.revocations = asRevocationSet(options.revocations);
    this.instanceId = options.instanceId;
    this.cacheTransformChain = cacheTransformChainFor(this.manifest, options);
    this.#requireIdentityBinding = options.requireIdentityBinding ?? false;
    if (this.#maxStagedBytes <= 0) {
      throw new PipelineError(
        ErrorCode.INVALID_ARGUMENT,
        "maxStagedBytes must be positive",
      );
    }
    if (this.highWaterMark <= 0 || this.maxChunkBytes <= 0) {
      throw new PipelineError(
        ErrorCode.INVALID_ARGUMENT,
        "highWaterMark and maxChunkBytes must be positive",
        {
          highWaterMark: this.highWaterMark,
          maxChunkBytes: this.maxChunkBytes,
        },
      );
    }
    this.gate = new PublicationGate(this.manifest, {
      revocations: this.revocations,
      onCallbackError: options.onCallbackError,
    });
    this.#assembler = new ShardAssembler({
      layout: this.manifest.shards,
      maxBufferedBytes: Math.max(this.#maxStagedBytes, 64 * 1024),
      verifyShardDigests: options.verifyShardDigests ?? true,
      alternateDigestDomain: this.manifest.transport.domain,
    });
    this.#decoder =
      options.decoder ??
      createDecoder(this.manifest.transformChain, {
        ...options.decoderOptions,
        maxOutputBuffer:
          options.decoderOptions?.maxOutputBuffer ??
          Math.max(this.highWaterMark, 1),
        maxInputBuffer:
          options.decoderOptions?.maxInputBuffer ??
          Math.max(4 * 1024 * 1024, this.manifest.transport.length),
      });
    this.#transportHashers = new MultiHasher(
      this.manifest.transport.groups.flatMap((group) =>
        group.entries
          .filter((entry) => isSupportedAlgorithm(entry.algorithm))
          .map((entry) => ({
            algorithm: entry.algorithm,
            domain:
              entry.domain ?? group.domain ?? this.manifest.transport.domain,
          })),
      ),
    );

    if (options.signal !== undefined) {
      this.#signal = options.signal;
      if (options.signal.aborted) {
        this.cancel(options.signal.reason);
      } else {
        this.#abortListener = () => this.cancel(options.signal?.reason);
        options.signal.addEventListener("abort", this.#abortListener, {
          once: true,
        });
      }
    }
  }

  get state(): PipelineState {
    return this.#state;
  }

  get failure(): PipelineError | undefined {
    return this.#failure;
  }

  get published(): boolean {
    return this.#state === "published";
  }

  get credential(): PublicationCredential | undefined {
    return this.#credential;
  }

  get backpressured(): boolean {
    return this.#backpressured;
  }

  get stagedBytes(): number {
    return this.#stagedBytes;
  }

  get bufferedShards(): number {
    return this.#pendingShards.length;
  }

  get expectedShards(): number | undefined {
    return this.#assembler.expectedCount;
  }

  onRead(callback: (credential: PublicationCredential) => void): () => void {
    return this.gate.onRead(callback);
  }

  onBytes(callback: (bytes: Uint8Array) => void): () => void {
    return this.gate.onRead((credential) => callback(credential.read()));
  }

  addShard(
    shardOrIndex: ShardInput | readonly [number, Uint8Array] | Uint8Array | number,
    data?: Uint8Array,
  ): ShardIngestResult {
    if (this.#state !== "open") {
      return {
        status: "ignored",
        index:
          typeof shardOrIndex === "number"
            ? shardOrIndex
            : this.#autoShardIndex,
        digest: "",
        drained: [],
        complete: this.#assembler.complete,
        backpressured: this.#backpressured,
      };
    }

    let input: ShardInput;
    if (typeof shardOrIndex === "number") {
      if (!(data instanceof Uint8Array)) {
        throw new PipelineError(
          ErrorCode.INVALID_SHARD,
          "addShard(index, data) requires Uint8Array data",
        );
      }
      input = { index: shardOrIndex, data };
      this.#autoShardIndex = Math.max(this.#autoShardIndex, shardOrIndex + 1);
    } else {
      input = normalizeShard(shardOrIndex, this.#autoShardIndex);
      this.#autoShardIndex = Math.max(
        this.#autoShardIndex,
        input.index + 1,
      );
    }

    try {
      const result = this.#assembler.add(input);
      this.#pendingShards.push(...result.drained);
      this.#pump(false);
      return {
        status: result.status,
        index: result.index,
        digest: result.digest,
        drained: result.drained,
        complete: result.complete,
        backpressured: this.#backpressured,
      };
    } catch (error) {
      this.#fail(error);
      throw this.#failure ?? error;
    }
  }

  async writeShard(
    shardOrIndex: ShardInput | readonly [number, Uint8Array] | Uint8Array | number,
    data?: Uint8Array,
  ): Promise<ShardIngestResult> {
    return this.addShard(shardOrIndex, data);
  }

  pushShard(
    shardOrIndex: ShardInput | readonly [number, Uint8Array] | Uint8Array | number,
    data?: Uint8Array,
  ): ShardIngestResult {
    return this.addShard(shardOrIndex, data);
  }

  #pump(force: boolean): void {
    if (this.#state !== "open" && this.#state !== "ending") {
      return;
    }
    while (this.#pendingShards.length > 0) {
      if (!force && this.#stagedBytes >= this.#maxStagedBytes) {
        this.#backpressured = true;
        return;
      }
      const shard = this.#pendingShards.shift();
      if (shard === undefined) {
        break;
      }
      if (
        this.#transportBytes + shard.data.byteLength >
        this.manifest.transport.length
      ) {
        throw new PipelineError(
          ErrorCode.LENGTH_MISMATCH,
          "transport exceeded its declared length",
          {
            declaredLength: this.manifest.transport.length,
            received: this.#transportBytes + shard.data.byteLength,
          },
        );
      }
      this.#transportChunks.push(cloneBytes(shard.data));
      this.#transportBytes += shard.data.byteLength;
      this.#transportHashers.update(shard.data);
      if (this.decodeMode === "stream") {
        this.#stagedBytes = feedDecoderChunk(
          this.#decoder,
          shard.data,
          this.#decodedChunks,
          this.maxChunkBytes,
          this.#maxStagedBytes,
          force,
          this.#stagedBytes,
        );
      }
      if (!force && this.#stagedBytes >= this.#maxStagedBytes) {
        this.#backpressured = true;
        return;
      }
    }
    this.#backpressured = false;
  }

  #streamedRawBytes(): Uint8Array {
    this.#pump(true);
    this.#decoder.finish();
    this.#stagedBytes = drainDecoder(
      this.#decoder,
      this.#decodedChunks,
      this.maxChunkBytes,
      Number.POSITIVE_INFINITY,
      true,
      this.#stagedBytes,
    ).staged;
    return concatBytes(this.#decodedChunks);
  }

  #verificationForStage(
    raw: Uint8Array,
    finalBytes: Uint8Array,
  ): {
    decoded: RepresentationVerification;
    final?: RepresentationVerification;
    stage: "raw" | "final";
  } {
    const placeholders = this.manifest.placeholders;
    const mode = this.manifest.decodedStage;
    if (mode === "final") {
      const decoded = assertRepresentation(
        this.manifest.decoded,
        finalBytes,
        { kind: "decoded" },
      );
      return {
        decoded,
        ...(this.manifest.final === undefined
          ? {}
          : {
              final: assertRepresentation(
                this.manifest.final,
                finalBytes,
                { kind: "final" },
              ),
            }),
        stage: "final",
      };
    }
    if (mode === "raw") {
      const decoded = assertRepresentation(
        this.manifest.decoded,
        raw,
        { kind: "decoded" },
      );
      return {
        decoded,
        ...(this.manifest.final === undefined
          ? {}
          : {
              final: assertRepresentation(
                this.manifest.final,
                finalBytes,
                { kind: "final" },
              ),
            }),
        stage: "raw",
      };
    }

    const rawVerification = verifyRepresentation(
      this.manifest.decoded,
      raw,
      { kind: "decoded" },
    );
    if (rawVerification.ok) {
      return {
        decoded: rawVerification,
        ...(this.manifest.final === undefined
          ? {}
          : {
              final: assertRepresentation(
                this.manifest.final,
                finalBytes,
                { kind: "final" },
              ),
            }),
        stage: "raw",
      };
    }
    const finalVerification = verifyRepresentation(
      this.manifest.decoded,
      finalBytes,
      { kind: "decoded" },
    );
    if (finalVerification.ok) {
      return {
        decoded: finalVerification,
        ...(this.manifest.final === undefined
          ? {}
          : {
              final: assertRepresentation(
                this.manifest.final,
                finalBytes,
                { kind: "final" },
              ),
            }),
        stage: "final",
      };
    }
    throw new PipelineError(
      ErrorCode.DIGEST_MISMATCH,
      "the decoded representation matched neither the raw nor final decoded bytes",
      {
        raw: {
          declaredLength: rawVerification.declaredLength,
          actualLength: rawVerification.actualLength,
          failures: rawVerification.failures,
        },
        final: {
          declaredLength: finalVerification.declaredLength,
          actualLength: finalVerification.actualLength,
          failures: finalVerification.failures,
        },
      },
    );
  }

  #finalizeSync(): PublishedAsset {
    if (this.#state === "published" && this.#published !== undefined) {
      return this.#published;
    }
    if (this.#state === "failed" || this.#state === "cancelled") {
      throw this.#failure ?? new PipelineError(
        ErrorCode.ABORTED,
        "pipeline is no longer open",
      );
    }
    this.#state = "ending";

    try {
      this.#assembler.finalize();
      this.#pump(true);
      const transportBytes = concatBytes(this.#transportChunks);

      const transportVerification = assertRepresentation(
        this.manifest.transport,
        transportBytes,
        { kind: "transport" },
      );

      // Ensure the streaming transport hasher and the verifier agree.
      for (const group of transportVerification.groups) {
        for (const check of group.checks) {
          const streamed = this.#transportHashers.digestFor(
            check.algorithm,
            check.domain,
          );
          if (streamed === undefined || streamed !== check.actual) {
            throw new PipelineError(
              ErrorCode.VERIFICATION_FAILED,
              "streaming transport hasher disagrees with representation verification",
              {
                algorithm: check.algorithm,
                domain: check.domain,
                streamed,
                verified: check.actual,
              },
            );
          }
        }
      }

      const contentCommitment = contentCommitmentFromManifest(this.manifest);
      const transformId = decoderChainId(this.cacheTransformChain);
      const identityId =
        this.identity.requestId ?? this.identity.id ?? "anonymous";
      const manifestId = manifestFingerprint(this.manifest);
      const intermediateKey = intermediateCacheKey({
        transformChain: this.cacheTransformChain,
        contentCommitment,
        manifestFingerprint: manifestId,
      });

      let raw: Uint8Array;
      let usedIntermediate = false;
      if (this.cache !== undefined) {
        const cachedIntermediate = this.cache.acquireIntermediate({
          key: intermediateKey,
          identity: identityId,
          revocations: this.revocations,
        });
        if (cachedIntermediate !== null) {
          raw = cachedIntermediate;
          usedIntermediate = true;
        } else {
          raw = this.#decodedRaw();
        }
      } else {
        raw = this.#decodedRaw();
      }

      const placeholderResult = replacePlaceholders(
        raw,
        this.manifest.placeholders,
        this.identity,
        { requireIdentitySource: this.#requireIdentityBinding },
      );
      const finalBytes = placeholderResult.bytes;
      const stageVerification = this.#verificationForStage(raw, finalBytes);
      const decodedVerification = stageVerification.decoded;
      const finalVerification = stageVerification.final;
      const decodedStage = stageVerification.stage;

      if (
        this.cache !== undefined &&
        decodedStage === "raw" &&
        !usedIntermediate
      ) {
        this.cache.putIntermediate({
          key: intermediateKey,
          bytes: raw,
          contentCommitment,
          transformChain: this.cacheTransformChain,
          revocationRefs: revocationRefs({
            manifest: this.manifest,
            contentCommitment,
            shardDigests: this.#assembler.shardDigests(),
          }),
          createdAt: Date.now(),
          instantiated: false,
        });
        this.cache.markIntermediateInstantiated(intermediateKey, identityId);
      }

      const placeholderBinding = placeholderIdentityDigest(
        this.identity,
        this.manifest.placeholders,
      );
      const refs = revocationRefs({
        manifest: this.manifest,
        contentCommitment,
        shardDigests: this.#assembler.shardDigests(),
      });
      const transportDigest =
        representativeDigest(transportVerification) ||
        transportCommitment(this.manifest);
      const decodedDigest =
        representativeDigest(decodedVerification) ||
        contentCommitment;

      const proof = createVerificationProof({
        manifest: this.manifest,
        bytes: finalBytes,
        transport: transportVerification,
        decoded: decodedVerification,
        ...(finalVerification === undefined
          ? {}
          : { final: finalVerification }),
        decodedStage,
        contentCommitment,
        transportDigest,
        decodedDigest,
        revocationRefs: refs,
        placeholderBinding,
        responseType: requestResponseType(this.identity),
        requestId: this.identity.requestId ?? this.identity.id ?? null,
      });

      this.gate.commit(proof, finalBytes);
      this.#credential = this.gate.credential;
      if (this.#credential === undefined) {
        throw new PipelineError(
          ErrorCode.CREDENTIAL_INVALID,
          "publication did not issue a credential",
        );
      }

      const asset: PublishedAsset = Object.freeze({
        credential: this.#credential,
        bytes: cloneBytes(finalBytes),
        get byteLength() {
          return finalBytes.byteLength;
        },
        get digest() {
          return proof.finalBytesCommitment;
        },
        read: (offset = 0, length?: number) =>
          (this.#credential as PublicationCredential).read(offset, length),
        manifest: this.manifest,
        identity: this.identity,
        transportDigest,
        decodedDigest,
        contentCommitment,
        decodedStage,
        transportVerification,
        decodedVerification,
        ...(finalVerification === undefined ? {} : { finalVerification }),
        replacements: placeholderResult.replacements,
        cacheHit: false,
      });
      this.#published = asset;
      this.#state = "published";
      this.#cleanupAbortListener();

      if (this.cache !== undefined) {
        try {
          const key = identityCacheKeyFor(
            this.manifest,
            this.identity,
            contentCommitment,
            {
              instanceId: this.instanceId,
              placeholderBinding,
              transformChain: this.cacheTransformChain,
              manifestFingerprint: manifestId,
            },
          );
          this.cache.put({
            key,
            bytes: finalBytes,
            contentCommitment,
            transformChain: this.cacheTransformChain,
            requestBodyDigest: requestBodyDigest(this.identity),
            responseType: requestResponseType(this.identity),
            placeholderBinding,
            manifestFingerprint: manifestId,
            revocationRefs: refs,
            verifiedAt: Date.now(),
            decodedStage,
            metadata: {
              transportDigest,
              decodedDigest,
              transformId,
              replacements: placeholderResult.replacements,
              transportVerification,
              decodedVerification,
              ...(finalVerification === undefined
                ? {}
                : { finalVerification }),
            },
          });
        } catch {
          // A cache write must never roll back an already atomic publication.
        }
      }
      return asset;
    } catch (error) {
      this.#fail(error);
      throw this.#failure ?? error;
    }
  }

  #decodedRaw(): Uint8Array {
    if (this.decodeMode === "stream") {
      return this.#streamedRawBytes();
    }
    const transportBytes = concatBytes(this.#transportChunks);
    return decodeTransport(
      this.#decoder,
      [transportBytes],
      this.maxChunkBytes,
    );
  }

  endSync(): PublishedAsset {
    return this.#finalizeSync();
  }

  async end(): Promise<PublishedAsset> {
    return this.#finalizeSync();
  }

  async publish(): Promise<PublicationCredential> {
    return this.#finalizeSync().credential;
  }

  read(offset = 0, length?: number): Uint8Array {
    if (this.#credential === undefined) {
      throw new PipelineError(
        ErrorCode.NOT_PUBLISHED,
        "the pipeline has not published an object",
        { state: this.#state },
      );
    }
    return this.#credential.read(offset, length);
  }

  get bytes(): Uint8Array {
    return this.read();
  }

  #fail(error: unknown): void {
    if (
      this.#state === "published" ||
      this.#state === "failed" ||
      this.#state === "cancelled"
    ) {
      return;
    }
    this.#state = "failed";
    this.#failure = asPipelineError(error);
    try {
      this.#decoder.cancel(this.#failure);
    } catch {
      // A cancelled decoder must not mask the original verification failure.
    }
    try {
      this.gate.abort(this.#failure);
    } catch {
      // The gate may already be committed; publication remains atomic.
    }
    this.#pendingShards = [];
    this.#cleanupAbortListener();
  }

  cancel(reason?: unknown): void {
    if (this.#state === "published") {
      return;
    }
    if (this.#state === "failed" || this.#state === "cancelled") {
      return;
    }
    this.#state = "cancelled";
    this.#failure = asPipelineError(
      reason,
      ErrorCode.CANCELLED,
      "asset pipeline was cancelled",
    );
    this.#decoder.cancel(this.#failure);
    this.gate.abort(this.#failure);
    this.#pendingShards = [];
    this.#cleanupAbortListener();
  }

  abort(reason?: unknown): void {
    this.cancel(reason);
  }

  /**
   * On disconnect we either hand back an object that was already atomically
   * complete and verified, or discard everything.  There is no partial commit.
   */
  disconnect(reason?: unknown): PublicationCredential | null {
    if (this.#state === "published") {
      return this.#credential ?? null;
    }
    if (this.#state === "failed" || this.#state === "cancelled") {
      return this.gate.disconnect(reason);
    }
    try {
      if (!this.#assembler.complete) {
        this.cancel(reason);
        return null;
      }
      return this.#finalizeSync().credential;
    } catch (error) {
      this.#fail(error);
      return null;
    }
  }

  #cleanupAbortListener(): void {
    if (this.#signal !== undefined && this.#abortListener !== undefined) {
      this.#signal.removeEventListener("abort", this.#abortListener);
      this.#abortListener = undefined;
    }
  }
}

export const AssetPipeline = AuthenticatedPipeline;
export const AuthenticatedAssetPipeline = AuthenticatedPipeline;

export async function publishAsset(
  manifest: AssetManifest | unknown,
  identity: RequestIdentity | string | number,
  shards:
    | Iterable<ShardInput | readonly [number, Uint8Array] | Uint8Array>
    | AsyncIterable<ShardInput | readonly [number, Uint8Array] | Uint8Array>,
  options: AuthenticatedPipelineOptions = {},
): Promise<PublishedAsset> {
  const pipeline = new AuthenticatedPipeline(manifest, {
    ...options,
    identity,
  });
  for await (const shard of shards) {
    pipeline.addShard(shard);
  }
  return pipeline.end();
}

export function createPipeline(
  manifest: AssetManifest | unknown,
  options: AuthenticatedPipelineOptions = {},
): AuthenticatedPipeline {
  return new AuthenticatedPipeline(manifest, options);
}
