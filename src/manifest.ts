import {
  ALGORITHM_STRENGTH,
  canonicalAlgorithm,
  digestHex,
  normalizeDigestValue,
  type AlgorithmName,
  type HashAlgorithm,
} from "./hash.ts";
import { canonicalJson, cloneBytes } from "./canonical.ts";
import { ErrorCode, PipelineError } from "./errors.ts";

export type RepresentationKind = "transport" | "decoded" | "final";
export type DecodedStage = "auto" | "raw" | "final";

export interface DigestEntry {
  readonly algorithm: HashAlgorithm | string;
  readonly digest: string;
  readonly domain?: string;
}

export interface DigestGroup {
  readonly id?: string;
  readonly domain?: string;
  readonly length?: number;
  readonly entries: readonly DigestEntry[];
}

export interface RepresentationManifest {
  readonly kind: RepresentationKind;
  readonly domain: string;
  readonly length: number;
  readonly groups: readonly DigestGroup[];
}

export interface TransformStep {
  readonly id: string;
  readonly version?: string | number;
  readonly options?: Readonly<Record<string, unknown>>;
}

export type PlaceholderValue = string | Uint8Array;

export interface PlaceholderSpec {
  readonly id: string;
  /** Offset in the decoded byte stream, before any replacement. */
  readonly offset: number;
  /** Number of source bytes occupied by the placeholder. */
  readonly length: number;
  /** Optional literal that must occupy the declared source span. */
  readonly token?: Uint8Array;
  /** Identity field whose value supplies the replacement. */
  readonly field?: string;
  /** Literal replacement, useful for manifests without per-request data. */
  readonly value?: Uint8Array;
  readonly encoding?: "utf8" | "hex" | "base64" | "base64url";
  readonly binding: "identity" | "none";
  readonly bindingAlgorithm: HashAlgorithm;
  readonly bindingDomain: string;
  readonly bindingDigest?: string;
}

export interface ShardSpec {
  readonly index: number;
  readonly length?: number;
  readonly algorithm?: HashAlgorithm;
  readonly digest?: string;
}

export interface ShardLayout {
  readonly count?: number;
  readonly lengths?: readonly number[];
  readonly shards?: readonly ShardSpec[];
}

export interface AssetManifest {
  readonly id?: string;
  readonly version?: string | number;
  readonly transport: RepresentationManifest;
  readonly decoded: RepresentationManifest;
  readonly final?: RepresentationManifest;
  readonly decodedStage: DecodedStage;
  readonly transformChain: readonly TransformStep[];
  readonly placeholders: readonly PlaceholderSpec[];
  readonly shards?: ShardLayout;
  readonly contentCommitment?: string;
  readonly metadata?: Readonly<Record<string, unknown>>;
}

type JsonObject = Record<string, unknown>;

function asObject(value: unknown, label: string): JsonObject {
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    throw new PipelineError(
      ErrorCode.INVALID_MANIFEST,
      `${label} must be an object`,
      { label },
    );
  }
  return value as JsonObject;
}

function firstDefined(record: JsonObject, keys: readonly string[]): unknown {
  for (const key of keys) {
    if (record[key] !== undefined) {
      return record[key];
    }
  }
  return undefined;
}

function normalizeDomain(value: unknown, label: string): string {
  if (typeof value !== "string" || value.length === 0) {
    throw new PipelineError(
      ErrorCode.INVALID_MANIFEST,
      `${label} requires a non-empty domain tag`,
      { label, domain: value },
    );
  }
  return value;
}

function normalizeLength(value: unknown, label: string): number {
  if (value === undefined) {
    throw new PipelineError(
      ErrorCode.INVALID_MANIFEST,
      `${label} requires a length`,
      { label },
    );
  }
  if (!Number.isSafeInteger(value) || (value as number) < 0) {
    throw new PipelineError(
      ErrorCode.INVALID_MANIFEST,
      `${label} length must be a non-negative safe integer`,
      { label, length: value },
    );
  }
  return value as number;
}

function toBytes(value: unknown, encoding?: string): Uint8Array {
  if (value instanceof Uint8Array) {
    return cloneBytes(value);
  }
  if (value instanceof ArrayBuffer) {
    return new Uint8Array(value.slice(0));
  }
  if (
    Array.isArray(value) &&
    value.every(
      (byte) =>
        Number.isInteger(byte) &&
        (byte as number) >= 0 &&
        (byte as number) <= 255,
    )
  ) {
    return new Uint8Array(value as number[]);
  }
  if (typeof value === "string") {
    if (encoding === "hex") {
      if (!/^[0-9a-f]*$/i.test(value) || value.length % 2 !== 0) {
        throw new PipelineError(
          ErrorCode.INVALID_MANIFEST,
          "placeholder value is not valid hexadecimal",
          { value },
        );
      }
      return new Uint8Array(Buffer.from(value, "hex"));
    }
    if (encoding === "base64" || encoding === "base64url") {
      return new Uint8Array(Buffer.from(value, encoding));
    }
    return new Uint8Array(Buffer.from(value, "utf8"));
  }
  throw new PipelineError(
    ErrorCode.INVALID_MANIFEST,
    "a placeholder value must be a string or Uint8Array",
    { valueType: typeof value },
  );
}

function normalizeDigestEntry(
  input: unknown,
  defaults: { algorithm?: AlgorithmName; domain?: string },
): DigestEntry {
  if (input instanceof Uint8Array) {
    if (defaults.algorithm === undefined) {
      throw new PipelineError(
        ErrorCode.INVALID_REPRESENTATION,
        "a binary digest needs an algorithm",
        { digestLength: input.byteLength },
      );
    }
    const canonical = canonicalAlgorithm(defaults.algorithm);
    const algorithm = canonical ?? String(defaults.algorithm).trim().toLowerCase();
    return {
      algorithm,
      digest: Buffer.from(
        input.buffer,
        input.byteOffset,
        input.byteLength,
      )
        .toString("hex")
        .toLowerCase(),
      ...(defaults.domain === undefined ? {} : { domain: defaults.domain }),
    };
  }
  if (typeof input === "string") {
    let algorithm = defaults.algorithm;
    let value = input;
    const match = /^([a-z0-9_-]+):(.*)$/i.exec(input);
    if (match && canonicalAlgorithm(match[1]) !== null) {
      algorithm = match[1];
      value = match[2];
    }
    if (algorithm === undefined || String(algorithm).trim().length === 0) {
      throw new PipelineError(
        ErrorCode.INVALID_REPRESENTATION,
        "a string digest needs an algorithm",
        { digest: input },
      );
    }
    const canonical = canonicalAlgorithm(algorithm);
    const algorithmName = canonical ?? String(algorithm).trim().toLowerCase();
    return {
      algorithm: algorithmName,
      digest: normalizeDigestValue(value, algorithmName),
      ...(defaults.domain === undefined ? {} : { domain: defaults.domain }),
    };
  }

  const record = asObject(input, "digest entry");
  const algorithmName = firstDefined(record, [
    "algorithm",
    "alg",
    "hash",
    "type",
  ]);
  const requestedAlgorithm = (algorithmName ?? defaults.algorithm) as
    | AlgorithmName
    | undefined;
  if (
    requestedAlgorithm === undefined ||
    String(requestedAlgorithm).trim().length === 0
  ) {
    throw new PipelineError(
      ErrorCode.INVALID_REPRESENTATION,
      "digest entry has no algorithm",
      { algorithm: algorithmName },
    );
  }
  const canonical = canonicalAlgorithm(requestedAlgorithm);
  const normalizedAlgorithm =
    canonical ?? String(requestedAlgorithm).trim().toLowerCase();
  const digest = firstDefined(record, ["digest", "hash", "value", "bytes"]);
  const domain = firstDefined(record, ["domain", "domainTag", "tag"]);
  const encoding = firstDefined(record, ["encoding", "digestEncoding"]);
  return {
    algorithm: normalizedAlgorithm,
    digest: normalizeDigestValue(
      digest,
      normalizedAlgorithm,
      encoding as "hex" | "base64" | "base64url" | undefined,
    ),
    ...(domain === undefined
      ? defaults.domain === undefined
        ? {}
        : { domain: defaults.domain }
      : { domain: normalizeDomain(domain, "digest entry") }),
  };
}

function parsePrefixedAlgorithm(value: string): AlgorithmName | undefined {
  const match = /^([a-z0-9_-]+):/i.exec(value);
  if (match && match[1].length > 0) {
    return match[1];
  }
  return undefined;
}

function entriesFromDigests(
  digests: unknown,
  algorithms: unknown,
  defaults: { algorithm?: AlgorithmName; domain?: string },
): DigestEntry[] {
  if (digests === undefined) {
    return [];
  }
  if (digests instanceof Uint8Array) {
    return [normalizeDigestEntry(digests, defaults)];
  }
  if (typeof digests === "string") {
    return [normalizeDigestEntry(digests, defaults)];
  }
  if (Array.isArray(digests)) {
    const algorithmList = Array.isArray(algorithms)
      ? algorithms
      : algorithms === undefined
        ? []
        : [algorithms];
    return digests.map((digest, index) => {
      const algorithm =
        typeof digest === "object" && digest !== null && !ArrayBuffer.isView(digest)
          ? undefined
          : algorithmList[index] ??
            defaults.algorithm ??
            (typeof digest === "string"
              ? parsePrefixedAlgorithm(digest)
              : undefined);
      return normalizeDigestEntry(digest, {
        algorithm: algorithm as AlgorithmName | undefined,
        domain: defaults.domain,
      });
    });
  }
  if (digests !== null && typeof digests === "object") {
    return Object.entries(digests as JsonObject).map(([algorithm, digest]) =>
      normalizeDigestEntry(
        { algorithm, digest },
        { domain: defaults.domain },
      ),
    );
  }
  throw new PipelineError(
    ErrorCode.INVALID_REPRESENTATION,
    "unsupported digest declaration",
    { digestsType: typeof digests },
  );
}

function normalizeGroupInput(
  input: unknown,
  index: number,
  defaults: { algorithm?: AlgorithmName; domain?: string },
): DigestGroup {
  if (typeof input === "string" || input instanceof Uint8Array) {
    return {
      id: `group-${index}`,
      entries: [normalizeDigestEntry(input, defaults)],
    };
  }
  if (Array.isArray(input)) {
    if (
      input.length === 2 &&
      typeof input[0] === "string" &&
      canonicalAlgorithm(input[0]) !== null &&
      (typeof input[1] === "string" || input[1] instanceof Uint8Array)
    ) {
      return {
        id: `group-${index}`,
        entries: [
          normalizeDigestEntry(
            { algorithm: input[0], digest: input[1] },
            defaults,
          ),
        ],
      };
    }
    const entries = entriesFromDigests(input, undefined, defaults);
    if (entries.length === 0) {
      throw new PipelineError(
        ErrorCode.INVALID_REPRESENTATION,
        "a digest group must contain at least one entry",
        { groupIndex: index },
      );
    }
    return { id: `group-${index}`, entries };
  }

  const record = asObject(input, "digest group");
  const idValue = firstDefined(record, ["id", "name", "label"]);
  const domainValue = firstDefined(record, ["domain", "domainTag", "tag"]);
  const lengthValue = firstDefined(record, ["length", "byteLength", "size"]);
  const explicitEntries = firstDefined(record, [
    "entries",
    "items",
    "digests",
    "hashes",
  ]);
  const algorithms = firstDefined(record, ["algorithms", "algs"]);
  const algorithm = firstDefined(record, ["algorithm", "alg", "hash", "type"]);
  const digest = firstDefined(record, ["digest", "digestHex", "value"]);
  const entryDomain =
    domainValue === undefined
      ? defaults.domain
      : normalizeDomain(domainValue, "digest group");
  const entryDefaults = {
    algorithm: (algorithm ?? defaults.algorithm) as AlgorithmName | undefined,
    domain: entryDomain,
  };

  let entries: DigestEntry[];
  if (explicitEntries !== undefined) {
    if (Array.isArray(explicitEntries)) {
      if (explicitEntries.some((item) => Array.isArray(item))) {
        throw new PipelineError(
          ErrorCode.INVALID_REPRESENTATION,
          "nested digest arrays must be declared as separate groups",
          { groupIndex: index },
        );
      }
      entries = entriesFromDigests(explicitEntries, algorithms, entryDefaults);
    } else {
      entries = entriesFromDigests(explicitEntries, algorithms, entryDefaults);
    }
  } else if (algorithm !== undefined || digest !== undefined) {
    entries = [
      normalizeDigestEntry(
        digest === undefined ? record : { ...record, algorithm, digest },
        entryDefaults,
      ),
    ];
  } else {
    entries = [];
  }

  if (entries.length === 0) {
    throw new PipelineError(
      ErrorCode.INVALID_REPRESENTATION,
      "a digest group must contain at least one entry",
      { groupIndex: index, groupId: idValue },
    );
  }

  const length =
    lengthValue === undefined
      ? undefined
      : normalizeLength(lengthValue, "digest group");

  return {
    id:
      typeof idValue === "string" && idValue.length > 0
        ? idValue
        : `group-${index}`,
    ...(entryDomain === undefined ? {} : { domain: entryDomain }),
    ...(length === undefined ? {} : { length }),
    entries,
  };
}

function normalizeRepresentation(
  input: unknown,
  kind: RepresentationKind,
  label: string,
): RepresentationManifest {
  const record = asObject(input, label);
  const domain = normalizeDomain(
    firstDefined(record, ["domain", "domainTag", "tag", "context"]),
    label,
  );
  const length = normalizeLength(
    firstDefined(record, ["length", "byteLength", "size"]),
    label,
  );

  const groupsInput = firstDefined(record, [
    "groups",
    "digestGroups",
    "authenticationGroups",
  ]);
  const digestsInput = firstDefined(record, ["digests", "hashes"]);
  const algorithms = firstDefined(record, ["algorithms", "algs"]);
  const algorithm = firstDefined(record, ["algorithm", "alg", "hash", "type"]);
  const singleDigest = firstDefined(record, ["digest", "digestHex"]);

  let groups: DigestGroup[];
  const groupsIsDescriptor =
    groupsInput !== null &&
    typeof groupsInput === "object" &&
    !Array.isArray(groupsInput) &&
    ["entries", "items", "digests", "hashes", "algorithm", "alg", "digest"].some(
      (key) => key in (groupsInput as JsonObject),
    );
  if (
    groupsInput !== undefined &&
    groupsInput !== null &&
    typeof groupsInput === "object" &&
    !Array.isArray(groupsInput) &&
    !groupsIsDescriptor
  ) {
    groups = Object.entries(groupsInput as JsonObject).map(([id, group], index) => {
      const normalized = normalizeGroupInput(group, index, {
        algorithm: algorithm as AlgorithmName | undefined,
        domain,
      });
      return { ...normalized, id };
    });
  } else if (
    groupsInput === undefined &&
    Array.isArray(digestsInput) &&
    digestsInput.some((entry) => Array.isArray(entry))
  ) {
    groups = (digestsInput as unknown[]).map((group, index) =>
      normalizeGroupInput(group, index, {
        algorithm: algorithm as AlgorithmName | undefined,
        domain,
      }),
    );
  } else if (groupsInput !== undefined && Array.isArray(groupsInput) && groupsInput.some(Array.isArray)) {
    groups = groupsInput.map((group, index) =>
      normalizeGroupInput(group, index, {
        algorithm: algorithm as AlgorithmName | undefined,
        domain,
      }),
    );
  } else if (
    groupsInput !== undefined &&
    Array.isArray(groupsInput) &&
    groupsInput.every(
      (entry) =>
        typeof entry === "object" &&
        entry !== null &&
        ("entries" in entry ||
          "digests" in entry ||
          "items" in entry ||
          "algorithm" in entry ||
          "alg" in entry),
    )
  ) {
    groups = (groupsInput as unknown[]).map((group, index) =>
      normalizeGroupInput(group, index, {
        algorithm: algorithm as AlgorithmName | undefined,
        domain,
      }),
    );
  } else if (groupsInput !== undefined) {
    groups = [
      normalizeGroupInput(groupsInput, 0, {
        algorithm: algorithm as AlgorithmName | undefined,
        domain,
      }),
    ];
  } else {
    const entries = entriesFromDigests(
      digestsInput ?? (singleDigest === undefined ? undefined : [singleDigest]),
      algorithms,
      {
        algorithm: algorithm as AlgorithmName | undefined,
        domain,
      },
    );
    if (entries.length === 0) {
      throw new PipelineError(
        ErrorCode.INVALID_REPRESENTATION,
        `${label} must declare at least one digest group`,
        { kind },
      );
    }
    groups = [{ id: "primary", entries }];
  }

  if (groups.length === 0) {
    throw new PipelineError(
      ErrorCode.INVALID_REPRESENTATION,
      `${label} must declare at least one digest group`,
      { kind },
    );
  }

  return { kind, domain, length, groups };
}

function normalizeTransformStep(input: unknown, index: number): TransformStep {
  if (typeof input === "string") {
    return { id: input, version: 1 };
  }
  const record = asObject(input, "transform step");
  const id = firstDefined(record, [
    "id",
    "name",
    "codec",
    "algorithm",
    "type",
    "kind",
  ]);
  if (typeof id !== "string" || id.length === 0) {
    throw new PipelineError(
      ErrorCode.INVALID_MANIFEST,
      "transform step requires an id",
      { index },
    );
  }
  const version = firstDefined(record, ["version", "v"]);
  const options = firstDefined(record, ["options", "config", "params"]);
  return {
    id,
    ...(version === undefined ? {} : { version: version as string | number }),
    ...(options === undefined
      ? {}
      : { options: asObject(options, "transform options") }),
  };
}

function normalizeTransformChain(input: unknown): TransformStep[] {
  if (input === undefined) {
    return [{ id: "identity", version: 1 }];
  }
  if (typeof input === "string") {
    return [normalizeTransformStep(input, 0)];
  }
  if (Array.isArray(input)) {
    if (input.length === 0) {
      return [{ id: "identity", version: 1 }];
    }
    return input.map(normalizeTransformStep);
  }
  if (input !== null && typeof input === "object") {
    return [normalizeTransformStep(input, 0)];
  }
  throw new PipelineError(
    ErrorCode.INVALID_MANIFEST,
    "transform chain must be a string, array, or object",
    { transformType: typeof input },
  );
}

function normalizePlaceholder(
  input: unknown,
  index: number,
  idFromMap?: string,
): PlaceholderSpec {
  const record = Array.isArray(input)
    ? {
        offset: input[0],
        length: input[1],
        token: input[2],
        field: input[3],
      }
    : asObject(input, "placeholder");

  const idValue = firstDefined(record, ["id", "name", "key"]) ?? idFromMap;
  const id =
    typeof idValue === "string" && idValue.length > 0
      ? idValue
      : `placeholder-${index}`;

  let offset = firstDefined(record, [
    "offset",
    "at",
    "position",
    "start",
    "index",
  ]);
  let length = firstDefined(record, ["length", "size", "span", "width"]);
  const end = firstDefined(record, ["end", "stop"]);
  if (length === undefined && offset !== undefined && end !== undefined) {
    length = (end as number) - (offset as number);
  }
  if (!Number.isSafeInteger(offset) || (offset as number) < 0) {
    throw new PipelineError(
      ErrorCode.INVALID_MANIFEST,
      "placeholder offset must be a non-negative safe integer",
      { id, offset },
    );
  }

  const encodingValue = firstDefined(record, ["encoding", "valueEncoding"]);
  const encoding =
    encodingValue === undefined
      ? undefined
      : (String(encodingValue) as PlaceholderSpec["encoding"]);
  const tokenValue = firstDefined(record, ["token", "marker", "expected"]);
  const valueValue = firstDefined(record, ["value", "replacement"]);
  const token =
    tokenValue === undefined ? undefined : toBytes(tokenValue, encoding);
  if (length === undefined && token !== undefined) {
    length = token.byteLength;
  }
  if (!Number.isSafeInteger(length) || (length as number) < 0) {
    throw new PipelineError(
      ErrorCode.INVALID_MANIFEST,
      "placeholder length must be a non-negative safe integer",
      { id, length },
    );
  }

  const fieldValue = firstDefined(record, [
    "field",
    "identityField",
    "valueFrom",
    "source",
  ]);
  const field =
    typeof fieldValue === "string" &&
    fieldValue.length > 0 &&
    !["literal", "static", "manifest", "identity", "request"].includes(
      fieldValue.toLowerCase(),
    )
      ? fieldValue
      : undefined;
  const value =
    valueValue === undefined ? undefined : toBytes(valueValue, encoding);

  const bindingValue = firstDefined(record, [
    "binding",
    "requestBinding",
    "boundTo",
  ]);
  const bindingRecord =
    bindingValue !== null &&
    typeof bindingValue === "object" &&
    !Array.isArray(bindingValue)
      ? (bindingValue as JsonObject)
      : undefined;
  const binding =
    bindingValue === "none" ||
    bindingValue === false ||
    bindingRecord?.required === false ||
    bindingRecord?.mode === "none"
      ? "none"
      : "identity";

  const bindingAlgorithmValue = firstDefined(record, [
    "bindingAlgorithm",
    "bindAlgorithm",
  ]) ?? firstDefined(bindingRecord ?? {}, ["algorithm", "alg"]);
  const bindingAlgorithm =
    canonicalAlgorithm(
      (bindingAlgorithmValue as AlgorithmName | undefined) ?? "sha256",
    ) ?? "sha256";
  const bindingDomainValue = firstDefined(record, [
    "bindingDomain",
    "bindDomain",
  ]) ?? firstDefined(bindingRecord ?? {}, ["domain", "domainTag"]);
  const bindingDomain =
    bindingDomainValue === undefined
      ? `gsb.placeholder.${id}`
      : normalizeDomain(bindingDomainValue, "placeholder binding");
  const bindingDigestValue = firstDefined(record, [
    "bindingDigest",
    "bindDigest",
    "valueDigest",
  ]) ?? firstDefined(bindingRecord ?? {}, ["digest", "value", "hash"]);

  return {
    id,
    offset: offset as number,
    length: length as number,
    ...(token === undefined ? {} : { token }),
    ...(field === undefined ? {} : { field }),
    ...(value === undefined ? {} : { value }),
    ...(encoding === undefined ? {} : { encoding }),
    binding,
    bindingAlgorithm,
    bindingDomain,
    ...(bindingDigestValue === undefined
      ? {}
      : {
          bindingDigest: normalizeDigestValue(
            bindingDigestValue,
            bindingAlgorithm,
          ),
        }),
  };
}

function normalizePlaceholders(input: unknown): PlaceholderSpec[] {
  if (input === undefined) {
    return [];
  }
  let raw: unknown[];
  if (Array.isArray(input)) {
    raw = input;
  } else if (input !== null && typeof input === "object") {
    raw = Object.entries(input as JsonObject).map(([id, spec]) => {
      if (spec !== null && typeof spec === "object" && !Array.isArray(spec)) {
        const record = spec as JsonObject;
        return record.id === undefined ? { ...record, id } : record;
      }
      return spec;
    });
  } else {
    throw new PipelineError(
      ErrorCode.INVALID_MANIFEST,
      "placeholders must be an array or object map",
      { placeholdersType: typeof input },
    );
  }

  const specs = raw.map((item, index) => normalizePlaceholder(item, index));
  const sorted = [...specs].sort((a, b) => a.offset - b.offset);
  let previousEnd = 0;
  for (const spec of sorted) {
    if (spec.offset < previousEnd) {
      throw new PipelineError(
        ErrorCode.PLACEHOLDER_RANGE,
        "placeholder spans must not overlap",
        { id: spec.id, offset: spec.offset, previousEnd },
      );
    }
    previousEnd = spec.offset + spec.length;
  }
  return sorted;
}

function normalizeShardLayout(input: unknown): ShardLayout | undefined {
  if (input === undefined) {
    return undefined;
  }
  if (typeof input === "number") {
    if (!Number.isSafeInteger(input) || input < 0) {
      throw new PipelineError(
        ErrorCode.INVALID_MANIFEST,
        "shard count must be a non-negative safe integer",
        { count: input },
      );
    }
    return { count: input };
  }
  if (Array.isArray(input)) {
    if (input.every((item) => typeof item === "number")) {
      const lengths = input.map((item) =>
        normalizeLength(item, "shard layout"),
      );
      return { count: lengths.length, lengths };
    }
    const shards: ShardSpec[] = input.map((item, index) => {
      if (typeof item === "number") {
        return { index, length: normalizeLength(item, "shard layout") };
      }
      const record = asObject(item, "shard spec");
      const indexValue = firstDefined(record, [
        "index",
        "i",
        "sequence",
        "seq",
        "order",
      ]);
      const shardIndex =
        indexValue === undefined ? index : (indexValue as number);
      if (!Number.isSafeInteger(shardIndex) || shardIndex < 0) {
        throw new PipelineError(
          ErrorCode.INVALID_MANIFEST,
          "shard index must be a non-negative safe integer",
          { shardIndex },
        );
      }
      const lengthValue = firstDefined(record, ["length", "size", "byteLength"]);
      const algorithmValue = firstDefined(record, [
        "algorithm",
        "alg",
        "hash",
      ]);
      const digestValue = firstDefined(record, ["digest", "hash"]);
      const prefixedAlgorithm =
        typeof digestValue === "string"
          ? parsePrefixedAlgorithm(digestValue)
          : undefined;
      const algorithm =
        algorithmValue === undefined
          ? prefixedAlgorithm === undefined
            ? digestValue === undefined
              ? undefined
              : "sha256"
            : canonicalAlgorithm(prefixedAlgorithm)
          : canonicalAlgorithm(algorithmValue as AlgorithmName);
      if (
        (algorithmValue !== undefined || digestValue !== undefined) &&
        algorithm === null
      ) {
        throw new PipelineError(
          ErrorCode.INVALID_MANIFEST,
          "shard spec has an unsupported algorithm",
          { shardIndex, algorithm: algorithmValue },
        );
      }
      return {
        index: shardIndex,
        ...(lengthValue === undefined
          ? {}
          : { length: normalizeLength(lengthValue, "shard spec") }),
        ...(algorithm === undefined || algorithm === null
          ? {}
          : { algorithm }),
        ...(digestValue === undefined || algorithm === undefined || algorithm === null
          ? {}
          : { digest: normalizeDigestValue(digestValue, algorithm) }),
      };
    });
    shards.sort((a, b) => a.index - b.index);
    const count =
      shards.length === 0 ? 0 : Math.max(...shards.map((shard) => shard.index)) + 1;
    return { count, shards };
  }
  const record = asObject(input, "shard layout");
  const countValue = firstDefined(record, ["count", "total", "segments"]);
  const lengthsValue = firstDefined(record, ["lengths", "sizes"]);
  const shardsValue = firstDefined(record, ["shards", "items"]);
  const layout = normalizeShardLayout(
    shardsValue ??
      (lengthsValue !== undefined
        ? (lengthsValue as unknown[])
        : countValue),
  );
  return layout;
}

function normalizeDecodedStage(input: unknown): DecodedStage {
  if (input === undefined) {
    return "auto";
  }
  const value = typeof input === "string" ? input.trim().toLowerCase() : input;
  if (value === "auto" || value === "raw" || value === "final") {
    return value;
  }
  if (value === "either" || value === "both") {
    return "auto";
  }
  if (value === "decoded" || value === "pre" || value === "before") {
    return "raw";
  }
  if (value === "after" || value === "published" || value === "post") {
    return "final";
  }
  throw new PipelineError(
    ErrorCode.INVALID_MANIFEST,
    "decodedStage must be auto, raw, or final",
    { decodedStage: input },
  );
}

function normalizeCommitment(value: unknown): string | undefined {
  if (value === undefined) {
    return undefined;
  }
  if (typeof value === "string") {
    return value;
  }
  if (value instanceof Uint8Array) {
    return Buffer.from(value).toString("hex");
  }
  if (value !== null && typeof value === "object" && !Array.isArray(value)) {
    const record = value as JsonObject;
    const algorithm = firstDefined(record, ["algorithm", "alg", "hash"]);
    const digest = firstDefined(record, ["digest", "value", "hex"]);
    if (
      (typeof algorithm === "string" || algorithm === undefined) &&
      typeof digest === "string"
    ) {
      return algorithm === undefined
        ? digest
        : `${canonicalAlgorithm(algorithm) ?? algorithm}:${digest}`;
    }
  }
  throw new PipelineError(
    ErrorCode.INVALID_MANIFEST,
    "contentCommitment must be a string, bytes, or digest entry",
  );
}

export function normalizeManifest(input: unknown): AssetManifest {
  const record = asObject(input, "manifest");
  const transportInput = firstDefined(record, [
    "transport",
    "transportRepresentation",
    "transportRep",
    "wire",
    "encoded",
  ]);
  const decodedInput = firstDefined(record, [
    "decoded",
    "decodedRepresentation",
    "decodedRep",
    "content",
  ]);
  const finalInput = firstDefined(record, [
    "final",
    "finalRepresentation",
    "published",
  ]);
  if (transportInput === undefined) {
    throw new PipelineError(
      ErrorCode.INVALID_MANIFEST,
      "manifest is missing the transport representation",
    );
  }
  if (decodedInput === undefined) {
    throw new PipelineError(
      ErrorCode.INVALID_MANIFEST,
      "manifest is missing the decoded representation",
    );
  }

  const transport = normalizeRepresentation(
    transportInput,
    "transport",
    "transport representation",
  );
  const decoded = normalizeRepresentation(
    decodedInput,
    "decoded",
    "decoded representation",
  );
  const final =
    finalInput === undefined
      ? undefined
      : normalizeRepresentation(finalInput, "final", "final representation");

  const idValue = firstDefined(record, ["id", "assetId", "name"]);
  const versionValue = firstDefined(record, ["version", "v"]);
  const contentCommitmentValue = firstDefined(record, [
    "contentCommitment",
    "contentDigest",
    "commitment",
  ]);
  const metadataValue = firstDefined(record, ["metadata", "meta"]);

  return {
    ...(typeof idValue === "string" ? { id: idValue } : {}),
    ...(versionValue === undefined
      ? {}
      : { version: versionValue as string | number }),
    transport,
    decoded,
    ...(final === undefined ? {} : { final }),
    decodedStage: normalizeDecodedStage(
      firstDefined(record, ["decodedStage", "stage", "decodedMode"]),
    ),
    transformChain: normalizeTransformChain(
      firstDefined(record, [
        "transformChain",
        "transform",
        "transforms",
        "chain",
        "pipeline",
      ]),
    ),
    placeholders: normalizePlaceholders(
      firstDefined(record, [
        "placeholders",
        "substitutions",
        "replacements",
        "replacement",
      ]),
    ),
    ...(normalizeShardLayout(
      firstDefined(record, ["shards", "segments", "shardLayout"]),
    ) === undefined
      ? {}
      : {
          shards: normalizeShardLayout(
            firstDefined(record, ["shards", "segments", "shardLayout"]),
          ),
        }),
    ...(normalizeCommitment(contentCommitmentValue) === undefined
      ? {}
      : { contentCommitment: normalizeCommitment(contentCommitmentValue) }),
    ...(metadataValue === undefined
      ? {}
      : { metadata: asObject(metadataValue, "manifest metadata") }),
  };
}

export function manifestFingerprint(manifest: AssetManifest): string {
  return digestHex("sha256", "gsb.manifest.v1", canonicalJson(manifest));
}

function strongestDigest(
  groups: readonly DigestGroup[],
): { algorithm: HashAlgorithm; digest: string } | null {
  let best: { algorithm: HashAlgorithm; digest: string } | null = null;
  for (const group of groups) {
    for (const entry of group.entries) {
      const algorithm = canonicalAlgorithm(entry.algorithm);
      if (algorithm === null) {
        continue;
      }
      if (
        best === null ||
        ALGORITHM_STRENGTH.indexOf(algorithm) <
          ALGORITHM_STRENGTH.indexOf(best.algorithm)
      ) {
        best = { algorithm, digest: entry.digest };
      } else if (
        algorithm === best.algorithm &&
        entry.digest < best.digest
      ) {
        best = { algorithm, digest: entry.digest };
      }
    }
  }
  return best;
}

export function transportCommitment(manifest: AssetManifest): string {
  const digest = strongestDigest(manifest.transport.groups);
  if (digest === null) {
    throw new PipelineError(
      ErrorCode.INVALID_MANIFEST,
      "transport representation has no digests",
    );
  }
  return `${digest.algorithm}:${digest.digest}`;
}

export function contentCommitmentFromManifest(manifest: AssetManifest): string {
  if (manifest.contentCommitment !== undefined) {
    return manifest.contentCommitment;
  }
  const digest =
    strongestDigest(manifest.decoded.groups) ??
    (manifest.final === undefined
      ? null
      : strongestDigest(manifest.final.groups));
  if (digest === null) {
    throw new PipelineError(
      ErrorCode.INVALID_MANIFEST,
      "decoded representation has no digests",
    );
  }
  return `${digest.algorithm}:${digest.digest}`;
}
