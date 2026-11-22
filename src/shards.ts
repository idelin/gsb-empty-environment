import { bytesEqual, cloneBytes } from "./canonical.ts";
import { ErrorCode, PipelineError } from "./errors.ts";
import {
  canonicalAlgorithm,
  constantTimeEqualHex,
  digestHex,
  normalizeDigestValue,
  type AlgorithmName,
  type HashAlgorithm,
} from "./hash.ts";
import type { ShardLayout, ShardSpec } from "./manifest.ts";

export const SHARD_DIGEST_DOMAIN = "gsb.shard.v1";

export interface ShardInput {
  readonly index: number;
  readonly data: Uint8Array;
  readonly algorithm?: AlgorithmName;
  readonly digest?: string;
}

export interface NormalizedShard {
  readonly index: number;
  readonly data: Uint8Array;
  readonly digest: string;
}

export type ShardAddStatus = "accepted" | "duplicate";

export interface ShardAddResult {
  readonly status: ShardAddStatus;
  readonly index: number;
  readonly digest: string;
  readonly drained: readonly NormalizedShard[];
  readonly complete: boolean;
}

export function shardDigest(
  data: Uint8Array,
  algorithm: AlgorithmName = "sha256",
): string {
  return digestHex(algorithm, SHARD_DIGEST_DOMAIN, data);
}

export function normalizeShard(
  shard: ShardInput | readonly [number, Uint8Array] | Uint8Array,
  fallbackIndex?: number,
): ShardInput {
  if (shard instanceof Uint8Array) {
    if (fallbackIndex === undefined) {
      throw new PipelineError(
        ErrorCode.INVALID_SHARD,
        "a raw shard needs an explicit or inferred index",
      );
    }
    return { index: fallbackIndex, data: shard };
  }
  if (Array.isArray(shard)) {
    return { index: shard[0] as number, data: shard[1] as Uint8Array };
  }
  const record = shard as ShardInput;
  if (record === null || typeof record !== "object") {
    throw new PipelineError(
      ErrorCode.INVALID_SHARD,
      "invalid shard descriptor",
      { valueType: typeof shard },
    );
  }
  const indexValue =
    (record as unknown as Record<string, unknown>).index ??
    (record as unknown as Record<string, unknown>).i ??
    (record as unknown as Record<string, unknown>).sequence ??
    (record as unknown as Record<string, unknown>).seq ??
    (record as unknown as Record<string, unknown>).order ??
    fallbackIndex;
  const dataValue =
    (record as unknown as Record<string, unknown>).data ??
    (record as unknown as Record<string, unknown>).bytes ??
    (record as unknown as Record<string, unknown>).chunk ??
    (record as unknown as Record<string, unknown>).payload ??
    (record as unknown as Record<string, unknown>).body;
  if (!Number.isSafeInteger(indexValue) || (indexValue as number) < 0) {
    throw new PipelineError(
      ErrorCode.INVALID_SHARD,
      "shard index must be a non-negative safe integer",
      { index: indexValue },
    );
  }
  if (!(dataValue instanceof Uint8Array)) {
    throw new PipelineError(
      ErrorCode.INVALID_SHARD,
      "shard data must be a Uint8Array",
      { index: indexValue, dataType: typeof dataValue },
    );
  }
  return {
    index: indexValue as number,
    data: dataValue,
    ...((record as unknown as Record<string, unknown>).algorithm === undefined
      ? {}
      : {
          algorithm: (record as unknown as Record<string, unknown>)
            .algorithm as AlgorithmName,
        }),
    ...((record as unknown as Record<string, unknown>).digest === undefined
      ? {}
      : {
          digest: (record as unknown as Record<string, unknown>)
            .digest as string,
        }),
  };
}

export interface ShardAssemblerOptions {
  readonly layout?: ShardLayout;
  readonly maxBufferedBytes?: number;
  /** Verify per-shard digests declared by the manifest. */
  readonly verifyShardDigests?: boolean;
  /** Additional domain tag accepted for declared per-shard digests. */
  readonly alternateDigestDomain?: string;
}

/**
 * Accepts shards in any order and tolerates byte-identical duplicates.  A
 * duplicate index with different bytes is a hard conflict, not a "last writer
 * wins" update.
 */
export class ShardAssembler {
  readonly expectedCount?: number;
  readonly expectedLengths?: readonly number[];
  readonly maxBufferedBytes: number;
  readonly verifyShardDigests: boolean;
  readonly alternateDigestDomain?: string;

  #specs = new Map<number, ShardSpec>();
  #received = new Map<number, NormalizedShard>();
  #all = new Map<number, NormalizedShard>();
  #nextIndex = 0;
  #bufferedBytes = 0;
  #drainedBytes = 0;

  constructor(options: ShardAssemblerOptions = {}) {
    const layout = options.layout;
    this.#specs = new Map(
      (layout?.shards ?? []).map((spec) => [spec.index, spec] as const),
    );
    if (layout?.count !== undefined) {
      this.expectedCount = layout.count;
    } else if (layout?.lengths !== undefined) {
      this.expectedCount = layout.lengths.length;
    } else if (layout?.shards !== undefined && layout.shards.length > 0) {
      this.expectedCount =
        Math.max(...layout.shards.map((spec) => spec.index)) + 1;
    }
    this.expectedLengths =
      layout?.lengths ??
      (layout?.shards !== undefined &&
      layout.shards.every((spec) => spec.length !== undefined)
        ? (() => {
            const lengths: number[] = [];
            for (const spec of layout.shards) {
              lengths[spec.index] = spec.length as number;
            }
            return lengths;
          })()
        : undefined);
    this.maxBufferedBytes =
      options.maxBufferedBytes ?? 64 * 1024 * 1024;
    this.verifyShardDigests = options.verifyShardDigests ?? true;
    this.alternateDigestDomain = options.alternateDigestDomain;
  }

  get nextIndex(): number {
    return this.#nextIndex;
  }

  get bufferedBytes(): number {
    return this.#bufferedBytes;
  }

  get drainedBytes(): number {
    return this.#drainedBytes;
  }

  get receivedCount(): number {
    return this.#received.size;
  }

  get backpressured(): boolean {
    return this.#bufferedBytes > this.maxBufferedBytes;
  }

  get complete(): boolean {
    if (this.expectedCount !== undefined) {
      return this.#nextIndex === this.expectedCount;
    }
    if (this.#received.size === 0) {
      return this.#nextIndex === 0;
    }
    return this.#nextIndex === Math.max(...this.#received.keys()) + 1;
  }

  add(shard: ShardInput): ShardAddResult {
    const index = shard.index;
    if (!Number.isSafeInteger(index) || index < 0) {
      throw new PipelineError(
        ErrorCode.INVALID_SHARD,
        "shard index must be a non-negative safe integer",
        { index },
      );
    }
    if (this.expectedCount !== undefined && index >= this.expectedCount) {
      throw new PipelineError(
        ErrorCode.SHARD_RANGE,
        "shard index is outside the declared shard count",
        { index, expectedCount: this.expectedCount },
      );
    }
    if (!(shard.data instanceof Uint8Array)) {
      throw new PipelineError(
        ErrorCode.INVALID_SHARD,
        "shard data must be a Uint8Array",
        { index, dataType: typeof shard.data },
      );
    }
    const expectedLength = this.expectedLengths?.[index];
    if (
      expectedLength !== undefined &&
      shard.data.byteLength !== expectedLength
    ) {
      throw new PipelineError(
        ErrorCode.SHARD_RANGE,
        "shard length does not match the declared layout",
        {
          index,
          expectedLength,
          actualLength: shard.data.byteLength,
        },
      );
    }

    const copy = cloneBytes(shard.data);
    const digest = shardDigest(copy);
    if (shard.digest !== undefined) {
      const algorithm = canonicalAlgorithm(shard.algorithm ?? "sha256");
      if (algorithm === null) {
        throw new PipelineError(
          ErrorCode.UNSUPPORTED_ALGORITHM,
          "shard digest algorithm is unsupported",
          { index, algorithm: shard.algorithm },
        );
      }
      const expected = normalizeDigestValue(shard.digest, algorithm);
      const actual = digestHex(algorithm, SHARD_DIGEST_DOMAIN, copy);
      const alternate =
        this.alternateDigestDomain === undefined
          ? undefined
          : digestHex(algorithm, this.alternateDigestDomain, copy);
      if (
        !constantTimeEqualHex(expected, actual) &&
        (alternate === undefined ||
          !constantTimeEqualHex(expected, alternate))
      ) {
        throw new PipelineError(
          ErrorCode.DIGEST_MISMATCH,
          "provided shard digest does not match its bytes",
          { index, algorithm, expected, actual, alternate },
        );
      }
    }
    const existing = this.#received.get(index) ?? this.#all.get(index);
    if (existing !== undefined) {
      if (!bytesEqual(existing.data, copy)) {
        throw new PipelineError(
          ErrorCode.SHARD_CONFLICT,
          "the same shard index was supplied with different bytes",
          {
            index,
            existingDigest: existing.digest,
            incomingDigest: digest,
            existingLength: existing.data.byteLength,
            incomingLength: copy.byteLength,
          },
        );
      }
      return {
        status: "duplicate",
        index,
        digest,
        drained: [],
        complete: this.complete,
      };
    }

    const spec = this.#specs.get(index);
    if (
      this.verifyShardDigests &&
      spec?.digest !== undefined &&
      spec.algorithm !== undefined
    ) {
      const expected = spec.digest;
      const actual = digestHex(spec.algorithm, SHARD_DIGEST_DOMAIN, copy);
      const alternate =
        this.alternateDigestDomain === undefined
          ? undefined
          : digestHex(spec.algorithm, this.alternateDigestDomain, copy);
      if (
        !constantTimeEqualHex(expected, actual) &&
        (alternate === undefined ||
          !constantTimeEqualHex(expected, alternate))
      ) {
        throw new PipelineError(
          ErrorCode.DIGEST_MISMATCH,
          "shard digest does not match the manifest",
          {
            index,
            algorithm: spec.algorithm,
            expected,
            actual,
            alternate,
          },
        );
      }
    }
    this.#received.set(index, { index, data: copy, digest });
    this.#all.set(index, { index, data: copy, digest });
    this.#bufferedBytes += copy.byteLength;
    const drained = this.#drain();
    return {
      status: "accepted",
      index,
      digest,
      drained,
      complete: this.complete,
    };
  }

  #drain(): NormalizedShard[] {
    const drained: NormalizedShard[] = [];
    while (this.#received.has(this.#nextIndex)) {
      const shard = this.#received.get(this.#nextIndex) as NormalizedShard;
      this.#received.delete(this.#nextIndex);
      this.#bufferedBytes -= shard.data.byteLength;
      this.#drainedBytes += shard.data.byteLength;
      drained.push(shard);
      this.#nextIndex += 1;
    }
    return drained;
  }

  /**
   * Ends the stream.  If the manifest declared a shard count, all indices must
   * have arrived.  Without a declared count the highest received index defines
   * the count, but gaps still fail.
   */
  finalize(): void {
    if (this.expectedCount !== undefined) {
      if (this.#nextIndex !== this.expectedCount) {
        const missing: number[] = [];
        for (let index = this.#nextIndex; index < this.expectedCount; index += 1) {
          if (!this.#received.has(index)) {
            missing.push(index);
          }
        }
        throw new PipelineError(
          ErrorCode.SHARD_GAP,
          "transport ended before every shard was received",
          { missing, nextIndex: this.#nextIndex, expectedCount: this.expectedCount },
        );
      }
      return;
    }
    if (this.#received.size > 0) {
      throw new PipelineError(
        ErrorCode.SHARD_GAP,
        "transport contains a shard gap",
        {
          nextIndex: this.#nextIndex,
          received: [...this.#received.keys()].sort((a, b) => a - b),
        },
      );
    }
  }

  shardDigests(): readonly { index: number; digest: string; length: number }[] {
    return [...this.#all.values()]
      .sort((a, b) => a.index - b.index)
      .map((shard) => ({
        index: shard.index,
        digest: shard.digest,
        length: shard.data.byteLength,
      }));
  }
}

export function assembleShards(
  shards: Iterable<ShardInput | readonly [number, Uint8Array] | Uint8Array>,
  options: ShardAssemblerOptions = {},
): { bytes: Uint8Array; shards: readonly NormalizedShard[] } {
  const assembler = new ShardAssembler(options);
  const collected: NormalizedShard[] = [];
  let autoIndex = 0;
  for (const input of shards) {
    const normalized = normalizeShard(input, autoIndex);
    autoIndex = Math.max(autoIndex + 1, normalized.index + 1);
    const result = assembler.add(normalized);
    collected.push(...result.drained);
  }
  assembler.finalize();
  let total = 0;
  for (const shard of collected) {
    total += shard.data.byteLength;
  }
  const bytes = new Uint8Array(total);
  let offset = 0;
  for (const shard of collected) {
    bytes.set(shard.data, offset);
    offset += shard.data.byteLength;
  }
  return { bytes, shards: collected };
}
