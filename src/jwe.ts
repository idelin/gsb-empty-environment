import { decodeBase64UrlStrict } from "./base64url.ts";
import { JweSecurityError } from "./errors.ts";
import {
  isStrictJsonObject,
  parseStrictJson,
  type StrictJsonObject,
} from "./json.ts";
import {
  STANDARD_JWE_HEADER_PARAMETERS,
  resolveJwePolicy,
  type JwePolicy,
  type ResolvedJwePolicy,
} from "./policy.ts";
import {
  validateEcdhEpk,
  type ValidatedEcdhEpk,
} from "./p256.ts";

export interface ParsedJwe {
  readonly token: string;
  readonly segments: readonly string[];
  readonly protectedHeaderSegment: string;
  readonly protectedHeaderBytes: Buffer;
  readonly protectedHeader: StrictJsonObject;
  /**
   * AEAD additional authenticated data. RFC 7516 uses the exact ASCII bytes of
   * the original protected-header segment, not a reserialized JSON object.
   */
  readonly aad: Buffer;
  readonly encryptedKeySegment: string;
  readonly encryptedKey: Buffer;
  readonly ivSegment: string;
  readonly iv: Buffer;
  readonly ciphertextSegment: string;
  readonly ciphertext: Buffer;
  readonly tagSegment: string;
  readonly tag: Buffer;
  readonly policy: ResolvedJwePolicy;
}

export interface ValidatedJwe extends ParsedJwe {
  /** Convenience alias for protectedHeader. */
  readonly header: StrictJsonObject;
  readonly algorithm: string;
  readonly encryption: string;
  readonly compression: "DEF" | null;
  readonly ecdhEpk?: ValidatedEcdhEpk;
  readonly apu?: Buffer;
  readonly apv?: Buffer;
  readonly pbes2?: {
    readonly iterations: number;
    readonly salt: Buffer;
  };
}

const decoder = new TextDecoder("utf-8", { fatal: true });

function parseProtectedHeader(
  segment: string,
  policy: ResolvedJwePolicy,
): {
  bytes: Buffer;
  header: StrictJsonObject;
} {
  const bytes = decodeBase64UrlStrict(segment, {
    name: "protected header",
  });
  if (bytes.length > policy.maxProtectedHeaderBytes) {
    throw new JweSecurityError(
      "JWE_HEADER_TOO_LARGE",
      "protected header exceeds maxProtectedHeaderBytes",
      {
        actualBytes: bytes.length,
        maxProtectedHeaderBytes: policy.maxProtectedHeaderBytes,
      },
    );
  }

  let text: string;
  try {
    text = decoder.decode(bytes);
  } catch {
    throw new JweSecurityError(
      "JWE_HEADER_UTF8_INVALID",
      "protected header is not valid UTF-8",
    );
  }

  let parsed;
  try {
    parsed = parseStrictJson(text);
  } catch (error) {
    if (error instanceof JweSecurityError) {
      throw error;
    }
    throw new JweSecurityError(
      "JWE_HEADER_JSON_INVALID",
      "protected header is not valid JSON",
    );
  }

  if (!isStrictJsonObject(parsed)) {
    throw new JweSecurityError(
      "JWE_HEADER_NOT_OBJECT",
      "the protected header must be a JSON object",
    );
  }
  return { bytes, header: parsed };
}

export function parseCompactJwe(
  token: string,
  policyInput: JwePolicy = {},
): ParsedJwe {
  if (typeof token !== "string") {
    throw new JweSecurityError(
      "JWE_TOKEN_TYPE",
      "JWE compact serialization must be a string",
    );
  }
  const policy = resolveJwePolicy(policyInput);
  const tokenBytes = Buffer.byteLength(token, "utf8");
  if (tokenBytes > policy.maxTokenBytes) {
    throw new JweSecurityError(
      "JWE_TOKEN_TOO_LARGE",
      "JWE exceeds maxTokenBytes",
      { actualBytes: tokenBytes, maxTokenBytes: policy.maxTokenBytes },
    );
  }

  const segments = token.split(".");
  if (segments.length !== 5) {
    throw new JweSecurityError(
      "JWE_SEGMENT_COUNT",
      "JWE compact serialization must contain exactly five segments",
      { segmentCount: segments.length },
    );
  }

  const [
    protectedHeaderSegment,
    encryptedKeySegment,
    ivSegment,
    ciphertextSegment,
    tagSegment,
  ] = segments as [string, string, string, string, string];

  const protectedHeader = parseProtectedHeader(protectedHeaderSegment, policy);

  const encryptedKey = decodeBase64UrlStrict(encryptedKeySegment, {
    name: "encrypted key",
    allowEmpty: true,
  });
  if (encryptedKey.length > policy.maxEncryptedKeyBytes) {
    throw new JweSecurityError(
      "JWE_ENCRYPTED_KEY_INVALID",
      "encrypted key exceeds maxEncryptedKeyBytes",
      { actualBytes: encryptedKey.length },
    );
  }

  const iv = decodeBase64UrlStrict(ivSegment, {
    name: "initialization vector",
    invalidCode: "JWE_IV_INVALID",
    nonCanonicalCode: "JWE_IV_INVALID",
  });
  if (iv.length > policy.maxIvBytes) {
    throw new JweSecurityError(
      "JWE_IV_INVALID",
      "initialization vector exceeds maxIvBytes",
      { actualBytes: iv.length },
    );
  }

  const ciphertext = decodeBase64UrlStrict(ciphertextSegment, {
    name: "ciphertext",
    allowEmpty: true,
    invalidCode: "JWE_CIPHERTEXT_INVALID",
    nonCanonicalCode: "JWE_CIPHERTEXT_INVALID",
  });
  if (ciphertext.length > policy.maxCiphertextBytes) {
    throw new JweSecurityError(
      "JWE_CIPHERTEXT_INVALID",
      "ciphertext exceeds maxCiphertextBytes",
      { actualBytes: ciphertext.length },
    );
  }

  const tag = decodeBase64UrlStrict(tagSegment, {
    name: "authentication tag",
    invalidCode: "JWE_TAG_INVALID",
    nonCanonicalCode: "JWE_TAG_INVALID",
  });
  if (tag.length > policy.maxTagBytes) {
    throw new JweSecurityError(
      "JWE_TAG_INVALID",
      "authentication tag exceeds maxTagBytes",
      { actualBytes: tag.length },
    );
  }

  const aad = Buffer.from(protectedHeaderSegment, "ascii");

  return Object.freeze({
    token,
    segments: Object.freeze([...segments]),
    protectedHeaderSegment,
    protectedHeaderBytes: protectedHeader.bytes,
    protectedHeader: protectedHeader.header,
    aad,
    encryptedKeySegment,
    encryptedKey,
    ivSegment,
    iv,
    ciphertextSegment,
    ciphertext,
    tagSegment,
    tag,
    policy,
  });
}

function headerString(
  header: StrictJsonObject,
  name: string,
  missingCode:
    | "JWE_HEADER_ALG_MISSING"
    | "JWE_HEADER_ENC_MISSING",
): string {
  const value = header[name];
  if (typeof value !== "string" || value.length === 0) {
    throw new JweSecurityError(
      missingCode,
      `protected header must contain a non-empty string ${name}`,
    );
  }
  return value;
}

function validateCriticalHeaders(
  header: StrictJsonObject,
  policy: ResolvedJwePolicy,
): void {
  if (!Object.prototype.hasOwnProperty.call(header, "crit")) {
    return;
  }

  const crit = header.crit;
  if (!Array.isArray(crit) || crit.length === 0) {
    throw new JweSecurityError(
      "JWE_CRIT_INVALID",
      "crit must be a non-empty array of registered extension names",
    );
  }

  const seen = new Set<string>();
  for (const entry of crit) {
    if (typeof entry !== "string" || entry.length === 0) {
      throw new JweSecurityError(
        "JWE_CRIT_INVALID",
        "crit entries must be non-empty strings",
      );
    }
    if (seen.has(entry)) {
      throw new JweSecurityError(
        "JWE_CRIT_DUPLICATE",
        `crit contains duplicate extension ${JSON.stringify(entry)}`,
        { extension: entry },
      );
    }
    seen.add(entry);

    if (STANDARD_JWE_HEADER_PARAMETERS.has(entry)) {
      throw new JweSecurityError(
        "JWE_CRIT_STANDARD_HEADER",
        `crit must not list standard header parameter ${JSON.stringify(entry)}`,
        { extension: entry },
      );
    }
    if (!policy.registeredCriticalExtensions.includes(entry)) {
      throw new JweSecurityError(
        "JWE_CRIT_UNREGISTERED",
        `crit lists an unregistered extension ${JSON.stringify(entry)}`,
        { extension: entry },
      );
    }
    if (!Object.prototype.hasOwnProperty.call(header, entry)) {
      throw new JweSecurityError(
        "JWE_CRIT_EXTENSION_MISSING",
        `crit extension ${JSON.stringify(entry)} is not present in the header`,
        { extension: entry },
      );
    }
  }
}

function validatePbes2(
  header: StrictJsonObject,
  policy: ResolvedJwePolicy,
): ValidatedJwe["pbes2"] {
  const p2c = header.p2c;
  if (p2c === undefined) {
    throw new JweSecurityError(
      "JWE_PBES2_P2C_MISSING",
      "PBES2 requires p2c",
    );
  }
  if (
    typeof p2c !== "number" ||
    !Number.isSafeInteger(p2c) ||
    p2c < policy.minPbes2Iterations ||
    p2c > policy.maxPbes2Iterations
  ) {
    throw new JweSecurityError(
      "JWE_PBES2_P2C_INVALID",
      "PBES2 p2c is outside the configured iteration bounds",
      {
        p2c: typeof p2c === "number" ? p2c : undefined,
        minIterations: policy.minPbes2Iterations,
        maxIterations: policy.maxPbes2Iterations,
      },
    );
  }

  const p2sValue = header.p2s;
  if (typeof p2sValue !== "string" || p2sValue.length === 0) {
    throw new JweSecurityError(
      "JWE_PBES2_P2S_MISSING",
      "PBES2 requires a Base64URL-encoded p2s salt",
    );
  }
  let salt: Buffer;
  try {
    salt = decodeBase64UrlStrict(p2sValue, {
      name: "PBES2 p2s",
      invalidCode: "JWE_PBES2_P2S_INVALID",
      nonCanonicalCode: "JWE_PBES2_P2S_INVALID",
    });
  } catch (error) {
    if (error instanceof JweSecurityError) {
      throw new JweSecurityError(
        "JWE_PBES2_P2S_INVALID",
        "PBES2 p2s is not canonical Base64URL",
      );
    }
    throw error;
  }

  if (salt.length < policy.minPbes2SaltBytes) {
    throw new JweSecurityError(
      "JWE_PBES2_SALT_TOO_SHORT",
      "PBES2 p2s salt is shorter than the configured minimum",
      {
        actualBytes: salt.length,
        minSaltBytes: policy.minPbes2SaltBytes,
      },
    );
  }

  return Object.freeze({ iterations: p2c, salt });
}

function optionalPartyInfo(
  header: StrictJsonObject,
  name: "apu" | "apv",
): Buffer | undefined {
  const value = header[name];
  if (value === undefined) {
    return undefined;
  }
  if (typeof value !== "string") {
    throw new JweSecurityError(
      "JWE_ECDH_APU_INVALID",
      `ECDH ${name} must be a Base64URL string`,
    );
  }
  try {
    return decodeBase64UrlStrict(value, {
      name: `ECDH ${name}`,
      allowEmpty: true,
      invalidCode: name === "apu" ? "JWE_ECDH_APU_INVALID" : "JWE_ECDH_APV_INVALID",
      nonCanonicalCode:
        name === "apu" ? "JWE_ECDH_APU_INVALID" : "JWE_ECDH_APV_INVALID",
    });
  } catch (error) {
    if (error instanceof JweSecurityError) {
      throw new JweSecurityError(
        name === "apu" ? "JWE_ECDH_APU_INVALID" : "JWE_ECDH_APV_INVALID",
        `ECDH ${name} must be canonical Base64URL`,
      );
    }
    throw error;
  }
}

export function validateJwe(
  token: string,
  policyInput: JwePolicy = {},
): ValidatedJwe {
  const parsed = parseCompactJwe(token, policyInput);
  const { protectedHeader: header, policy } = parsed;

  const algorithm = headerString(header, "alg", "JWE_HEADER_ALG_MISSING");
  if (!policy.allowedAlgorithms.includes(algorithm)) {
    throw new JweSecurityError(
      "JWE_ALGORITHM_NOT_ALLOWED",
      `key management algorithm ${JSON.stringify(algorithm)} is not allowed`,
      { algorithm, allowedAlgorithms: policy.allowedAlgorithms },
    );
  }

  const encryption = headerString(header, "enc", "JWE_HEADER_ENC_MISSING");
  if (!policy.allowedEncryptions.includes(encryption)) {
    throw new JweSecurityError(
      "JWE_ENCRYPTION_NOT_ALLOWED",
      `content encryption algorithm ${JSON.stringify(encryption)} is not allowed`,
      { encryption, allowedEncryptions: policy.allowedEncryptions },
    );
  }

  let compression: "DEF" | null = null;
  if (Object.prototype.hasOwnProperty.call(header, "zip")) {
    if (header.zip !== "DEF") {
      throw new JweSecurityError(
        "JWE_ZIP_UNSUPPORTED",
        'zip must be absent or exactly "DEF"',
      );
    }
    compression = "DEF";
  }

  validateCriticalHeaders(header, policy);

  let pbes2: ValidatedJwe["pbes2"];
  if (algorithm.startsWith("PBES2")) {
    pbes2 = validatePbes2(header, policy);
  }

  let ecdhEpk: ValidatedEcdhEpk | undefined;
  let apu: Buffer | undefined;
  let apv: Buffer | undefined;
  if (algorithm.startsWith("ECDH-ES")) {
    if (!Object.prototype.hasOwnProperty.call(header, "epk")) {
      throw new JweSecurityError(
        "JWE_ECDH_EPK_MISSING",
        "ECDH-ES requires an epk public key",
      );
    }
    ecdhEpk = validateEcdhEpk(header.epk);
    apu = optionalPartyInfo(header, "apu");
    apv = optionalPartyInfo(header, "apv");
  }

  return Object.freeze({
    ...parsed,
    header,
    algorithm,
    encryption,
    compression,
    ...(ecdhEpk === undefined ? {} : { ecdhEpk }),
    ...(apu === undefined ? {} : { apu }),
    ...(apv === undefined ? {} : { apv }),
    ...(pbes2 === undefined ? {} : { pbes2 }),
  });
}

export { validateJwe as inspectJwe, validateJwe as admitJwe };
export { STANDARD_JWE_HEADER_PARAMETERS as STANDARD_JWE_HEADERS };
