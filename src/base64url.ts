import { JweSecurityError, type JweSecurityErrorCode } from "./errors.ts";

const BASE64URL_ALPHABET = /^[A-Za-z0-9_-]*$/;

export interface Base64UrlDecodeOptions {
  readonly name: string;
  readonly allowEmpty?: boolean;
  readonly maxBytes?: number;
  readonly nonCanonicalCode?: JweSecurityErrorCode;
  readonly invalidCode?: JweSecurityErrorCode;
}

export function decodeBase64UrlStrict(
  value: string,
  options: Base64UrlDecodeOptions,
): Buffer {
  const {
    name,
    allowEmpty = false,
    maxBytes,
    nonCanonicalCode = "JWE_BASE64URL_NON_CANONICAL",
    invalidCode = "JWE_BASE64URL_INVALID",
  } = options;

  if (typeof value !== "string") {
    throw new JweSecurityError(
      invalidCode,
      `${name} must be a Base64URL string`,
      { name },
    );
  }

  if (value.length === 0) {
    if (allowEmpty) {
      return Buffer.alloc(0);
    }
    throw new JweSecurityError(invalidCode, `${name} must not be empty`, {
      name,
    });
  }

  if (value.includes("=") || !BASE64URL_ALPHABET.test(value)) {
    throw new JweSecurityError(
      nonCanonicalCode,
      `${name} contains padding, whitespace, or characters outside Base64URL`,
      { name },
    );
  }

  let bytes: Buffer;
  try {
    bytes = Buffer.from(value, "base64url");
  } catch {
    throw new JweSecurityError(invalidCode, `${name} is not valid Base64URL`, {
      name,
    });
  }

  if (bytes.toString("base64url") !== value) {
    throw new JweSecurityError(
      nonCanonicalCode,
      `${name} is non-canonical Base64URL`,
      { name },
    );
  }

  if (maxBytes !== undefined && bytes.length > maxBytes) {
    throw new JweSecurityError(
      invalidCode,
      `${name} exceeds the configured byte limit`,
      { name, maxBytes, actualBytes: bytes.length },
    );
  }

  return bytes;
}

export function encodeBase64Url(value: Uint8Array | string): string {
  return Buffer.from(value).toString("base64url");
}
