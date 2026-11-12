import { createHash } from "node:crypto";
import { b64urlDecodeJson, b64urlEncode } from "./base64url.ts";
import { SdJwtVerificationError } from "./errors.ts";

/** Default hash algorithm for SD-JWT (RFC 9812): SHA-256, base64url output. */
export type Hasher = (data: Uint8Array) => Uint8Array;

export const sha256Hasher: Hasher = (data) =>
  new Uint8Array(createHash("sha256").update(data).digest());

/** Object-property disclosure: [salt, claim name, claim value]. */
export interface PropertyDisclosure {
  kind: "property";
  salt: string;
  name: string;
  value: unknown;
}

/** Array-item disclosure: [salt, item value]. */
export interface ArrayItemDisclosure {
  kind: "array_item";
  salt: string;
  value: unknown;
}

export type ParsedDisclosure = PropertyDisclosure | ArrayItemDisclosure;

export interface DecodedDisclosure {
  /** Original base64url-encoded disclosure as carried in the presentation. */
  encoded: string;
  disclosure: ParsedDisclosure;
  /** Base64url hash of the encoded disclosure string. */
  digest: string;
}

function parseDisclosureArray(
  raw: unknown,
  encoded: string,
): ParsedDisclosure {
  if (!Array.isArray(raw) || (raw.length !== 3 && raw.length !== 2)) {
    throw new SdJwtVerificationError(
      "MALFORMED_DISCLOSURE",
      `Disclosure ${encoded} must be a JSON array of [salt, name, value] or [salt, value]`,
    );
  }

  const [salt, second, third] = raw;
  if (typeof salt !== "string" || salt.length === 0) {
    throw new SdJwtVerificationError(
      "MALFORMED_DISCLOSURE",
      `Disclosure ${encoded} has an invalid salt`,
    );
  }

  if (raw.length === 3) {
    if (typeof second !== "string") {
      throw new SdJwtVerificationError(
        "MALFORMED_DISCLOSURE",
        `Disclosure ${encoded} has a non-string claim name`,
      );
    }
    return { kind: "property", salt, name: second, value: third };
  }
  return { kind: "array_item", salt, value: second };
}

/**
 * Decode one base64url(JSON) disclosure and compute its digest.
 * The digest is always taken over the encoded string's bytes, never over the
 * decoded JSON, so re-serialization cannot change the digest.
 */
export function decodeDisclosure(
  encoded: string,
  hasher: Hasher = sha256Hasher,
): DecodedDisclosure {
  if (typeof encoded !== "string" || encoded.length === 0) {
    throw new SdJwtVerificationError(
      "MALFORMED_DISCLOSURE",
      "Disclosure must be a non-empty base64url string",
    );
  }

  let raw: unknown;
  try {
    raw = b64urlDecodeJson(encoded);
  } catch {
    throw new SdJwtVerificationError(
      "MALFORMED_DISCLOSURE",
      `Disclosure ${encoded} is not base64url-encoded JSON`,
    );
  }

  const disclosure = parseDisclosureArray(raw, encoded);
  const digest = b64urlEncode(
    hasher(new TextEncoder().encode(encoded)),
  );
  return { encoded, disclosure, digest };
}

export function decodeDisclosures(
  encoded: readonly string[],
  hasher: Hasher = sha256Hasher,
): DecodedDisclosure[] {
  return encoded.map((entry) => decodeDisclosure(entry, hasher));
}

/** Convenience helper for tests and for issuers building presentations. */
export function encodeDisclosure(
  disclosure: readonly [string, string, unknown] | readonly [string, unknown],
): string {
  return b64urlEncode(JSON.stringify(disclosure));
}
