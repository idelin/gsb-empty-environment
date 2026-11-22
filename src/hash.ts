import { createHash, type Hash } from "node:crypto";
import { ErrorCode, PipelineError } from "./errors.ts";

export type HashAlgorithm =
  | "sha512"
  | "sha384"
  | "sha256"
  | "sha1"
  | "md5";

export type AlgorithmName = HashAlgorithm | string;

/**
 * Ordered from strongest to weakest.  The list is deliberately small and
 * deterministic: the "strongest supported" algorithm in a digest group is the
 * first entry from this list that occurs in the group.
 */
export const ALGORITHM_STRENGTH: readonly HashAlgorithm[] = Object.freeze([
  "sha512",
  "sha384",
  "sha256",
  "sha1",
  "md5",
]);

const ALGORITHM_RANK = new Map<string, number>(
  ALGORITHM_STRENGTH.map((algorithm, index) => [algorithm, index]),
);

const ALGORITHM_ALIASES = new Map<string, HashAlgorithm>([
  ["sha512", "sha512"],
  ["sha-512", "sha512"],
  ["sha_512", "sha512"],
  ["sha2-512", "sha512"],
  ["sha2_512", "sha512"],
  ["sha384", "sha384"],
  ["sha-384", "sha384"],
  ["sha_384", "sha384"],
  ["sha2-384", "sha384"],
  ["sha2_384", "sha384"],
  ["sha256", "sha256"],
  ["sha-256", "sha256"],
  ["sha_256", "sha256"],
  ["sha2-256", "sha256"],
  ["sha2_256", "sha256"],
  ["sha1", "sha1"],
  ["sha-1", "sha1"],
  ["sha_1", "sha1"],
  ["md5", "md5"],
]);

export function canonicalAlgorithm(algorithm: AlgorithmName): HashAlgorithm | null {
  if (typeof algorithm !== "string") {
    return null;
  }
  return ALGORITHM_ALIASES.get(algorithm.trim().toLowerCase()) ?? null;
}

export function isSupportedAlgorithm(algorithm: AlgorithmName): boolean {
  return canonicalAlgorithm(algorithm) !== null;
}

export function algorithmStrength(algorithm: AlgorithmName): number {
  const canonical = canonicalAlgorithm(algorithm);
  if (canonical === null) {
    return -1;
  }
  return ALGORITHM_STRENGTH.length - (ALGORITHM_RANK.get(canonical) ?? 0);
}

export function compareAlgorithms(a: AlgorithmName, b: AlgorithmName): number {
  return algorithmStrength(b) - algorithmStrength(a);
}

/**
 * Returns the strongest algorithm that this runtime supports.  Unsupported
 * names are ignored, which is what makes algorithm agility work in a browser:
 * a manifest can contain a future algorithm and an older supported one without
 * forcing the old algorithm to be downgraded to.
 */
export function strongestSupportedAlgorithm(
  algorithms: Iterable<AlgorithmName>,
): HashAlgorithm | null {
  let strongest: HashAlgorithm | null = null;
  for (const algorithm of algorithms) {
    const canonical = canonicalAlgorithm(algorithm);
    if (canonical === null) {
      continue;
    }
    if (
      strongest === null ||
      algorithmStrength(canonical) > algorithmStrength(strongest)
    ) {
      strongest = canonical;
    }
  }
  return strongest;
}

const DOMAIN_MAGIC = Buffer.from("GSB-AUTH-V1\n", "utf8");

function uint32be(value: number): Buffer {
  const buffer = Buffer.allocUnsafe(4);
  buffer.writeUInt32BE(value >>> 0, 0);
  return buffer;
}

function uint64be(value: number): Buffer {
  if (!Number.isSafeInteger(value) || value < 0) {
    throw new PipelineError(
      ErrorCode.INVALID_ARGUMENT,
      "hash length must be a non-negative safe integer",
      { value },
    );
  }
  const buffer = Buffer.allocUnsafe(8);
  buffer.writeBigUInt64BE(BigInt(value), 0);
  return buffer;
}

function assertDomain(domain: unknown): string {
  if (typeof domain !== "string" || domain.length === 0) {
    throw new PipelineError(
      ErrorCode.INVALID_ARGUMENT,
      "a non-empty domain tag is required",
      { domain },
    );
  }
  return domain;
}

/**
 * Domain framing is length-prefixed so that no two (domain, content) pairs can
 * be made ambiguous by concatenation.  The total content length is appended as
 * well, which is why a streaming hasher does not need to know the length up
 * front.
 *
 *   SHA( "GSB-AUTH-V1\n" || u32be(len(domain)) || domain
 *        || content || u64be(len(content)) )
 */
export function domainFrame(domain: string): Buffer {
  const encoded = Buffer.from(assertDomain(domain), "utf8");
  return Buffer.concat([DOMAIN_MAGIC, uint32be(encoded.length), encoded]);
}

export type ByteSource = Uint8Array | ArrayBuffer | string;

export function asBuffer(data: ByteSource): Buffer {
  if (typeof data === "string") {
    return Buffer.from(data, "utf8");
  }
  if (data instanceof ArrayBuffer) {
    return Buffer.from(data);
  }
  if (!(data instanceof Uint8Array)) {
    throw new PipelineError(
      ErrorCode.INVALID_ARGUMENT,
      "expected a Uint8Array or string",
      { received: typeof data },
    );
  }
  return Buffer.from(data.buffer, data.byteOffset, data.byteLength);
}

export class StreamingHasher {
  readonly algorithm: HashAlgorithm;
  readonly domain: string;
  readonly expectedLength?: number;

  #hash: Hash;
  #length = 0;
  #finalized = false;
  #digestHex?: string;

  constructor(
    algorithm: AlgorithmName,
    domain: string,
    options: { expectedLength?: number } = {},
  ) {
    const canonical = canonicalAlgorithm(algorithm);
    if (canonical === null) {
      throw new PipelineError(
        ErrorCode.UNSUPPORTED_ALGORITHM,
        `unsupported hash algorithm: ${String(algorithm)}`,
        { algorithm },
      );
    }
    if (
      options.expectedLength !== undefined &&
      (!Number.isSafeInteger(options.expectedLength) || options.expectedLength < 0)
    ) {
      throw new PipelineError(
        ErrorCode.INVALID_ARGUMENT,
        "expectedLength must be a non-negative safe integer",
        { expectedLength: options.expectedLength },
      );
    }
    this.algorithm = canonical;
    this.domain = assertDomain(domain);
    this.expectedLength = options.expectedLength;
    this.#hash = createHash(canonical);
    this.#hash.update(domainFrame(this.domain));
  }

  update(data: ByteSource): this {
    if (this.#finalized) {
      throw new PipelineError(
        ErrorCode.INVALID_ARGUMENT,
        "cannot update a finalized streaming hasher",
        { algorithm: this.algorithm, domain: this.domain },
      );
    }
    const buffer = asBuffer(data);
    this.#length += buffer.length;
    this.#hash.update(buffer);
    return this;
  }

  get byteLength(): number {
    return this.#length;
  }

  get finalized(): boolean {
    return this.#finalized;
  }

  digest(encoding: "hex" | "base64" | "base64url" = "hex"): string {
    if (this.#digestHex === undefined) {
      if (
        this.expectedLength !== undefined &&
        this.#length !== this.expectedLength
      ) {
        throw new PipelineError(
          ErrorCode.LENGTH_MISMATCH,
          "streamed content length does not match the declared length",
          {
            expectedLength: this.expectedLength,
            actualLength: this.#length,
            domain: this.domain,
            algorithm: this.algorithm,
          },
        );
      }
      this.#hash.update(uint64be(this.#length));
      this.#digestHex = this.#hash.digest("hex");
      this.#finalized = true;
    }
    if (encoding === "hex") {
      return this.#digestHex;
    }
    return Buffer.from(this.#digestHex, "hex").toString(encoding);
  }

  digestHex(): string {
    return this.digest("hex");
  }
}

export function digestHex(
  algorithm: AlgorithmName,
  domain: string,
  data: ByteSource,
  expectedLength?: number,
): string {
  return new StreamingHasher(algorithm, domain, { expectedLength })
    .update(data)
    .digestHex();
}

export const hashBytes = digestHex;

/**
 * Streaming hasher over a transform chain.  This is the hasher used by the
 * asset pipeline for transport and decoded representations.
 */
export class MultiHasher {
  readonly hashers: StreamingHasher[];

  constructor(
    specs: Iterable<{
      algorithm: AlgorithmName;
      domain: string;
      expectedLength?: number;
    }>,
  ) {
    this.hashers = [...specs].map(
      ({ algorithm, domain, expectedLength }) =>
        new StreamingHasher(algorithm, domain, { expectedLength }),
    );
  }

  update(data: ByteSource): void {
    for (const hasher of this.hashers) {
      hasher.update(data);
    }
  }

  digests(): string[] {
    return this.hashers.map((hasher) => hasher.digestHex());
  }

  digestFor(algorithm: AlgorithmName, domain?: string): string | undefined {
    const canonical = canonicalAlgorithm(algorithm);
    if (canonical === null) {
      return undefined;
    }
    return this.hashers.find(
      (hasher) =>
        hasher.algorithm === canonical &&
        (domain === undefined || hasher.domain === domain),
    )?.digestHex();
  }
}

/**
 * Removes whitespace, an optional "algorithm:" prefix, and a leading "0x".
 * Digest values are otherwise kept as case-insensitive hexadecimal.
 */
export function normalizeDigestValue(
  digest: unknown,
  algorithm?: AlgorithmName,
  encoding?: "hex" | "base64" | "base64url",
): string {
  if (digest instanceof Uint8Array) {
    return Buffer.from(digest).toString("hex").toLowerCase();
  }
  if (
    Array.isArray(digest) &&
    digest.every(
      (byte) => Number.isInteger(byte) && (byte as number) >= 0 && (byte as number) <= 255,
    )
  ) {
    return Buffer.from(digest as number[]).toString("hex").toLowerCase();
  }
  if (typeof digest !== "string") {
    throw new PipelineError(
      ErrorCode.INVALID_REPRESENTATION,
      "a digest must be a hexadecimal string, base64 string, or byte array",
      { digestType: typeof digest },
    );
  }
  let value = digest.trim();
  if (/^[a-z0-9_-]+:/i.test(value)) {
    const [prefix, ...rest] = value.split(":");
    const prefixAlgorithm = canonicalAlgorithm(prefix);
    if (prefixAlgorithm !== null) {
      value = rest.join(":");
      algorithm = algorithm ?? prefixAlgorithm;
    }
  }
  if (value.startsWith("0x") || value.startsWith("0X")) {
    value = value.slice(2);
  }
  const requestedEncoding = encoding ?? "hex";
  if (requestedEncoding === "hex") {
    if (!/^[0-9a-f]+$/i.test(value) || value.length % 2 !== 0) {
      throw new PipelineError(
        ErrorCode.INVALID_REPRESENTATION,
        "digest is not valid hexadecimal",
        { digest: value, algorithm },
      );
    }
    return value.toLowerCase();
  }
  if (requestedEncoding !== "base64" && requestedEncoding !== "base64url") {
    throw new PipelineError(
      ErrorCode.INVALID_REPRESENTATION,
      "unsupported digest encoding",
      { encoding: requestedEncoding },
    );
  }
  const buffer = Buffer.from(value, requestedEncoding);
  if (buffer.length === 0) {
    throw new PipelineError(
      ErrorCode.INVALID_REPRESENTATION,
      "digest must not be empty",
      { digest: value, encoding: requestedEncoding },
    );
  }
  return buffer.toString("hex");
}

export function constantTimeEqualHex(a: string, b: string): boolean {
  const left = a.toLowerCase();
  const right = b.toLowerCase();
  if (left.length !== right.length) {
    return false;
  }
  let difference = 0;
  for (let index = 0; index < left.length; index += 1) {
    difference |= left.charCodeAt(index) ^ right.charCodeAt(index);
  }
  return difference === 0;
}
