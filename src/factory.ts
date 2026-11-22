import {
  canonicalAlgorithm,
  digestHex,
  type AlgorithmName,
  type HashAlgorithm,
} from "./hash.ts";
import {
  normalizeManifest,
  type AssetManifest,
  type DecodedStage,
  type DigestGroup,
  type PlaceholderSpec,
  type RepresentationKind,
  type RepresentationManifest,
  type ShardLayout,
  type TransformStep,
} from "./manifest.ts";

export interface RepresentationBuildOptions {
  readonly domain: string;
  readonly algorithms?: readonly AlgorithmName[];
  readonly groups?: readonly (readonly AlgorithmName[])[];
}

export function buildRepresentation(
  kind: RepresentationKind,
  bytes: Uint8Array,
  options: RepresentationBuildOptions,
): RepresentationManifest {
  const groupsInput =
    options.groups ??
    [options.algorithms ?? ["sha256"]];
  if (groupsInput.length === 0) {
    throw new Error("buildRepresentation needs at least one digest group");
  }
  const groups: DigestGroup[] = groupsInput.map((algorithms, index) => {
    if (algorithms.length === 0) {
      throw new Error("a digest group needs at least one algorithm");
    }
    const entries = algorithms.map((algorithm) => {
      const canonical = canonicalAlgorithm(algorithm);
      if (canonical === null) {
        throw new Error(`unsupported hash algorithm: ${String(algorithm)}`);
      }
      return {
        algorithm: canonical as HashAlgorithm,
        digest: digestHex(canonical, options.domain, bytes),
        domain: options.domain,
      };
    });
    return {
      id: `group-${index}`,
      domain: options.domain,
      entries,
    };
  });
  return {
    kind,
    domain: options.domain,
    length: bytes.byteLength,
    groups,
  };
}

export interface CreateManifestOptions {
  readonly transportBytes: Uint8Array;
  readonly decodedBytes: Uint8Array;
  readonly finalBytes?: Uint8Array;
  readonly transport: RepresentationBuildOptions;
  readonly decoded: RepresentationBuildOptions;
  readonly final?: RepresentationBuildOptions;
  readonly transform?: string | readonly (string | TransformStep)[];
  readonly placeholders?: readonly Partial<PlaceholderSpec>[];
  readonly shards?: ShardLayout | number | readonly number[];
  readonly decodedStage?: DecodedStage;
  readonly contentCommitment?: string;
  readonly id?: string;
  readonly version?: string | number;
  readonly metadata?: Readonly<Record<string, unknown>>;
}

/**
 * Convenience builder for tests and hosts.  Digest values are always computed
 * with the same domain framing used by the verifier.
 */
export function createManifest(options: CreateManifestOptions): AssetManifest {
  const decodedStage =
    options.decodedStage ??
    (options.finalBytes === undefined ? "raw" : "final");
  const decodedRepresentationBytes =
    decodedStage === "final"
      ? options.finalBytes ?? options.decodedBytes
      : options.decodedBytes;
  const transport = buildRepresentation(
    "transport",
    options.transportBytes,
    options.transport,
  );
  const decoded = buildRepresentation(
    "decoded",
    decodedRepresentationBytes,
    options.decoded,
  );
  const final =
    options.finalBytes === undefined
      ? undefined
      : buildRepresentation(
          "final",
          options.finalBytes,
          options.final ?? options.decoded,
        );
  const raw = {
    ...(options.id === undefined ? {} : { id: options.id }),
    ...(options.version === undefined ? {} : { version: options.version }),
    transport,
    decoded,
    ...(final === undefined ? {} : { final }),
    decodedStage,
    transform: options.transform ?? ["identity"],
    placeholders: options.placeholders ?? [],
    ...(options.shards === undefined ? {} : { shards: options.shards }),
    ...(options.contentCommitment === undefined
      ? {}
      : { contentCommitment: options.contentCommitment }),
    ...(options.metadata === undefined ? {} : { metadata: options.metadata }),
  };
  return normalizeManifest(raw);
}

export const buildManifest = createManifest;

/**
 * Splits a transport buffer into deterministic contiguous shards.
 */
export function partitionTransport(
  bytes: Uint8Array,
  sizes: readonly number[],
): Uint8Array[] {
  let total = 0;
  for (const size of sizes) {
    if (!Number.isSafeInteger(size) || size < 0) {
      throw new Error("shard sizes must be non-negative safe integers");
    }
    total += size;
  }
  if (total !== bytes.byteLength) {
    throw new Error(
      `shard sizes total ${total}, but transport has ${bytes.byteLength} bytes`,
    );
  }
  const shards: Uint8Array[] = [];
  let offset = 0;
  for (const size of sizes) {
    shards.push(bytes.slice(offset, offset + size));
    offset += size;
  }
  return shards;
}
