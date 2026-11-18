import {
  constants as cryptoConstants,
  createHash,
  createHmac,
  createPublicKey,
  createSecretKey,
  verify as verifyWithCrypto,
} from "node:crypto";

const BASE64URL_RE = /^[A-Za-z0-9_-]+$/;
const DISCLOSURE_SEPARATOR = "~";
const DEFAULT_SD_ALG = "sha-256";

const HASH_ALGORITHMS = {
  "sha-256": { nodeName: "sha256", digestBytes: 32 },
  "sha-384": { nodeName: "sha384", digestBytes: 48 },
  "sha-512": { nodeName: "sha512", digestBytes: 64 },
} as const;

type SupportedSdAlg = keyof typeof HASH_ALGORITHMS;

const JWT_ALGORITHMS = {
  HS256: { family: "hmac", hash: "sha256" },
  HS384: { family: "hmac", hash: "sha384" },
  HS512: { family: "hmac", hash: "sha512" },
  RS256: { family: "rsa", hash: "sha256" },
  RS384: { family: "rsa", hash: "sha384" },
  RS512: { family: "rsa", hash: "sha512" },
  PS256: { family: "pss", hash: "sha256" },
  PS384: { family: "pss", hash: "sha384" },
  PS512: { family: "pss", hash: "sha512" },
  ES256: { family: "ecdsa", hash: "sha256" },
  ES384: { family: "ecdsa", hash: "sha384" },
  ES512: { family: "ecdsa", hash: "sha512" },
  EdDSA: { family: "eddsa", hash: null },
} as const;

export type JsonObject = Record<string, unknown>;
export type JwtKey = string | Uint8Array | JsonObject | { type: string };

export type SdJwtValidationErrorCode =
  | "INVALID_SD_JWT"
  | "INVALID_ISSUER_JWT"
  | "ISSUER_PAYLOAD_MISMATCH"
  | "ISSUER_SIGNATURE_INVALID"
  | "UNSUPPORTED_SD_ALG"
  | "ALGORITHM_MIXING"
  | "INVALID_SD_DIGEST"
  | "MALFORMED_SD_ARRAY"
  | "INVALID_DISCLOSURE"
  | "MISSING_SALT"
  | "DUPLICATE_SALT"
  | "DUPLICATE_DIGEST"
  | "FORBIDDEN_DISCLOSURE_NAME"
  | "DISCLOSURE_NAME_CONFLICT"
  | "DISCLOSURE_NOT_SIGNED_OR_PARENT_WITHHELD"
  | "KB_JWT_MISSING"
  | "KB_JWT_INVALID"
  | "KB_ALGORITHM_UNSUPPORTED"
  | "KB_SIGNATURE_INVALID"
  | "KB_SD_HASH_MISMATCH"
  | "KB_NONCE_MISMATCH"
  | "KB_AUD_MISMATCH"
  | "KB_EXPIRED"
  | "KB_NOT_YET_VALID";

export class SdJwtValidationError extends Error {
  readonly code: SdJwtValidationErrorCode;

  constructor(code: SdJwtValidationErrorCode, message: string) {
    super(message);
    this.name = "SdJwtValidationError";
    this.code = code;
  }
}

function fail(code: SdJwtValidationErrorCode, message: string): never {
  throw new SdJwtValidationError(code, message);
}

function isPlainObject(value: unknown): value is JsonObject {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function hasOwn(object: object, key: string): boolean {
  return Object.prototype.hasOwnProperty.call(object, key);
}

function defineJsonProperty(object: JsonObject, key: string, value: unknown): void {
  Object.defineProperty(object, key, {
    configurable: true,
    enumerable: true,
    writable: true,
    value,
  });
}

function normalizeSdAlg(value: unknown, fallback: SupportedSdAlg = DEFAULT_SD_ALG): SupportedSdAlg {
  if (value === undefined) {
    return fallback;
  }

  if (typeof value !== "string") {
    return fail("UNSUPPORTED_SD_ALG", "_sd_alg must be a string");
  }

  const normalized = value.toLowerCase();
  if (!hasOwn(HASH_ALGORITHMS, normalized)) {
    return fail("UNSUPPORTED_SD_ALG", `Unsupported _sd_alg: ${value}`);
  }

  return normalized as SupportedSdAlg;
}

function decodeBase64UrlStrict(value: string, code: SdJwtValidationErrorCode, label: string): Buffer {
  if (typeof value !== "string" || value.length === 0 || !BASE64URL_RE.test(value)) {
    fail(code, `${label} is not canonical base64url`);
  }

  const decoded = Buffer.from(value, "base64url");
  if (decoded.toString("base64url") !== value) {
    fail(code, `${label} is not canonical base64url`);
  }

  return decoded;
}

function decodeJsonBase64Url(value: string, code: SdJwtValidationErrorCode, label: string): unknown {
  const decoded = decodeBase64UrlStrict(value, code, label);
  let text: string;

  try {
    text = new TextDecoder("utf-8", { fatal: true }).decode(decoded);
  } catch {
    fail(code, `${label} is not valid UTF-8`);
  }

  try {
    return JSON.parse(text);
  } catch {
    return fail(code, `${label} is not valid JSON`);
  }
}

function digestBase64Url(value: string, alg: SupportedSdAlg): string {
  return createHash(HASH_ALGORITHMS[alg].nodeName).update(value, "utf8").digest("base64url");
}

function assertDigestForAlg(value: unknown, alg: SupportedSdAlg): asserts value is string {
  if (typeof value !== "string" || value.length === 0) {
    fail("INVALID_SD_DIGEST", "_sd entries must be non-empty base64url strings");
  }

  const decoded = decodeBase64UrlStrict(value, "INVALID_SD_DIGEST", "_sd digest");
  if (decoded.length !== HASH_ALGORITHMS[alg].digestBytes) {
    fail("ALGORITHM_MIXING", "An _sd digest does not match the size required by _sd_alg");
  }
}

function constantTimeStringEqual(left: string, right: string): boolean {
  const leftBuffer = Buffer.from(left, "utf8");
  const rightBuffer = Buffer.from(right, "utf8");
  const leftDigest = createHash("sha256").update(leftBuffer).digest();
  const rightDigest = createHash("sha256").update(rightBuffer).digest();
  return leftBuffer.length === rightBuffer.length && leftDigest.equals(rightDigest);
}

function deepEqualJson(left: unknown, right: unknown): boolean {
  if (Object.is(left, right)) {
    return true;
  }

  if (Array.isArray(left) || Array.isArray(right)) {
    if (!Array.isArray(left) || !Array.isArray(right) || left.length !== right.length) {
      return false;
    }
    return left.every((item, index) => deepEqualJson(item, right[index]));
  }

  if (isPlainObject(left) && isPlainObject(right)) {
    const leftKeys = Object.keys(left);
    const rightKeys = Object.keys(right);
    if (leftKeys.length !== rightKeys.length) {
      return false;
    }
    return leftKeys.every((key) => hasOwn(right, key) && deepEqualJson(left[key], right[key]));
  }

  return false;
}

function cloneJson<T>(value: T): T {
  if (Array.isArray(value)) {
    return value.map((item) => cloneJson(item)) as T;
  }

  if (isPlainObject(value)) {
    const clone: JsonObject = {};
    for (const key of Object.keys(value)) {
      defineJsonProperty(clone, key, cloneJson(value[key]));
    }
    return clone as T;
  }

  return value;
}

function sanitizeClaims(value: unknown): unknown {
  if (Array.isArray(value)) {
    return value.map((item) => sanitizeClaims(item));
  }

  if (isPlainObject(value)) {
    const claims: JsonObject = {};
    for (const key of Object.keys(value)) {
      if (key === "_sd") {
        continue;
      }
      defineJsonProperty(claims, key, sanitizeClaims(value[key]));
    }
    return claims;
  }

  return value;
}

export interface DecodedDisclosure {
  readonly encoded: string;
  readonly salt: string;
  readonly name: string;
  readonly value: unknown;
  readonly digest: string;
}

export interface DisclosureValidationOptions {
  /**
   * Optional expected _sd_alg. When present it must match the issuer payload.
   */
  readonly sdAlg?: string;
}

export interface DisclosureValidationResult {
  readonly valid: true;
  readonly sdAlg: SupportedSdAlg;
  /** The verified issuer payload, unmodified. */
  readonly payload: JsonObject;
  /** The reconstructed claim set with all _sd arrays removed. */
  readonly claims: JsonObject;
  readonly disclosures: readonly DecodedDisclosure[];
  readonly digestCounts: ReadonlyMap<string, number>;
}

interface DigestScanState {
  readonly sdAlg: SupportedSdAlg;
  readonly digestCounts: Map<string, number>;
  readonly digestParents: Map<string, JsonObject>;
}

function scanAuthenticatedValue(value: unknown, state: DigestScanState): void {
  if (Array.isArray(value)) {
    for (const item of value) {
      scanAuthenticatedValue(item, state);
    }
    return;
  }

  if (!isPlainObject(value)) {
    return;
  }

  if (hasOwn(value, "_sd_alg")) {
    const nestedAlg = normalizeSdAlg(value._sd_alg);
    if (nestedAlg !== state.sdAlg) {
      fail("ALGORITHM_MIXING", "Nested _sd_alg does not match the issuer payload _sd_alg");
    }
  }

  if (hasOwn(value, "_sd")) {
    if (!Array.isArray(value._sd)) {
      fail("MALFORMED_SD_ARRAY", "_sd must be an array");
    }

    for (const digest of value._sd) {
      assertDigestForAlg(digest, state.sdAlg);

      const previousCount = state.digestCounts.get(digest) ?? 0;
      if (previousCount !== 0) {
        fail("DUPLICATE_DIGEST", "An _sd digest appears more than once in the authenticated payload");
      }

      state.digestCounts.set(digest, 1);
      state.digestParents.set(digest, value);
    }
  }

  for (const key of Object.keys(value)) {
    if (key !== "_sd") {
      scanAuthenticatedValue(value[key], state);
    }
  }
}

function normalizeDisclosureInput(disclosures: readonly string[] | string): readonly string[] {
  if (Array.isArray(disclosures)) {
    return disclosures;
  }

  if (typeof disclosures !== "string") {
    fail("INVALID_DISCLOSURE", "Disclosures must be an array or a ~-separated string");
  }

  if (disclosures.length === 0) {
    return [];
  }

  const parts = disclosures.split(DISCLOSURE_SEPARATOR);
  if (parts.length > 1 && parts[parts.length - 1] === "") {
    parts.pop();
  }
  return parts;
}

function validateDisclosureName(name: string): void {
  if (name === "_sd" || name === "...") {
    fail("FORBIDDEN_DISCLOSURE_NAME", `Disclosure name "${name}" is reserved`);
  }
}

export function disclosureDigest(disclosure: string, sdAlg: string = DEFAULT_SD_ALG): string {
  if (typeof disclosure !== "string" || disclosure.length === 0) {
    fail("INVALID_DISCLOSURE", "Disclosure must be a non-empty string");
  }
  return digestBase64Url(disclosure, normalizeSdAlg(sdAlg));
}

export function decodeDisclosure(
  disclosure: string,
  sdAlg: string = DEFAULT_SD_ALG,
): DecodedDisclosure {
  const normalizedAlg = normalizeSdAlg(sdAlg);
  const decoded = decodeBase64UrlStrict(disclosure, "INVALID_DISCLOSURE", "Disclosure");
  let text: string;

  try {
    text = new TextDecoder("utf-8", { fatal: true }).decode(decoded);
  } catch {
    fail("INVALID_DISCLOSURE", "Disclosure is not valid UTF-8");
  }

  let parsed: unknown;
  try {
    parsed = JSON.parse(text);
  } catch {
    fail("INVALID_DISCLOSURE", "Disclosure is not valid JSON");
  }

  if (!Array.isArray(parsed) || parsed.length !== 3) {
    fail("INVALID_DISCLOSURE", "Disclosure must be a JSON array [salt, name, value]");
  }

  const [salt, name, value] = parsed;
  if (typeof salt !== "string" || salt.length === 0) {
    fail("MISSING_SALT", "Disclosure salt must be a non-empty string");
  }
  if (typeof name !== "string") {
    fail("INVALID_DISCLOSURE", "Disclosure name must be a string");
  }
  validateDisclosureName(name);

  return {
    encoded: disclosure,
    salt,
    name,
    value,
    digest: digestBase64Url(disclosure, normalizedAlg),
  };
}

/**
 * Validates every supplied disclosure as a single transaction.
 *
 * `issuerPayload` must already have passed issuer JWT signature verification.
 * A nested disclosure is only accepted after its parent disclosure has been
 * expanded from the authenticated payload.
 */
export function verifySdJwtDisclosures(
  issuerPayload: JsonObject,
  disclosures: readonly string[] | string,
  options: DisclosureValidationOptions = {},
): DisclosureValidationResult {
  if (!isPlainObject(issuerPayload)) {
    fail("INVALID_ISSUER_JWT", "Issuer payload must be a JSON object");
  }

  const sdAlg = normalizeSdAlg(hasOwn(issuerPayload, "_sd_alg") ? issuerPayload._sd_alg : undefined);
  if (options.sdAlg !== undefined) {
    const expectedAlg = normalizeSdAlg(options.sdAlg);
    if (expectedAlg !== sdAlg) {
      fail("ALGORITHM_MIXING", "Expected _sd_alg does not match the issuer payload");
    }
  }

  const signedPayload = cloneJson(issuerPayload);
  const document = cloneJson(issuerPayload);
  const state: DigestScanState = {
    sdAlg,
    digestCounts: new Map(),
    digestParents: new Map(),
  };
  scanAuthenticatedValue(document, state);

  const disclosureInputs = normalizeDisclosureInput(disclosures);
  const parsedDisclosures = disclosureInputs.map((disclosure) => decodeDisclosure(disclosure, sdAlg));
  const salts = new Set<string>();
  const digests = new Set<string>();
  const disclosureByDigest = new Map<string, DecodedDisclosure>();

  for (const disclosure of parsedDisclosures) {
    if (salts.has(disclosure.salt)) {
      fail("DUPLICATE_SALT", "The same disclosure salt was used more than once");
    }
    salts.add(disclosure.salt);

    if (digests.has(disclosure.digest)) {
      fail("DUPLICATE_DIGEST", "The same disclosure was presented more than once");
    }
    digests.add(disclosure.digest);
    disclosureByDigest.set(disclosure.digest, disclosure);
  }

  const processed = new Set<string>();
  let madeProgress = true;

  while (madeProgress) {
    madeProgress = false;

    for (const [digest, disclosure] of disclosureByDigest) {
      if (processed.has(digest)) {
        continue;
      }

      const parent = state.digestParents.get(digest);
      if (parent === undefined) {
        continue;
      }

      if (hasOwn(parent, disclosure.name)) {
        fail(
          "DISCLOSURE_NAME_CONFLICT",
          `Disclosure name "${disclosure.name}" conflicts with a claim that is already present`,
        );
      }

      const disclosedValue = cloneJson(disclosure.value);
      defineJsonProperty(parent, disclosure.name, disclosedValue);
      processed.add(digest);
      madeProgress = true;
      scanAuthenticatedValue(disclosedValue, state);
    }
  }

  if (processed.size !== disclosureByDigest.size) {
    const missing = [...disclosureByDigest.keys()].find((digest) => !processed.has(digest));
    const disclosure = missing === undefined ? undefined : disclosureByDigest.get(missing);
    fail(
      "DISCLOSURE_NOT_SIGNED_OR_PARENT_WITHHELD",
      disclosure === undefined
        ? "A disclosure is not reachable from the signed payload"
        : `Disclosure "${disclosure.name}" is not signed by the issuer or its parent was not disclosed`,
    );
  }

  return {
    valid: true,
    sdAlg,
    payload: signedPayload,
    claims: sanitizeClaims(document) as JsonObject,
    disclosures: [...parsedDisclosures],
    digestCounts: new Map(state.digestCounts),
  };
}

export interface ParsedSdJwtPresentation {
  readonly issuerJwt: string;
  readonly issuerHeader: JsonObject;
  readonly issuerPayload: JsonObject;
  readonly disclosures: readonly string[];
  /** Exact SD-JWT portion covered by KB-JWT sd_hash (without the KB-JWT). */
  readonly sdJwt: string;
  readonly kbJwt?: string;
}

interface ParsedCompactJwt {
  readonly jwt: string;
  readonly header: JsonObject;
  readonly payload: JsonObject;
  readonly signingInput: string;
  readonly signature: Buffer;
}

function parseCompactJwt(
  jwt: string,
  code: SdJwtValidationErrorCode,
  label: string,
): ParsedCompactJwt {
  if (typeof jwt !== "string") {
    return fail(code, `${label} must be a compact JWT string`);
  }

  const parts = jwt.split(".");
  if (parts.length !== 3 || parts.some((part) => part.length === 0)) {
    return fail(code, `${label} must have three non-empty compact JWT segments`);
  }

  const header = decodeJsonBase64Url(parts[0], code, `${label} header`);
  const payload = decodeJsonBase64Url(parts[1], code, `${label} payload`);
  if (!isPlainObject(header) || !isPlainObject(payload)) {
    return fail(code, `${label} header and payload must be JSON objects`);
  }

  return {
    jwt,
    header,
    payload,
    signingInput: `${parts[0]}.${parts[1]}`,
    signature: decodeBase64UrlStrict(parts[2], code, `${label} signature`),
  };
}

export function parseSdJwtPresentation(presentation: string): ParsedSdJwtPresentation {
  if (typeof presentation !== "string" || presentation.length === 0) {
    fail("INVALID_SD_JWT", "SD-JWT presentation must be a non-empty string");
  }

  const parts = presentation.split(DISCLOSURE_SEPARATOR);
  const issuerJwt = parts[0];
  const issuer = parseCompactJwt(issuerJwt, "INVALID_ISSUER_JWT", "Issuer JWT");

  if (parts.length === 1) {
    return {
      issuerJwt,
      issuerHeader: issuer.header,
      issuerPayload: issuer.payload,
      disclosures: [],
      sdJwt: presentation,
    };
  }

  const last = parts[parts.length - 1];
  const hasKbJwt = last.length > 0 && last.split(".").length === 3;
  const bodyParts = hasKbJwt ? parts.slice(0, -1) : parts;
  const disclosureParts = bodyParts.slice(1);

  if (disclosureParts.length > 0 && disclosureParts[disclosureParts.length - 1] === "") {
    disclosureParts.pop();
  }

  if (disclosureParts.some((part) => part.length === 0)) {
    fail("INVALID_SD_JWT", "SD-JWT contains an empty disclosure segment");
  }

  const sdJwt = hasKbJwt
    ? `${bodyParts.join(DISCLOSURE_SEPARATOR)}${DISCLOSURE_SEPARATOR}`
    : presentation;
  const kbJwt = hasKbJwt ? last : undefined;

  if (kbJwt !== undefined) {
    parseCompactJwt(kbJwt, "KB_JWT_INVALID", "KB-JWT");
  }

  return {
    issuerJwt,
    issuerHeader: issuer.header,
    issuerPayload: issuer.payload,
    disclosures: disclosureParts,
    sdJwt,
    kbJwt,
  };
}

function jwtKeyObject(
  key: JwtKey,
  alg: string,
): ReturnType<typeof createSecretKey> | ReturnType<typeof createPublicKey> {
  if (typeof key === "string") {
    if (alg.startsWith("HS")) {
      if (key.includes("-----BEGIN ")) {
        fail("KB_SIGNATURE_INVALID", "A public/private PEM key cannot be used for HMAC verification");
      }
      return createSecretKey(Buffer.from(key, "utf8"));
    }
    return createPublicKey(key);
  }

  if (key instanceof Uint8Array) {
    if (alg.startsWith("HS")) {
      return createSecretKey(key);
    }
    return createPublicKey(key);
  }

  if (!isPlainObject(key)) {
    fail("KB_SIGNATURE_INVALID", "Unsupported JWT key");
  }

  if (typeof key.type === "string") {
    const isSecretKey = key.type === "secret";
    if (isSecretKey !== alg.startsWith("HS")) {
      fail("KB_SIGNATURE_INVALID", "JWT algorithm is incompatible with the supplied key type");
    }
    return key as unknown as ReturnType<typeof createSecretKey>;
  }

  if (key.kty === "oct") {
    if (!alg.startsWith("HS")) {
      fail("KB_SIGNATURE_INVALID", "JWT algorithm is incompatible with the supplied JWK");
    }
    if (typeof key.k !== "string") {
      fail("KB_SIGNATURE_INVALID", "Symmetric JWK is missing k");
    }
    return createSecretKey(decodeBase64UrlStrict(key.k, "KB_SIGNATURE_INVALID", "JWK k"));
  }

  if (alg.startsWith("HS")) {
    fail("KB_SIGNATURE_INVALID", "JWT algorithm is incompatible with the supplied JWK");
  }

  return createPublicKey({ key: key as never, format: "jwk" });
}

function verifyCompactJwtSignature(
  parsed: ParsedCompactJwt,
  key: JwtKey,
  signatureCode: SdJwtValidationErrorCode,
): void {
  const alg = parsed.header.alg;
  if (typeof alg !== "string" || !hasOwn(JWT_ALGORITHMS, alg)) {
    fail("KB_ALGORITHM_UNSUPPORTED", `Unsupported JWT algorithm: ${String(alg)}`);
  }

  const metadata = JWT_ALGORITHMS[alg as keyof typeof JWT_ALGORITHMS];
  const signingInput = Buffer.from(parsed.signingInput, "utf8");

  try {
    if (metadata.family === "hmac") {
      const keyObject = jwtKeyObject(key, alg);
      const actual = createHmac(metadata.hash, keyObject).update(signingInput).digest();
      if (actual.length !== parsed.signature.length || !actual.equals(parsed.signature)) {
        fail(signatureCode, "JWT signature verification failed");
      }
      return;
    }

    const keyObject = jwtKeyObject(key, alg);
    let valid = false;

    if (metadata.family === "pss") {
      valid = verifyWithCrypto(metadata.hash, signingInput, keyObject, parsed.signature, {
        padding: cryptoConstants.RSA_PKCS1_PSS_PADDING,
        saltLength: cryptoConstants.RSA_PSS_SALTLEN_DIGEST,
      });
    } else if (metadata.family === "ecdsa") {
      valid = verifyWithCrypto(metadata.hash, signingInput, keyObject, parsed.signature, {
        dsaEncoding: "ieee-p1363",
      });
    } else if (metadata.family === "eddsa") {
      valid = verifyWithCrypto(null, signingInput, keyObject, parsed.signature);
    } else {
      valid = verifyWithCrypto(metadata.hash, signingInput, keyObject, parsed.signature);
    }

    if (!valid) {
      fail(signatureCode, "JWT signature verification failed");
    }
  } catch (error) {
    if (error instanceof SdJwtValidationError) {
      throw error;
    }
    fail(signatureCode, "JWT signature verification failed");
  }
}

export interface KeyBindingJwtValidationOptions {
  readonly expectedNonce?: string;
  /** Alias for expectedNonce. */
  readonly nonce?: string;
  readonly expectedAudience?: string | readonly string[];
  /** Alias for expectedAudience. */
  readonly audience?: string | readonly string[];
  /** Alias for expectedAudience. */
  readonly aud?: string | readonly string[];
  readonly now?: number | Date;
  readonly clockToleranceSeconds?: number;
  /** Alias for clockToleranceSeconds. */
  readonly clockTolerance?: number;
  readonly sdAlg?: string;
  readonly holderKey?: JwtKey;
  readonly verifyKbJwt?: (
    kbJwt: string,
    payload: JsonObject,
    header: JsonObject,
  ) => boolean;
  readonly verifyKbJwtSignature?: boolean;
}

export interface KeyBindingJwtValidationResult {
  readonly header: JsonObject;
  readonly payload: JsonObject;
  readonly sdHash: string;
  readonly signatureVerified: boolean;
}

function nowInSeconds(now: number | Date | undefined): number {
  if (now === undefined) {
    return Date.now() / 1000;
  }
  if (now instanceof Date) {
    return now.getTime() / 1000;
  }
  if (typeof now === "number" && Number.isFinite(now)) {
    return now > 1_000_000_000_000 ? now / 1000 : now;
  }
  return fail("KB_JWT_INVALID", "now must be a finite number of seconds or a Date");
}

function audienceMatches(actual: unknown, expected: string | readonly string[]): boolean {
  if (typeof expected !== "string" && !Array.isArray(expected)) {
    return false;
  }
  const expectedValues = typeof expected === "string" ? [expected] : [...expected];
  const actualValues = typeof actual === "string" ? [actual] : Array.isArray(actual) ? actual : [];
  return expectedValues.some(
    (expectedValue) =>
      typeof expectedValue === "string" &&
      actualValues.some(
        (actualValue) => typeof actualValue === "string" && actualValue === expectedValue,
      ),
  );
}

/**
 * Validates a KB-JWT against the exact SD-JWT serialization that was presented.
 * The caller must pass the SD-JWT string without the KB-JWT appended.
 */
export function validateKeyBindingJwt(
  kbJwt: string,
  sdJwt: string,
  options: KeyBindingJwtValidationOptions,
): KeyBindingJwtValidationResult {
  if (!isPlainObject(options)) {
    fail("KB_JWT_INVALID", "KB-JWT validation options are required");
  }
  const expectedNonce = options.expectedNonce ?? options.nonce;
  if (typeof expectedNonce !== "string") {
    fail("KB_JWT_INVALID", "expectedNonce is required when a KB-JWT is presented");
  }

  const expectedAudience = options.expectedAudience ?? options.audience ?? options.aud;
  if (expectedAudience === undefined) {
    fail("KB_JWT_INVALID", "expectedAudience is required when a KB-JWT is presented");
  }

  const sdAlg = normalizeSdAlg(options.sdAlg);
  if (typeof sdJwt !== "string") {
    fail("KB_SD_HASH_MISMATCH", "The presented SD-JWT must be a string");
  }
  const parsed = parseCompactJwt(kbJwt, "KB_JWT_INVALID", "KB-JWT");
  const sdHash = parsed.payload.sd_hash;

  if (typeof sdHash !== "string") {
    fail("KB_SD_HASH_MISMATCH", "KB-JWT is missing sd_hash");
  }

  const expectedSdHash = digestBase64Url(sdJwt, sdAlg);
  if (!constantTimeStringEqual(sdHash, expectedSdHash)) {
    fail("KB_SD_HASH_MISMATCH", "KB-JWT sd_hash does not match the presented SD-JWT");
  }

  if (
    typeof parsed.payload.nonce !== "string" ||
    !constantTimeStringEqual(parsed.payload.nonce, expectedNonce)
  ) {
    fail("KB_NONCE_MISMATCH", "KB-JWT nonce does not match the expected nonce");
  }

  if (!audienceMatches(parsed.payload.aud, expectedAudience)) {
    fail("KB_AUD_MISMATCH", "KB-JWT audience does not match");
  }

  const exp = parsed.payload.exp;
  if (typeof exp !== "number" || !Number.isFinite(exp)) {
    fail("KB_JWT_INVALID", "KB-JWT must contain a finite exp claim");
  }

  const tolerance = options.clockToleranceSeconds ?? options.clockTolerance ?? 0;
  if (typeof tolerance !== "number" || !Number.isFinite(tolerance) || tolerance < 0) {
    fail("KB_JWT_INVALID", "clockToleranceSeconds must be a non-negative finite number");
  }

  const currentTime = nowInSeconds(options.now);
  if (currentTime >= exp + tolerance) {
    fail("KB_EXPIRED", "KB-JWT has expired");
  }

  if (typeof parsed.payload.nbf === "number" && Number.isFinite(parsed.payload.nbf)) {
    if (currentTime + tolerance < parsed.payload.nbf) {
      fail("KB_NOT_YET_VALID", "KB-JWT is not valid yet");
    }
  }

  let signatureVerified = false;
  if (options.verifyKbJwt !== undefined) {
    if (!options.verifyKbJwt(kbJwt, parsed.payload, parsed.header)) {
      fail("KB_SIGNATURE_INVALID", "KB-JWT signature verification failed");
    }
    signatureVerified = true;
  } else if (options.holderKey !== undefined) {
    verifyCompactJwtSignature(parsed, options.holderKey, "KB_SIGNATURE_INVALID");
    signatureVerified = true;
  } else if (options.verifyKbJwtSignature === true) {
    fail("KB_SIGNATURE_INVALID", "No holder key was provided for KB-JWT signature verification");
  }

  return {
    header: parsed.header,
    payload: parsed.payload,
    sdHash,
    signatureVerified,
  };
}

function holderKeyFromIssuer(payload: JsonObject): JsonObject | undefined {
  const cnf = payload.cnf;
  if (!isPlainObject(cnf) || !isPlainObject(cnf.jwk)) {
    return undefined;
  }
  return cnf.jwk;
}

export interface VerifySdJwtOptions extends DisclosureValidationOptions {
  /**
   * The issuer JWT payload after its signature has been verified by the caller.
   * If omitted, the payload embedded in the issuer JWT is parsed. Supply
   * issuerKey or verifyIssuerJwt when issuer verification must happen here.
   */
  readonly issuerPayload?: JsonObject;
  /** Alias for issuerPayload. */
  readonly issuerJwtPayload?: JsonObject;
  readonly issuerKey?: JwtKey;
  readonly verifyIssuerJwt?: (
    issuerJwt: string,
    payload: JsonObject,
    header: JsonObject,
  ) => boolean;
  readonly expectedNonce?: string;
  /** Alias for expectedNonce. */
  readonly nonce?: string;
  readonly expectedAudience?: string | readonly string[];
  /** Alias for expectedAudience. */
  readonly audience?: string | readonly string[];
  /** Alias for expectedAudience. */
  readonly aud?: string | readonly string[];
  readonly now?: number | Date;
  readonly clockToleranceSeconds?: number;
  /** Alias for clockToleranceSeconds. */
  readonly clockTolerance?: number;
  readonly holderKey?: JwtKey;
  readonly verifyKbJwt?: (
    kbJwt: string,
    payload: JsonObject,
    header: JsonObject,
  ) => boolean;
  readonly verifyKbJwtSignature?: boolean;
  readonly kbJwt?: string;
  readonly requireKbJwt?: boolean;
}

export interface VerifySdJwtResult extends DisclosureValidationResult {
  readonly issuerJwt: string;
  readonly issuerPayload: JsonObject;
  readonly issuerSignatureVerified: boolean;
  readonly sdJwt: string;
  readonly kbJwt?: string;
  readonly kb?: KeyBindingJwtValidationResult;
}

/**
 * Full SD-JWT presentation verification.
 *
 * Disclosure validation is atomic: every disclosure must be valid and reachable
 * from the authenticated payload or the whole presentation is rejected.
 */
export function verifySdJwt(
  presentationOrOptions: string | (VerifySdJwtOptions & { sdJwt: string }),
  maybeOptions: VerifySdJwtOptions = {},
): VerifySdJwtResult {
  let presentation: string;
  let options: VerifySdJwtOptions;

  if (typeof presentationOrOptions === "string") {
    presentation = presentationOrOptions;
    options = maybeOptions;
  } else {
    if (!isPlainObject(presentationOrOptions)) {
      fail("INVALID_SD_JWT", "SD-JWT presentation must be a string or an options object");
    }
    const { sdJwt, ...rest } = presentationOrOptions;
    if (typeof sdJwt !== "string") {
      fail("INVALID_SD_JWT", "sdJwt must be a string");
    }
    presentation = sdJwt;
    options = rest;
  }

  const parsed = parseSdJwtPresentation(presentation);
  const suppliedIssuerPayload = options.issuerPayload ?? options.issuerJwtPayload;
  let issuerSignatureVerified = false;

  if (suppliedIssuerPayload !== undefined) {
    if (!deepEqualJson(parsed.issuerPayload, suppliedIssuerPayload)) {
      fail("ISSUER_PAYLOAD_MISMATCH", "Supplied issuer payload does not match the issuer JWT");
    }
  }

  if (options.verifyIssuerJwt !== undefined) {
    if (!options.verifyIssuerJwt(parsed.issuerJwt, parsed.issuerPayload, parsed.issuerHeader)) {
      fail("ISSUER_SIGNATURE_INVALID", "Issuer JWT signature verification failed");
    }
    issuerSignatureVerified = true;
  }

  if (options.issuerKey !== undefined) {
    const issuerJwt = parseCompactJwt(parsed.issuerJwt, "INVALID_ISSUER_JWT", "Issuer JWT");
    verifyCompactJwtSignature(issuerJwt, options.issuerKey, "ISSUER_SIGNATURE_INVALID");
    issuerSignatureVerified = true;
  }

  const validated = verifySdJwtDisclosures(
    suppliedIssuerPayload ?? parsed.issuerPayload,
    parsed.disclosures,
    options,
  );

  if (parsed.kbJwt !== undefined && options.kbJwt !== undefined && parsed.kbJwt !== options.kbJwt) {
    fail("KB_JWT_INVALID", "Two different KB-JWTs were supplied");
  }

  const kbJwt = parsed.kbJwt ?? options.kbJwt;
  if (kbJwt === undefined) {
    if (options.requireKbJwt === true) {
      fail("KB_JWT_MISSING", "A KB-JWT is required");
    }

    return {
      ...validated,
      issuerJwt: parsed.issuerJwt,
      issuerPayload: parsed.issuerPayload,
      issuerSignatureVerified,
      sdJwt: parsed.sdJwt,
    };
  }

  const expectedNonce = options.expectedNonce ?? options.nonce;
  const expectedAudience = options.expectedAudience ?? options.audience ?? options.aud;
  if (expectedNonce === undefined || expectedAudience === undefined) {
    fail("KB_JWT_INVALID", "expectedNonce and expectedAudience are required for KB-JWT validation");
  }

  const holderKey = options.holderKey ?? holderKeyFromIssuer(parsed.issuerPayload);
  const kb = validateKeyBindingJwt(kbJwt, parsed.sdJwt, {
    expectedNonce,
    expectedAudience,
    now: options.now,
    clockToleranceSeconds: options.clockToleranceSeconds ?? options.clockTolerance,
    sdAlg: validated.sdAlg,
    holderKey,
    verifyKbJwt: options.verifyKbJwt,
    verifyKbJwtSignature: options.verifyKbJwtSignature,
  });

  return {
    ...validated,
    issuerJwt: parsed.issuerJwt,
    issuerPayload: parsed.issuerPayload,
    issuerSignatureVerified,
    sdJwt: parsed.sdJwt,
    kbJwt,
    kb,
  };
}

export const hashDisclosure = disclosureDigest;
export const computeDisclosureDigest = disclosureDigest;
export const verifyDisclosures = verifySdJwtDisclosures;
export const validateDisclosures = verifySdJwtDisclosures;
export const validateSdJwtDisclosures = verifySdJwtDisclosures;
export const verifySdJwtDisclosure = verifySdJwtDisclosures;
export const parseSdJwt = parseSdJwtPresentation;
export const verifySdJwtPresentation = verifySdJwt;
export const validateSdJwtPresentation = verifySdJwt;
export const validateSdJwt = verifySdJwt;
export const verifyKeyBindingJwt = validateKeyBindingJwt;
export const validateKbJwt = validateKeyBindingJwt;
