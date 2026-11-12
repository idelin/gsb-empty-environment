/**
 * Machine-readable failure codes. Every rejection thrown by this package uses
 * one of these codes so a validation library can map failures to responses.
 */
export type SdJwtErrorCode =
  | "MALFORMED_PRESENTATION"
  | "UNSUPPORTED_SD_ALG"
  | "MALFORMED_DISCLOSURE"
  | "MALFORMED_SD_ARRAY"
  | "MALFORMED_ARRAY_MARKER"
  | "DUPLICATE_SD_DIGEST"
  | "DUPLICATE_DISCLOSURE_DIGEST"
  | "UNREFERENCED_DISCLOSURE"
  | "RESERVED_DISCLOSURE_NAME"
  | "CLAIM_NAME_CONFLICT"
  | "MALFORMED_KB_JWT"
  | "KB_SD_HASH_MISMATCH"
  | "KB_NONCE_MISMATCH"
  | "KB_AUD_MISMATCH";

export class SdJwtVerificationError extends Error {
  override readonly name = "SdJwtVerificationError";
  readonly code: SdJwtErrorCode;

  constructor(code: SdJwtErrorCode, message: string) {
    super(message);
    this.code = code;
  }
}
