import {
  constants,
  createPublicKey,
  verify as verifySignature,
} from "node:crypto";

import {
  NonceStore,
  type Clock,
  type NonceRecord,
  type NonceStoreLike,
  type NonceStoreOptions,
} from "./nonce-store.ts";

export const PROOF_JWT_TYP = "openid4vci-proof+jwt";
export const MAX_IAT_FUTURE_SECONDS = 60;
const MAX_PROOF_LENGTH = 64 * 1024;

type PublicKeyObject = ReturnType<typeof createPublicKey>;

export type ProofValidationErrorCode =
  | "MALFORMED_PROOF"
  | "INVALID_TYP"
  | "INVALID_AUDIENCE"
  | "INVALID_NONCE"
  | "UNKNOWN_NONCE"
  | "NONCE_EXPIRED"
  | "NONCE_ALREADY_USED"
  | "INVALID_IAT"
  | "IAT_BEFORE_NONCE"
  | "IAT_TOO_FAR_IN_FUTURE"
  | "INVALID_JWK"
  | "PRIVATE_JWK"
  | "UNSUPPORTED_ALGORITHM"
  | "INVALID_SIGNATURE";

export class ProofValidationError extends Error {
  readonly code: ProofValidationErrorCode;

  constructor(code: ProofValidationErrorCode, message: string) {
    super(message);
    this.name = "ProofValidationError";
    this.code = code;
  }
}

export interface ProofJwtHeader {
  typ?: unknown;
  alg?: unknown;
  jwk?: unknown;
  [member: string]: unknown;
}

export interface ProofJwtPayload {
  aud?: unknown;
  nonce?: unknown;
  iat?: unknown;
  [member: string]: unknown;
}

export interface VerifiedProof {
  header: ProofJwtHeader;
  payload: ProofJwtPayload;
  /** Public JWK from the proof header; it can safely be used for key binding. */
  publicKeyJwk: Record<string, unknown>;
}

export type ProofValidationResult =
  | {
      valid: true;
      header: ProofJwtHeader;
      payload: ProofJwtPayload;
      claims: ProofJwtPayload;
      publicKeyJwk: Record<string, unknown>;
    }
  | {
      valid: false;
      error: ProofValidationError;
      code: ProofValidationErrorCode;
      message: string;
    };

export interface ProofValidatorOptions extends NonceStoreOptions {
  /** Exact issuer identifier that must appear in the proof's aud member. */
  issuer: string;
  /**
   * Existing nonce store. When omitted, an isolated in-memory store is created.
   * Supply a shared store when several validator instances run in one process.
   */
  nonceStore?: NonceStoreLike;
  /** Alias for maxIatFutureSeconds. */
  maxIatSkewSeconds?: number;
  /** Maximum allowed iat distance into the future. Defaults to 60 seconds. */
  maxIatFutureSeconds?: number;
  /** Clock returning epoch milliseconds. */
  now?: Clock | number;
}

type AlgorithmSpec = {
  hash: "sha256" | "sha384" | "sha512";
  keyType: "EC" | "RSA" | "OKP";
  curve?: string;
  padding?: "pkcs1" | "pss";
};

const ALGORITHMS: Record<string, AlgorithmSpec> = {
  ES256: { hash: "sha256", keyType: "EC", curve: "P-256" },
  ES384: { hash: "sha384", keyType: "EC", curve: "P-384" },
  ES512: { hash: "sha512", keyType: "EC", curve: "P-521" },
  RS256: { hash: "sha256", keyType: "RSA", padding: "pkcs1" },
  RS384: { hash: "sha384", keyType: "RSA", padding: "pkcs1" },
  RS512: { hash: "sha512", keyType: "RSA", padding: "pkcs1" },
  PS256: { hash: "sha256", keyType: "RSA", padding: "pss" },
  PS384: { hash: "sha384", keyType: "RSA", padding: "pss" },
  PS512: { hash: "sha512", keyType: "RSA", padding: "pss" },
  EdDSA: { hash: "sha512", keyType: "OKP" },
};

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function hasOwn(value: object, member: string): boolean {
  return Object.prototype.hasOwnProperty.call(value, member);
}

function decodeBase64Url(segment: string): Buffer {
  if (!/^[A-Za-z0-9_-]+$/.test(segment)) {
    throw new ProofValidationError("MALFORMED_PROOF", "proof contains invalid base64url data");
  }
  return Buffer.from(segment, "base64url");
}

function parseJsonObject(segment: string, part: string): Record<string, unknown> {
  let parsed: unknown;
  try {
    parsed = JSON.parse(decodeBase64Url(segment).toString("utf8"));
  } catch (error) {
    if (error instanceof ProofValidationError) {
      throw error;
    }
    throw new ProofValidationError("MALFORMED_PROOF", `proof ${part} is not valid JSON`);
  }

  if (!isRecord(parsed)) {
    throw new ProofValidationError("MALFORMED_PROOF", `proof ${part} must be a JSON object`);
  }
  return parsed;
}

function parseProof(proofJwt: string): {
  header: ProofJwtHeader;
  payload: ProofJwtPayload;
  signingInput: string;
  signature: Buffer;
} {
  if (typeof proofJwt !== "string" || proofJwt.length === 0) {
    throw new ProofValidationError("MALFORMED_PROOF", "proof must be a non-empty compact JWS string");
  }
  if (proofJwt.length > MAX_PROOF_LENGTH) {
    throw new ProofValidationError("MALFORMED_PROOF", "proof is too large");
  }

  const parts = proofJwt.split(".");
  if (parts.length !== 3) {
    throw new ProofValidationError("MALFORMED_PROOF", "proof must have three JWS segments");
  }

  const [encodedHeader, encodedPayload, encodedSignature] = parts;
  const header = parseJsonObject(encodedHeader, "header");
  const payload = parseJsonObject(encodedPayload, "payload");
  const signature = decodeBase64Url(encodedSignature);

  if (signature.length === 0) {
    throw new ProofValidationError("MALFORMED_PROOF", "proof signature must not be empty");
  }

  return {
    header,
    payload,
    signingInput: `${encodedHeader}.${encodedPayload}`,
    signature,
  };
}

function assertPrivateJwkAbsent(jwk: Record<string, unknown>): void {
  if (hasOwn(jwk, "d")) {
    throw new ProofValidationError(
      "PRIVATE_JWK",
      'proof header jwk must not contain private key member "d"',
    );
  }

  for (const member of ["p", "q", "dp", "dq", "qi", "oth", "k"]) {
    if (hasOwn(jwk, member)) {
      throw new ProofValidationError(
        "PRIVATE_JWK",
        `proof header jwk must not contain private key member "${member}"`,
      );
    }
  }
}

function assertAlgorithmAndKey(
  alg: unknown,
  jwk: Record<string, unknown>,
): { spec: AlgorithmSpec; publicKey: PublicKeyObject } {
  if (typeof alg !== "string" || !hasOwn(ALGORITHMS, alg)) {
    throw new ProofValidationError(
      "UNSUPPORTED_ALGORITHM",
      "proof header alg must be a supported asymmetric JWS algorithm",
    );
  }

  const spec = ALGORITHMS[alg];
  if (typeof jwk.kty !== "string" || jwk.kty !== spec.keyType) {
    throw new ProofValidationError(
      "INVALID_JWK",
      `proof header jwk kty must be ${spec.keyType} for ${alg}`,
    );
  }

  if (spec.curve !== undefined && jwk.crv !== spec.curve) {
    throw new ProofValidationError(
      "INVALID_JWK",
      `proof header jwk crv must be ${spec.curve} for ${alg}`,
    );
  }

  if (alg === "EdDSA" && (jwk.crv !== "Ed25519" && jwk.crv !== "Ed448")) {
    throw new ProofValidationError(
      "INVALID_JWK",
      "proof header jwk crv must be Ed25519 or Ed448 for EdDSA",
    );
  }

  let publicKey: PublicKeyObject;
  try {
    publicKey = createPublicKey({ key: jwk as any, format: "jwk" });
  } catch {
    throw new ProofValidationError("INVALID_JWK", "proof header jwk is not a valid public key");
  }

  return { spec, publicKey };
}

function verifySignatureWithKey(
  algorithm: string,
  spec: AlgorithmSpec,
  publicKey: PublicKeyObject,
  signingInput: string,
  signature: Buffer,
): boolean {
  const data = Buffer.from(signingInput, "utf8");

  try {
    if (spec.keyType === "OKP") {
      return verifySignature(null, data, publicKey, signature);
    }

    if (spec.keyType === "EC") {
      return verifySignature(
        spec.hash,
        data,
        { key: publicKey, dsaEncoding: "ieee-p1363" } as any,
        signature,
      );
    }

    const keyOptions: {
      key: PublicKeyObject;
      padding: number;
      saltLength?: number;
    } = {
      key: publicKey,
      padding:
        spec.padding === "pss"
          ? constants.RSA_PKCS1_PSS_PADDING
          : constants.RSA_PKCS1_PADDING,
    };
    if (spec.padding === "pss") {
      keyOptions.saltLength = constants.RSA_PSS_SALTLEN_DIGEST;
    }

    return verifySignature(spec.hash, data, keyOptions as any, signature);
  } catch {
    throw new ProofValidationError(
      "INVALID_SIGNATURE",
      `proof signature is not valid for ${algorithm}`,
    );
  }
}

function nonceFailure(record: NonceRecord | undefined, nowMs: number): ProofValidationError | undefined {
  if (record === undefined) {
    return new ProofValidationError(
      "UNKNOWN_NONCE",
      "proof nonce was not issued by this service",
    );
  }
  if (record.usedAtMs !== undefined) {
    return new ProofValidationError(
      "NONCE_ALREADY_USED",
      "proof nonce has already been used",
    );
  }
  if (nowMs >= record.expiresAtMs) {
    return new ProofValidationError("NONCE_EXPIRED", "proof nonce has expired");
  }
  return undefined;
}

function validateProofWithStore(
  proofJwt: string,
  issuer: string,
  nonceStore: NonceStoreLike,
  nowMs: number,
  maxIatFutureSeconds: number,
): ProofValidationResult {
  let parsed: ReturnType<typeof parseProof>;
  try {
    parsed = parseProof(proofJwt);
  } catch (error) {
    const validationError =
      error instanceof ProofValidationError
        ? error
        : new ProofValidationError("MALFORMED_PROOF", "proof could not be parsed");
    return {
      valid: false,
      error: validationError,
      code: validationError.code,
      message: validationError.message,
    };
  }

  const { header, payload, signingInput, signature } = parsed;

  if (header.typ !== PROOF_JWT_TYP) {
    const error = new ProofValidationError(
      "INVALID_TYP",
      `proof header typ must be exactly "${PROOF_JWT_TYP}"`,
    );
    return { valid: false, error, code: error.code, message: error.message };
  }

  if (typeof payload.aud !== "string" || payload.aud !== issuer) {
    const error = new ProofValidationError(
      "INVALID_AUDIENCE",
      "proof aud must equal this issuer identifier",
    );
    return { valid: false, error, code: error.code, message: error.message };
  }

  if (typeof payload.nonce !== "string" || payload.nonce.length === 0) {
    const error = new ProofValidationError("INVALID_NONCE", "proof nonce must be a non-empty string");
    return { valid: false, error, code: error.code, message: error.message };
  }
  const nonce = payload.nonce;

  if (
    typeof payload.iat !== "number" ||
    !Number.isFinite(payload.iat)
  ) {
    const error = new ProofValidationError("INVALID_IAT", "proof iat must be a finite NumericDate");
    return { valid: false, error, code: error.code, message: error.message };
  }
  const iat = payload.iat;

  const record = nonceStore.getNonce(nonce);
  const failure = nonceFailure(record, nowMs);
  if (failure !== undefined) {
    return { valid: false, error: failure, code: failure.code, message: failure.message };
  }

  const nonceIssuedAtSeconds = Math.floor(record!.issuedAtMs / 1000);
  if (iat < nonceIssuedAtSeconds) {
    const error = new ProofValidationError(
      "IAT_BEFORE_NONCE",
      "proof iat must not be earlier than the nonce issuance time",
    );
    return { valid: false, error, code: error.code, message: error.message };
  }

  const nowSeconds = nowMs / 1000;
  if (iat > nowSeconds + maxIatFutureSeconds) {
    const error = new ProofValidationError(
      "IAT_TOO_FAR_IN_FUTURE",
      `proof iat must not be later than ${maxIatFutureSeconds} seconds in the future`,
    );
    return { valid: false, error, code: error.code, message: error.message };
  }

  if (!isRecord(header.jwk)) {
    const error = new ProofValidationError(
      "INVALID_JWK",
      "proof header must contain a public jwk object",
    );
    return { valid: false, error, code: error.code, message: error.message };
  }
  const jwk = header.jwk;

  let spec: AlgorithmSpec;
  let publicKey: PublicKeyObject;
  try {
    assertPrivateJwkAbsent(jwk);
    ({ spec, publicKey } = assertAlgorithmAndKey(header.alg, jwk));
  } catch (error) {
    const validationError =
      error instanceof ProofValidationError
        ? error
        : new ProofValidationError("INVALID_JWK", "proof header jwk is invalid");
    return {
      valid: false,
      error: validationError,
      code: validationError.code,
      message: validationError.message,
    };
  }

  let signatureValid = false;
  try {
    signatureValid = verifySignatureWithKey(
      header.alg as string,
      spec,
      publicKey,
      signingInput,
      signature,
    );
  } catch (error) {
    const validationError =
      error instanceof ProofValidationError
        ? error
        : new ProofValidationError("INVALID_SIGNATURE", "proof signature verification failed");
    return {
      valid: false,
      error: validationError,
      code: validationError.code,
      message: validationError.message,
    };
  }

  if (!signatureValid) {
    const error = new ProofValidationError(
      "INVALID_SIGNATURE",
      `proof signature is not valid for ${String(header.alg)}`,
    );
    return { valid: false, error, code: error.code, message: error.message };
  }

  if (!nonceStore.consumeNonce(nonce, nowMs)) {
    const latest = nonceStore.getNonce(nonce);
    const consumeFailure =
      latest?.usedAtMs !== undefined
        ? new ProofValidationError("NONCE_ALREADY_USED", "proof nonce has already been used")
        : new ProofValidationError("NONCE_EXPIRED", "proof nonce has expired");
    return {
      valid: false,
      error: consumeFailure,
      code: consumeFailure.code,
      message: consumeFailure.message,
    };
  }

  return {
    valid: true,
    header,
    payload,
    claims: payload,
    publicKeyJwk: { ...jwk },
  };
}

function normalizeNow(now: Clock | number | undefined): Clock {
  if (typeof now === "number") {
    if (!Number.isFinite(now)) {
      throw new TypeError("now must be a finite epoch-millisecond value");
    }
    return () => now;
  }
  if (now === undefined) {
    return Date.now;
  }
  if (typeof now !== "function") {
    throw new TypeError("now must be a function returning epoch milliseconds");
  }
  return () => {
    const value = now();
    if (!Number.isFinite(value)) {
      throw new TypeError("now() must return a finite epoch-millisecond value");
    }
    return value;
  };
}

/**
 * Stateful proof validator for one issuer. Issue nonces with issueNonce() and
 * verify incoming holder proofs with validate() or verify().
 */
export class ProofValidator {
  readonly issuer: string;
  readonly nonceStore: NonceStoreLike;
  readonly maxIatFutureSeconds: number;
  readonly #now: Clock;

  constructor(options: ProofValidatorOptions | string) {
    if (typeof options === "string") {
      options = { issuer: options };
    }
    if (!isRecord(options)) {
      throw new TypeError("ProofValidator options must be an object or an issuer string");
    }
    if (typeof options.issuer !== "string" || options.issuer.length === 0) {
      throw new TypeError("issuer must be a non-empty string");
    }

    this.issuer = options.issuer;
    this.#now =
      options.now !== undefined
        ? normalizeNow(options.now)
        : options.nonceStore !== undefined
          ? () => options.nonceStore!.currentTime()
          : normalizeNow(undefined);
    this.nonceStore =
      options.nonceStore ??
      new NonceStore({
        nonceTtlSeconds: options.nonceTtlSeconds,
        ttlSeconds: options.ttlSeconds,
        nonceTtlMs: options.nonceTtlMs,
        ttlMs: options.ttlMs,
        now: this.#now,
      });

    const maxIatFutureSeconds =
      options.maxIatFutureSeconds ??
      options.maxIatSkewSeconds ??
      MAX_IAT_FUTURE_SECONDS;
    if (
      typeof maxIatFutureSeconds !== "number" ||
      !Number.isFinite(maxIatFutureSeconds) ||
      maxIatFutureSeconds < 0 ||
      maxIatFutureSeconds > MAX_IAT_FUTURE_SECONDS
    ) {
      throw new RangeError(
        `maxIatFutureSeconds must be between 0 and ${MAX_IAT_FUTURE_SECONDS}`,
      );
    }
    this.maxIatFutureSeconds = maxIatFutureSeconds;
  }

  /** Issues a fresh, opaque nonce from this service's nonce store. */
  issueNonce(): string {
    return this.nonceStore.issueNonce();
  }

  /** Alias for issueNonce(). */
  issue(): string {
    return this.issueNonce();
  }

  /**
   * Validates a proof without throwing. A successful validation consumes the
   * nonce exactly once.
   */
  validate(proofJwt: string): ProofValidationResult {
    return validateProofWithStore(
      proofJwt,
      this.issuer,
      this.nonceStore,
      this.#now(),
      this.maxIatFutureSeconds,
    );
  }

  /** Alias for validate(). */
  validateProof(proofJwt: string): ProofValidationResult {
    return this.validate(proofJwt);
  }

  /**
   * Validates a proof and returns its verified claims, or throws a
   * ProofValidationError.
   */
  verify(proofJwt: string): VerifiedProof {
    const result = this.validate(proofJwt);
    if (!result.valid) {
      throw result.error;
    }
    return {
      header: result.header,
      payload: result.payload,
      publicKeyJwk: result.publicKeyJwk,
    };
  }

  /** Alias for verify(). */
  verifyProof(proofJwt: string): VerifiedProof {
    return this.verify(proofJwt);
  }
}

export function createProofValidator(
  options: ProofValidatorOptions | string,
): ProofValidator {
  return new ProofValidator(options);
}

/** Alias for createProofValidator(). */
export const createProofService = createProofValidator;

export { ProofValidator as ProofVerifier };

/** Alias for createProofValidator(). */
export const createProofVerifier = createProofValidator;

export type ProofValidatorInput = ProofValidator | ProofValidatorOptions | string;

function resolveValidator(input: ProofValidatorInput): ProofValidator {
  return input instanceof ProofValidator ? input : createProofValidator(input);
}

/** Functional form of ProofValidator.validate(). */
export function validateProof(
  proofJwt: string,
  options: ProofValidatorInput,
): ProofValidationResult {
  return resolveValidator(options).validate(proofJwt);
}

/** Alias for validateProof(). */
export const validateProofJwt = validateProof;

/** Functional form of ProofValidator.verify(); throws on an invalid proof. */
export function verifyProof(
  proofJwt: string,
  options: ProofValidatorInput,
): VerifiedProof {
  return resolveValidator(options).verify(proofJwt);
}

/** Alias for verifyProof(). */
export const verifyProofJwt = verifyProof;

export function isValidProof(
  proofJwt: string,
  options: ProofValidatorInput,
): boolean {
  return validateProof(proofJwt, options).valid;
}
