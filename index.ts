/**
 * PRF input validation and salt normalisation for browser clients.
 *
 * The PRF extension is not a generic HMAC oracle: every salt is domain-separated
 * before it reaches WebAuthn, and evalByCredential is only accepted for credential
 * IDs that are actually present in allowCredentials.
 */

export const PRF_DOMAIN = "WebAuthn PRF";

const PRF_DOMAIN_BYTES = new Uint8Array([
  0x57, 0x65, 0x62, 0x41, 0x75, 0x74, 0x68, 0x6e,
  0x20, 0x50, 0x52, 0x46,
]);

const BASE64URL_ALPHABET =
  "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";

export type PrfBufferSource = ArrayBuffer | ArrayBufferView;

export interface PrfValues {
  first: PrfBufferSource;
  second?: PrfBufferSource;
}

export interface PrfInputs {
  eval?: PrfValues;
  evalByCredential?: Record<string, PrfValues>;
}

export interface PreparedPrfValues {
  first: Uint8Array;
  second?: Uint8Array;
}

export interface PreparedPrfInputs {
  eval?: PreparedPrfValues;
  evalByCredential?: Record<string, PreparedPrfValues>;
}

export interface PublicKeyCredentialDescriptorLike {
  id: PrfBufferSource | string;
  type?: string;
  transports?: readonly string[];
}

export type AllowCredentialLike =
  | PublicKeyCredentialDescriptorLike
  | PrfBufferSource
  | string;

export type PrfDigest = {
  digest(
    algorithm: string,
    data: PrfBufferSource,
  ): Promise<ArrayBuffer | ArrayBufferView>;
};

export type PrfValidationErrorCode =
  | "MISSING_FIRST"
  | "INVALID_PRF"
  | "INVALID_PRF_VALUES"
  | "INVALID_FIRST_SALT"
  | "INVALID_SECOND_SALT"
  | "INVALID_ALLOW_CREDENTIALS"
  | "INVALID_CREDENTIAL_ID"
  | "INVALID_BASE64URL"
  | "UNKNOWN_CREDENTIAL"
  | "EVAL_BY_CREDENTIAL_WITHOUT_ALLOW_CREDENTIALS"
  | "CRYPTO_UNAVAILABLE"
  | "INVALID_DIGEST";

export class PrfValidationError extends TypeError {
  readonly code: PrfValidationErrorCode;

  constructor(code: PrfValidationErrorCode, message: string) {
    super(message);
    this.name = "PrfValidationError";
    this.code = code;
    Object.setPrototypeOf(this, PrfValidationError.prototype);
  }
}

interface ParsedPrfValues {
  first: PrfBufferSource;
  second?: PrfBufferSource;
}

interface ParsedPrfInputs {
  eval?: ParsedPrfValues;
  evalByCredential?: Record<string, ParsedPrfValues>;
}

function fail(code: PrfValidationErrorCode, message: string): never {
  throw new PrfValidationError(code, message);
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function hasDefinedOwn(object: object, property: string): boolean {
  return (
    Object.prototype.hasOwnProperty.call(object, property) &&
    (object as Record<string, unknown>)[property] !== undefined
  );
}

function isArrayBuffer(value: unknown): value is ArrayBuffer {
  return Object.prototype.toString.call(value) === "[object ArrayBuffer]";
}

function toBytes(
  value: unknown,
  label: string,
  code: PrfValidationErrorCode,
): Uint8Array {
  try {
    if (isArrayBuffer(value)) {
      return new Uint8Array(value);
    }

    if (ArrayBuffer.isView(value)) {
      return new Uint8Array(
        value.buffer as ArrayBuffer,
        value.byteOffset,
        value.byteLength,
      );
    }
  } catch {
    // A detached or otherwise unusable buffer is an invalid BufferSource.
  }

  return fail(code, `${label} must be an ArrayBuffer or an ArrayBuffer view`);
}

function base64UrlCharCode(code: number): number {
  if (code >= 0x41 && code <= 0x5a) return code - 0x41;
  if (code >= 0x61 && code <= 0x7a) return code - 0x61 + 26;
  if (code >= 0x30 && code <= 0x39) return code - 0x30 + 52;
  if (code === 0x2d) return 62;
  if (code === 0x5f) return 63;
  return -1;
}

function hasBase64UrlSyntax(value: unknown): value is string {
  if (typeof value !== "string" || value.length === 0) {
    return false;
  }

  if (value.length % 4 === 1) {
    return false;
  }

  for (let index = 0; index < value.length; index += 1) {
    if (base64UrlCharCode(value.charCodeAt(index)) < 0) {
      return false;
    }
  }

  return true;
}

function decodeBase64UrlSyntax(value: string): Uint8Array {
  const remainder = value.length % 4;
  const outputLength =
    Math.floor(value.length / 4) * 3 +
    (remainder === 2 ? 1 : remainder === 3 ? 2 : 0);
  const bytes = new Uint8Array(outputLength);
  let outputIndex = 0;

  for (let index = 0; index < value.length; index += 4) {
    const first = base64UrlCharCode(value.charCodeAt(index));
    const second = base64UrlCharCode(value.charCodeAt(index + 1));
    const third =
      index + 2 < value.length
        ? base64UrlCharCode(value.charCodeAt(index + 2))
        : -1;
    const fourth =
      index + 3 < value.length
        ? base64UrlCharCode(value.charCodeAt(index + 3))
        : -1;

    bytes[outputIndex] = (first << 2) | (second >> 4);
    outputIndex += 1;

    if (third >= 0) {
      bytes[outputIndex] = ((second & 0x0f) << 4) | (third >> 2);
      outputIndex += 1;
    }

    if (fourth >= 0) {
      bytes[outputIndex] = ((third & 0x03) << 6) | fourth;
      outputIndex += 1;
    }
  }

  return bytes;
}

export function isBase64Url(value: unknown): value is string {
  if (!hasBase64UrlSyntax(value)) {
    return false;
  }

  return bytesToBase64Url(decodeBase64UrlSyntax(value)) === value;
}

export function bytesToBase64Url(value: unknown): string {
  const bytes = toBytes(value, "value", "INVALID_CREDENTIAL_ID");
  let result = "";

  for (let index = 0; index < bytes.length; index += 3) {
    const first = bytes[index] ?? 0;
    const second = bytes[index + 1];
    const third = bytes[index + 2];

    result += BASE64URL_ALPHABET.charAt(first >> 2);

    if (second === undefined) {
      result += BASE64URL_ALPHABET.charAt((first & 0x03) << 4);
      break;
    }

    result += BASE64URL_ALPHABET.charAt(
      ((first & 0x03) << 4) | (second >> 4),
    );

    if (third === undefined) {
      result += BASE64URL_ALPHABET.charAt((second & 0x0f) << 2);
      break;
    }

    result += BASE64URL_ALPHABET.charAt(
      ((second & 0x0f) << 2) | (third >> 6),
    );
    result += BASE64URL_ALPHABET.charAt(third & 0x3f);
  }

  return result;
}

export function base64UrlToBytes(value: unknown): Uint8Array {
  if (!hasBase64UrlSyntax(value)) {
    return fail(
      "INVALID_BASE64URL",
      "credential ID must be a non-empty, unpadded base64url string",
    );
  }

  const bytes = decodeBase64UrlSyntax(value);

  if (bytesToBase64Url(bytes) !== value) {
    return fail("INVALID_BASE64URL", "credential ID is not canonical base64url");
  }

  return bytes;
}

function credentialIdToBytes(value: unknown, label: string): Uint8Array {
  if (typeof value === "string") {
    try {
      return base64UrlToBytes(value);
    } catch {
      return fail(
        "INVALID_CREDENTIAL_ID",
        `${label} must be a BufferSource or a canonical base64url string`,
      );
    }
  }

  return toBytes(value, label, "INVALID_CREDENTIAL_ID");
}

function allowedCredentialIds(
  allowCredentials: unknown,
): Set<string> {
  if (allowCredentials === undefined) {
    return new Set();
  }

  if (!Array.isArray(allowCredentials)) {
    return fail(
      "INVALID_ALLOW_CREDENTIALS",
      "allowCredentials must be an array when provided",
    );
  }

  const ids = new Set<string>();

  for (let index = 0; index < allowCredentials.length; index += 1) {
    const entry = allowCredentials[index];
    let credentialId: Uint8Array;

    if (isRecord(entry) && "id" in entry) {
      credentialId = credentialIdToBytes(
        entry.id,
        `allowCredentials[${index}].id`,
      );
    } else {
      credentialId = credentialIdToBytes(
        entry,
        `allowCredentials[${index}]`,
      );
    }

    ids.add(bytesToBase64Url(credentialId));
  }

  return ids;
}

function parsePrfValues(value: unknown, label: string): ParsedPrfValues {
  if (!isRecord(value)) {
    return fail("INVALID_PRF_VALUES", `${label} must be an object`);
  }

  const firstValue = value.first;

  if (firstValue === undefined) {
    return fail("MISSING_FIRST", `${label}.first is required`);
  }

  const first = toBytes(firstValue, `${label}.first`, "INVALID_FIRST_SALT");
  const secondValue = value.second;
  let second: Uint8Array | undefined;

  if (secondValue !== undefined) {
    second = toBytes(secondValue, `${label}.second`, "INVALID_SECOND_SALT");
  }

  return second === undefined ? { first } : { first, second };
}

function parsePrfInputs(
  value: unknown,
  allowCredentials: unknown,
): ParsedPrfInputs {
  if (!isRecord(value)) {
    return fail("INVALID_PRF", "PRF extension input must be an object");
  }

  const allowedIds = allowedCredentialIds(allowCredentials);
  const evalValue = value.eval;
  const mappingValue = value.evalByCredential;
  const hasEval = evalValue !== undefined;
  const hasEvalByCredential = mappingValue !== undefined;

  if (!hasEval && !hasEvalByCredential) {
    return fail("MISSING_FIRST", "PRF extension input must contain first salt values");
  }

  const parsed: ParsedPrfInputs = {};

  if (hasEval) {
    parsed.eval = parsePrfValues(evalValue, "eval");
  }

  if (hasEvalByCredential) {
    if (allowedIds.size === 0) {
      return fail(
        "EVAL_BY_CREDENTIAL_WITHOUT_ALLOW_CREDENTIALS",
        "evalByCredential is not allowed when allowCredentials is empty",
      );
    }

    const mapping = mappingValue;

    if (!isRecord(mapping)) {
      return fail(
        "INVALID_PRF_VALUES",
        "evalByCredential must be an object keyed by base64url credential IDs",
      );
    }

    const keys = Object.keys(mapping);

    if (keys.length === 0 && !hasEval) {
      return fail("MISSING_FIRST", "evalByCredential must contain at least one first salt");
    }

    const parsedMapping: Record<string, ParsedPrfValues> = {};

    for (const key of keys) {
      let canonicalKey: string;

      try {
        canonicalKey = bytesToBase64Url(base64UrlToBytes(key));
      } catch {
        return fail(
          "INVALID_BASE64URL",
          "evalByCredential keys must be canonical base64url strings",
        );
      }

      if (!allowedIds.has(canonicalKey)) {
        return fail(
          "UNKNOWN_CREDENTIAL",
          "evalByCredential key is not present in allowCredentials",
        );
      }

      parsedMapping[canonicalKey] = parsePrfValues(
        mapping[key],
        `evalByCredential[${canonicalKey}]`,
      );
    }

    parsed.evalByCredential = parsedMapping;
  }

  return parsed;
}

function getSubtle(explicit?: PrfDigest): PrfDigest {
  if (explicit !== undefined) {
    if (typeof explicit.digest !== "function") {
      return fail("CRYPTO_UNAVAILABLE", "provided digest implementation is invalid");
    }

    return explicit;
  }

  const subtle = (
    globalThis as {
      crypto?: {
        subtle?: PrfDigest;
      };
    }
  ).crypto?.subtle;

  if (subtle === undefined || typeof subtle.digest !== "function") {
    return fail("CRYPTO_UNAVAILABLE", "Web Crypto SHA-256 support is unavailable");
  }

  return subtle;
}

/**
 * Derive the WebAuthn PRF salt:
 * SHA-256("WebAuthn PRF" || 0x00 || salt)
 */
export async function derivePrfSalt(
  salt: unknown,
  subtle?: PrfDigest,
): Promise<Uint8Array> {
  const saltBytes = toBytes(salt, "salt", "INVALID_FIRST_SALT");
  const message = new Uint8Array(PRF_DOMAIN_BYTES.length + 1 + saltBytes.length);

  message.set(PRF_DOMAIN_BYTES, 0);
  message[PRF_DOMAIN_BYTES.length] = 0x00;
  message.set(saltBytes, PRF_DOMAIN_BYTES.length + 1);

  const digest = getSubtle(subtle);
  let result: unknown;

  try {
    result = await digest.digest("SHA-256", message);
  } catch (error) {
    throw new PrfValidationError(
      "INVALID_DIGEST",
      `SHA-256 failed: ${error instanceof Error ? error.message : String(error)}`,
    );
  }

  if (
    !isArrayBuffer(result) &&
    !ArrayBuffer.isView(result)
  ) {
    return fail("INVALID_DIGEST", "SHA-256 did not return a BufferSource");
  }

  const bytes = toBytes(result, "SHA-256 result", "INVALID_DIGEST");

  if (bytes.byteLength !== 32) {
    return fail("INVALID_DIGEST", "SHA-256 must produce exactly 32 bytes");
  }

  return new Uint8Array(bytes);
}

async function preparePrfValues(
  values: ParsedPrfValues,
  subtle?: PrfDigest,
): Promise<PreparedPrfValues> {
  const first = derivePrfSalt(values.first, subtle);
  const second =
    values.second === undefined
      ? undefined
      : derivePrfSalt(values.second, subtle);

  const preparedFirst = await first;

  if (second === undefined) {
    return { first: preparedFirst };
  }

  return {
    first: preparedFirst,
    second: await second,
  };
}

/**
 * Validate PRF input and return fresh values whose salts have been hashed and
 * whose evalByCredential keys have been checked against allowCredentials.
 */
export async function preparePrfInput(
  input: unknown,
  allowCredentials: readonly AllowCredentialLike[] | null = [],
  subtle?: PrfDigest,
): Promise<PreparedPrfInputs> {
  const parsed = parsePrfInputs(input, allowCredentials);
  const prepared: PreparedPrfInputs = {};

  if (parsed.eval !== undefined) {
    prepared.eval = await preparePrfValues(parsed.eval, subtle);
  }

  if (parsed.evalByCredential !== undefined) {
    const mapping = parsed.evalByCredential;
    const entries = await Promise.all(
      Object.keys(mapping).map(async (key) => {
        const values = mapping[key];

        if (values === undefined) {
          return fail(
            "INVALID_PRF_VALUES",
            `evalByCredential[${key}] is missing`,
          );
        }

        const preparedValues = await preparePrfValues(
          values,
          subtle,
        );

        return [key, preparedValues] as const;
      }),
    );

    prepared.evalByCredential = Object.fromEntries(entries);
  }

  return prepared;
}

/**
 * Validate either a bare PRF extension input or an extensions object containing
 * a `prf` member. The returned object uses the same shape as the input.
 */
export async function validatePrfExtension(
  input: unknown,
  allowCredentials: readonly AllowCredentialLike[] | null = [],
  subtle?: PrfDigest,
): Promise<PreparedPrfInputs | ({ prf: PreparedPrfInputs } & Record<string, unknown>)> {
  if (isRecord(input) && hasDefinedOwn(input, "prf")) {
    const prepared = await preparePrfInput(
      input.prf,
      allowCredentials,
      subtle,
    );

    return {
      ...input,
      prf: prepared,
    };
  }

  return preparePrfInput(input, allowCredentials, subtle);
}

export function assertPrfInputShape(
  input: unknown,
  allowCredentials: readonly AllowCredentialLike[] | null = [],
): void {
  parsePrfInputs(input, allowCredentials);
}

export function isValidPrfInput(
  input: unknown,
  allowCredentials: readonly AllowCredentialLike[] | null = [],
): boolean {
  try {
    parsePrfInputs(input, allowCredentials);
    return true;
  } catch {
    return false;
  }
}

export async function isValidPrfExtension(
  input: unknown,
  allowCredentials: readonly AllowCredentialLike[] | null = [],
  subtle?: PrfDigest,
): Promise<boolean> {
  try {
    await validatePrfExtension(input, allowCredentials, subtle);
    return true;
  } catch {
    return false;
  }
}

export const validatePrfInput = preparePrfInput;
export const normalizePrfInput = preparePrfInput;
export const validatePrf = validatePrfExtension;
export const preparePrfExtension = validatePrfExtension;
export const hashPrfSalt = derivePrfSalt;
export const hashSalt = derivePrfSalt;

const prfValidation = {
  PRF_DOMAIN,
  PrfValidationError,
  assertPrfInputShape,
  base64UrlToBytes,
  bytesToBase64Url,
  derivePrfSalt,
  hashPrfSalt,
  hashSalt,
  isBase64Url,
  isValidPrfExtension,
  isValidPrfInput,
  normalizePrfInput,
  preparePrfExtension,
  preparePrfInput,
  validatePrf,
  validatePrfExtension,
  validatePrfInput,
};

export default prfValidation;
