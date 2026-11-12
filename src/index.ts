import { verifyDisclosures } from "./claims.ts";
import type {
  DecodedDisclosure,
  Hasher,
} from "./disclosures.ts";
import { sha256Hasher } from "./disclosures.ts";
import { SdJwtVerificationError } from "./errors.ts";
import { verifyKbJwt } from "./kb.ts";

export {
  decodeDisclosure,
  decodeDisclosures,
  encodeDisclosure,
  sha256Hasher,
  type ArrayItemDisclosure,
  type DecodedDisclosure,
  type Hasher,
  type ParsedDisclosure,
  type PropertyDisclosure,
} from "./disclosures.ts";
export { verifyDisclosures } from "./claims.ts";
export {
  verifyKbJwt,
  type VerifyKbJwtOptions,
} from "./kb.ts";
export {
  SdJwtVerificationError,
  type SdJwtErrorCode,
} from "./errors.ts";
export {
  b64urlDecode,
  b64urlDecodeJson,
  b64urlDecodeString,
  b64urlEncode,
} from "./base64url.ts";

export interface VerifyPresentationOptions {
  /**
   * Combined presentation in compact serialization:
   * `<jwt>~<disclosure>~...~[<kb-jwt>]`.
   */
  presentation: string;
  /**
   * Issuer-signed JWT payload, already signature-verified and decoded by the
   * caller. This package never performs JWT signature verification.
   */
  signedClaims: Record<string, unknown>;
  /** Holder-binding expectations. When present the KB-JWT is mandatory. */
  kb?: {
    expectedNonce: string;
    expectedAud: string;
  };
  /** Override the hash function; only `_sd_alg: "sha-256"` (or absent) is accepted. */
  hasher?: Hasher;
}

export interface VerifiedPresentation {
  /** Reconstructed claims with all presented disclosures applied. */
  claims: Record<string, unknown>;
  disclosures: DecodedDisclosure[];
  kbJwt?: string;
  /** The exact string whose hash is the KB-JWT `sd_hash` input. */
  presentedSdJwt: string;
}

/**
 * Entry point for a validation library: verify every disclosure carried by a
 * holder presentation against an already issuer-verified payload, plus the
 * KB-JWT holder-binding claims when holder binding is required.
 */
export function verifySdJwtPresentation(
  options: VerifyPresentationOptions,
): VerifiedPresentation {
  const hasher = options.hasher ?? sha256Hasher;
  const sdAlg = options.signedClaims._sd_alg;
  if (sdAlg !== undefined && sdAlg !== "sha-256") {
    throw new SdJwtVerificationError(
      "UNSUPPORTED_SD_ALG",
      `Unsupported _sd_alg "${String(sdAlg)}"; only "sha-256" is supported`,
    );
  }

  const parts = options.presentation.split("~");
  const issuerJwt = parts[0];
  if (!issuerJwt || issuerJwt.split(".").length !== 3) {
    throw new SdJwtVerificationError(
      "MALFORMED_PRESENTATION",
      "Presentation must start with a compact issuer JWT",
    );
  }

  // Trailing element after the final '~' is the KB-JWT; it must be empty
  // when no KB-JWT is present, otherwise it must be a compact JWT.
  const trailing = parts[parts.length - 1];
  const hasKbSegment = trailing.length > 0;
  if (hasKbSegment && trailing.split(".").length !== 3) {
    throw new SdJwtVerificationError(
      "MALFORMED_PRESENTATION",
      "Trailing KB-JWT segment is not a compact JWT",
    );
  }
  if (options.kb && !hasKbSegment) {
    throw new SdJwtVerificationError(
      "MALFORMED_KB_JWT",
      "Holder binding is required but the presentation carries no KB-JWT",
    );
  }

  const encodedDisclosures = (
    hasKbSegment ? parts.slice(1, -1) : parts.slice(1, parts.length - 1)
  ).filter((entry) => entry.length > 0);

  const { claims, disclosures } = verifyDisclosures(
    options.signedClaims,
    encodedDisclosures,
    hasher,
  );

  // sd_hash covers issuer JWT + disclosures joined by '~', always ending '~'.
  const presentedSdJwt = [issuerJwt, ...encodedDisclosures].join("~") + "~";

  let kbJwt: string | undefined;
  if (hasKbSegment) {
    kbJwt = trailing;
    if (options.kb) {
      verifyKbJwt({
        kbJwt: trailing,
        presentedSdJwt,
        expectedNonce: options.kb.expectedNonce,
        expectedAud: options.kb.expectedAud,
        hasher,
      });
    }
  }

  return { claims, disclosures, kbJwt, presentedSdJwt };
}
