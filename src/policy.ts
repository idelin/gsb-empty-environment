import { JweSecurityError } from "./errors.ts";

export const DEFAULT_ALLOWED_ALGORITHMS = Object.freeze([
  "dir",
  "A128KW",
  "A192KW",
  "A256KW",
  "ECDH-ES",
  "ECDH-ES+A128KW",
  "ECDH-ES+A192KW",
  "ECDH-ES+A256KW",
  "PBES2-HS256+A128KW",
  "PBES2-HS384+A192KW",
  "PBES2-HS512+A256KW",
  "RSA-OAEP",
  "RSA-OAEP-256",
]);

export const DEFAULT_ALLOWED_ENCRYPTIONS = Object.freeze([
  "A128GCM",
  "A192GCM",
  "A256GCM",
  "A128CBC-HS256",
  "A192CBC-HS384",
  "A256CBC-HS512",
]);

export const PBES2_ALGORITHMS = new Set([
  "PBES2-HS256+A128KW",
  "PBES2-HS384+A192KW",
  "PBES2-HS512+A256KW",
]);

export const ECDH_ES_ALGORITHMS = new Set([
  "ECDH-ES",
  "ECDH-ES+A128KW",
  "ECDH-ES+A192KW",
  "ECDH-ES+A256KW",
]);

export const STANDARD_JWE_HEADER_PARAMETERS: ReadonlySet<string> = new Set([
  "alg",
  "enc",
  "zip",
  "jku",
  "jwk",
  "kid",
  "x5u",
  "x5c",
  "x5t",
  "x5t#S256",
  "typ",
  "cty",
  "crit",
  "epk",
  "p2s",
  "p2c",
  "apu",
  "apv",
  "iv",
  "tag",
]);

export interface JwePbes2Policy {
  /** Inclusive lower bound for PBES2 p2c. */
  minIterations?: number;
  /** Alias for minIterations. */
  minIter?: number;
  /** Inclusive upper bound for PBES2 p2c. */
  maxIterations?: number;
  /** Alias for maxIterations. */
  maxIter?: number;
  /** Minimum decoded p2s length in octets. */
  minSaltBytes?: number;
}

export interface JwePolicy {
  readonly allowedAlgorithms?: readonly string[];
  /** Alias for allowedAlgorithms. */
  readonly allowedAlg?: readonly string[];
  readonly allowedEncryptions?: readonly string[];
  /** Alias for allowedEncryptions. */
  readonly allowedEnc?: readonly string[];
  readonly pbes2?: JwePbes2Policy;
  /** Alias configuration: [minIter, maxIter]. */
  readonly p2cRange?: readonly [number, number];
  readonly minPbes2Iterations?: number;
  readonly maxPbes2Iterations?: number;
  readonly minPbes2SaltBytes?: number;
  /** Additional top-level aliases for the PBES2 bounds. */
  readonly minIterations?: number;
  readonly maxIterations?: number;
  readonly minSaltBytes?: number;
  /** Top-level aliases for the PBES2 iteration bounds. */
  readonly minIter?: number;
  readonly maxIter?: number;
  readonly registeredCriticalExtensions?: readonly string[];
  /** Alias for registeredCriticalExtensions. */
  readonly allowedCriticalExtensions?: readonly string[];
  readonly maxTokenBytes?: number;
  readonly maxProtectedHeaderBytes?: number;
  readonly maxEncryptedKeyBytes?: number;
  readonly maxIvBytes?: number;
  readonly maxCiphertextBytes?: number;
  readonly maxTagBytes?: number;
  readonly maxPlaintextBytes?: number;
  /** Alias for maxPlaintextBytes. */
  readonly maxDecompressedBytes?: number;
  /** Maximum decompressed-to-compressed byte ratio for zip="DEF". */
  readonly maxRatio?: number;
  readonly maxCompressionRatio?: number;
}

export interface ResolvedJwePolicy {
  readonly allowedAlgorithms: readonly string[];
  readonly allowedEncryptions: readonly string[];
  readonly minPbes2Iterations: number;
  readonly maxPbes2Iterations: number;
  readonly minPbes2SaltBytes: number;
  readonly registeredCriticalExtensions: readonly string[];
  readonly maxTokenBytes: number;
  readonly maxProtectedHeaderBytes: number;
  readonly maxEncryptedKeyBytes: number;
  readonly maxIvBytes: number;
  readonly maxCiphertextBytes: number;
  readonly maxTagBytes: number;
  readonly maxPlaintextBytes: number;
  readonly maxRatio: number;
}

const DEFAULT_POLICY: ResolvedJwePolicy = Object.freeze({
  allowedAlgorithms: DEFAULT_ALLOWED_ALGORITHMS,
  allowedEncryptions: DEFAULT_ALLOWED_ENCRYPTIONS,
  minPbes2Iterations: 10_000,
  maxPbes2Iterations: 100_000,
  minPbes2SaltBytes: 8,
  registeredCriticalExtensions: Object.freeze([]),
  maxTokenBytes: 1_048_576,
  maxProtectedHeaderBytes: 16_384,
  maxEncryptedKeyBytes: 65_536,
  maxIvBytes: 64,
  maxCiphertextBytes: 1_048_576,
  maxTagBytes: 64,
  maxPlaintextBytes: 1_048_576,
  maxRatio: 100,
});

function policyError(message: string, details?: Record<string, unknown>) {
  return new JweSecurityError("JWE_POLICY_INVALID", message, details);
}

function integerOption(
  value: unknown,
  name: string,
  fallback: number,
  minimum: number,
  maximum = Number.MAX_SAFE_INTEGER,
): number {
  if (value === undefined) {
    return fallback;
  }
  if (
    typeof value !== "number" ||
    !Number.isSafeInteger(value) ||
    value < minimum ||
    value > maximum
  ) {
    throw policyError(`${name} must be a safe integer in the allowed range`, {
      name,
      minimum,
      maximum,
    });
  }
  return value;
}

function stringList(
  value: unknown,
  name: string,
  fallback: readonly string[],
): readonly string[] {
  if (value === undefined) {
    return fallback;
  }
  if (
    !Array.isArray(value) ||
    value.some((entry) => typeof entry !== "string" || entry.length === 0)
  ) {
    throw policyError(`${name} must be an array of non-empty strings`, {
      name,
    });
  }
  return Object.freeze(Array.from(new Set(value as string[])));
}

export function resolveJwePolicy(policy: JwePolicy = {}): ResolvedJwePolicy {
  if (policy === null || typeof policy !== "object" || Array.isArray(policy)) {
    throw policyError("JWE policy must be an object");
  }

  const allowedAlgorithms = stringList(
    policy.allowedAlgorithms ?? policy.allowedAlg,
    "allowedAlgorithms",
    DEFAULT_ALLOWED_ALGORITHMS,
  );
  const allowedEncryptions = stringList(
    policy.allowedEncryptions ?? policy.allowedEnc,
    "allowedEncryptions",
    DEFAULT_ALLOWED_ENCRYPTIONS,
  );

  const pbes2 = policy.pbes2 ?? {};
  if (pbes2 === null || typeof pbes2 !== "object" || Array.isArray(pbes2)) {
    throw policyError("pbes2 policy must be an object");
  }

  let p2cRange: readonly [number, number] | undefined;
  if (policy.p2cRange !== undefined) {
    if (
      !Array.isArray(policy.p2cRange) ||
      policy.p2cRange.length !== 2 ||
      !Number.isSafeInteger(policy.p2cRange[0]) ||
      !Number.isSafeInteger(policy.p2cRange[1])
    ) {
      throw policyError("p2cRange must be [minIter, maxIter] safe integers");
    }
    p2cRange = [policy.p2cRange[0], policy.p2cRange[1]];
  }

  const minIterations = integerOption(
    pbes2.minIterations ??
      pbes2.minIter ??
      policy.minPbes2Iterations ??
      policy.minIterations ??
      policy.minIter ??
      p2cRange?.[0],
    "pbes2.minIterations",
    DEFAULT_POLICY.minPbes2Iterations,
    1,
  );
  const maxIterations = integerOption(
    pbes2.maxIterations ??
      pbes2.maxIter ??
      policy.maxPbes2Iterations ??
      policy.maxIterations ??
      policy.maxIter ??
      p2cRange?.[1],
    "pbes2.maxIterations",
    DEFAULT_POLICY.maxPbes2Iterations,
    1,
  );
  if (maxIterations < minIterations) {
    throw policyError("pbes2.maxIterations must be >= pbes2.minIterations", {
      minIterations,
      maxIterations,
    });
  }

  const minSaltBytes = integerOption(
    pbes2.minSaltBytes ??
      policy.minPbes2SaltBytes ??
      policy.minSaltBytes,
    "pbes2.minSaltBytes",
    DEFAULT_POLICY.minPbes2SaltBytes,
    1,
  );

  const registeredCriticalExtensions = stringList(
    policy.registeredCriticalExtensions ??
      policy.allowedCriticalExtensions,
    "registeredCriticalExtensions",
    DEFAULT_POLICY.registeredCriticalExtensions,
  );
  for (const extension of registeredCriticalExtensions) {
    if (STANDARD_JWE_HEADER_PARAMETERS.has(extension)) {
      throw policyError(
        "registeredCriticalExtensions must not contain standard JOSE header names",
        { extension },
      );
    }
  }

  const maxRatio = policy.maxRatio ?? policy.maxCompressionRatio;
  if (
    maxRatio !== undefined &&
    (typeof maxRatio !== "number" ||
      !Number.isFinite(maxRatio) ||
      maxRatio <= 0)
  ) {
    throw policyError("maxRatio must be a finite number greater than zero");
  }

  return Object.freeze({
    allowedAlgorithms,
    allowedEncryptions,
    minPbes2Iterations: minIterations,
    maxPbes2Iterations: maxIterations,
    minPbes2SaltBytes: minSaltBytes,
    registeredCriticalExtensions,
    maxTokenBytes: integerOption(
      policy.maxTokenBytes,
      "maxTokenBytes",
      DEFAULT_POLICY.maxTokenBytes,
      1,
    ),
    maxProtectedHeaderBytes: integerOption(
      policy.maxProtectedHeaderBytes,
      "maxProtectedHeaderBytes",
      DEFAULT_POLICY.maxProtectedHeaderBytes,
      1,
    ),
    maxEncryptedKeyBytes: integerOption(
      policy.maxEncryptedKeyBytes,
      "maxEncryptedKeyBytes",
      DEFAULT_POLICY.maxEncryptedKeyBytes,
      0,
    ),
    maxIvBytes: integerOption(
      policy.maxIvBytes,
      "maxIvBytes",
      DEFAULT_POLICY.maxIvBytes,
      1,
    ),
    maxCiphertextBytes: integerOption(
      policy.maxCiphertextBytes,
      "maxCiphertextBytes",
      DEFAULT_POLICY.maxCiphertextBytes,
      1,
    ),
    maxTagBytes: integerOption(
      policy.maxTagBytes,
      "maxTagBytes",
      DEFAULT_POLICY.maxTagBytes,
      1,
    ),
    maxPlaintextBytes: integerOption(
      policy.maxPlaintextBytes ?? policy.maxDecompressedBytes,
      "maxPlaintextBytes",
      DEFAULT_POLICY.maxPlaintextBytes,
      0,
    ),
    maxRatio: maxRatio ?? DEFAULT_POLICY.maxRatio,
  });
}
