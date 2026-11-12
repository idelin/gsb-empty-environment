import { timingSafeEqual } from "node:crypto";
import { b64urlDecodeJson, b64urlEncode } from "./base64url.ts";
import { sha256Hasher, type Hasher } from "./disclosures.ts";
import { SdJwtVerificationError } from "./errors.ts";

export interface VerifyKbJwtOptions {
  /** Compact KB-JWT carried after the disclosures in the presentation. */
  kbJwt: string;
  /**
   * The presented SD-JWT: issuer JWT plus `~`-joined disclosures, ending in
   * `~` when a KB-JWT follows (RFC 9812 Section 4). Its hash is sd_hash.
   */
  presentedSdJwt: string;
  /** Expected nonce previously communicated to the holder. */
  expectedNonce: string;
  /** Expected audience (verifier/resource server) identifier. */
  expectedAud: string;
  hasher?: Hasher;
}

interface KbHeader {
  typ?: unknown;
  alg?: unknown;
}

interface KbPayload {
  sd_hash?: unknown;
  nonce?: unknown;
  aud?: unknown;
  iat?: unknown;
}

function constantTimeEquals(left: string, right: string): boolean {
  const leftBytes = new TextEncoder().encode(left);
  const rightBytes = new TextEncoder().encode(right);
  if (leftBytes.length !== rightBytes.length) {
    return false;
  }
  return timingSafeEqual(leftBytes, rightBytes);
}

function audienceMatches(actual: unknown, expected: string): boolean {
  if (typeof actual === "string") {
    return constantTimeEquals(actual, expected);
  }
  if (Array.isArray(actual) && actual.every((entry) => typeof entry === "string")) {
    return (actual as string[]).some((entry) =>
      constantTimeEquals(entry, expected)
    );
  }
  return false;
}

function splitJwt(jwt: string): [string, string, string] {
  const parts = jwt.split(".");
  if (parts.length !== 3 || parts.some((part) => part.length === 0)) {
    throw new SdJwtVerificationError(
      "MALFORMED_KB_JWT",
      "KB-JWT must be a compact serialization with three non-empty parts",
    );
  }
  return [parts[0], parts[1], parts[2]];
}

/**
 * Verify the holder-binding claims of a KB-JWT.
 *
 * Per the task boundary this checks `sd_hash`, `nonce` and `aud` only; the
 * KB-JWT signature (proof of possession of the key in `cnf.jwk`) must be
 * verified by the caller together with the issuer JWT signature.
 */
export function verifyKbJwt(options: VerifyKbJwtOptions): {
  header: KbHeader;
  payload: KbPayload;
  sdHash: string;
} {
  const hasher = options.hasher ?? sha256Hasher;
  const [headerPart, payloadPart] = splitJwt(options.kbJwt);

  let header: KbHeader;
  let payload: KbPayload;
  try {
    header = b64urlDecodeJson<KbHeader>(headerPart);
    payload = b64urlDecodeJson<KbPayload>(payloadPart);
  } catch {
    throw new SdJwtVerificationError(
      "MALFORMED_KB_JWT",
      "KB-JWT header/payload are not base64url-encoded JSON",
    );
  }

  if (header.typ !== "kb+jwt") {
    throw new SdJwtVerificationError(
      "MALFORMED_KB_JWT",
      'KB-JWT header typ must be "kb+jwt"',
    );
  }
  if (typeof header.alg !== "string" || header.alg.length === 0) {
    throw new SdJwtVerificationError(
      "MALFORMED_KB_JWT",
      "KB-JWT header must carry a string alg",
    );
  }

  const expectedSdHash = b64urlEncode(
    hasher(new TextEncoder().encode(options.presentedSdJwt)),
  );
  if (
    typeof payload.sd_hash !== "string" ||
    !constantTimeEquals(payload.sd_hash, expectedSdHash)
  ) {
    throw new SdJwtVerificationError(
      "KB_SD_HASH_MISMATCH",
      "KB-JWT sd_hash does not match the hash of the presented SD-JWT",
    );
  }

  if (
    typeof payload.nonce !== "string" ||
    !constantTimeEquals(payload.nonce, options.expectedNonce)
  ) {
    throw new SdJwtVerificationError(
      "KB_NONCE_MISMATCH",
      "KB-JWT nonce does not match the expected nonce",
    );
  }

  if (!audienceMatches(payload.aud, options.expectedAud)) {
    throw new SdJwtVerificationError(
      "KB_AUD_MISMATCH",
      "KB-JWT aud does not match the expected audience",
    );
  }

  return { header, payload, sdHash: expectedSdHash };
}
