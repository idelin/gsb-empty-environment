/**
 * Stable error codes emitted by the assertion verifier.
 * Hosts should branch on `code` rather than on the message text.
 */
export type WebAuthnErrorCode =
  | "AUTHENTICATOR_DATA_MALFORMED"
  | "CLIENT_DATA_MALFORMED"
  | "RP_ID_MISMATCH"
  | "CLIENT_TYPE_MISMATCH"
  | "ORIGIN_MISMATCH"
  | "CROSS_ORIGIN_FORBIDDEN"
  | "CHALLENGE_MISSING"
  | "CHALLENGE_MISMATCH"
  | "CHALLENGE_REPLAY"
  | "CHALLENGE_EXPIRED"
  | "USER_PRESENCE_REQUIRED"
  | "USER_VERIFICATION_REQUIRED"
  | "BACKUP_FLAGS_INVALID"
  | "BACKUP_ELIGIBILITY_TRANSITION"
  | "DEVICE_BOUND_POLICY"
  | "CREDENTIAL_NOT_FOUND"
  | "CREDENTIAL_LOCKED"
  | "CLONE_DETECTED"
  | "SIGNATURE_INVALID"
  | "PUBLIC_KEY_UNSUPPORTED";

export class WebAuthnVerificationError extends Error {
  readonly code: WebAuthnErrorCode;

  constructor(code: WebAuthnErrorCode, message: string, options?: { cause?: unknown }) {
    super(message, options);
    this.name = "WebAuthnVerificationError";
    this.code = code;
  }
}

export class AuthenticatorDataError extends WebAuthnVerificationError {
  constructor(message: string, options?: { cause?: unknown }) {
    super("AUTHENTICATOR_DATA_MALFORMED", message, options);
    this.name = "AuthenticatorDataError";
  }
}

export class ClientDataError extends WebAuthnVerificationError {
  constructor(message: string, options?: { cause?: unknown }) {
    super("CLIENT_DATA_MALFORMED", message, options);
    this.name = "ClientDataError";
  }
}

export class RelyingPartyIdMismatchError extends WebAuthnVerificationError {
  constructor(message: string) {
    super("RP_ID_MISMATCH", message);
    this.name = "RelyingPartyIdMismatchError";
  }
}

export class ClientTypeMismatchError extends WebAuthnVerificationError {
  constructor(message: string) {
    super("CLIENT_TYPE_MISMATCH", message);
    this.name = "ClientTypeMismatchError";
  }
}

export class OriginMismatchError extends WebAuthnVerificationError {
  constructor(message: string) {
    super("ORIGIN_MISMATCH", message);
    this.name = "OriginMismatchError";
  }
}

export class CrossOriginForbiddenError extends WebAuthnVerificationError {
  constructor(message: string) {
    super("CROSS_ORIGIN_FORBIDDEN", message);
    this.name = "CrossOriginForbiddenError";
  }
}

export class ChallengeError extends WebAuthnVerificationError {}

export class UserPresenceError extends WebAuthnVerificationError {
  constructor(message: string) {
    super("USER_PRESENCE_REQUIRED", message);
    this.name = "UserPresenceError";
  }
}

export class UserVerificationError extends WebAuthnVerificationError {
  constructor(message: string) {
    super("USER_VERIFICATION_REQUIRED", message);
    this.name = "UserVerificationError";
  }
}

export class BackupFlagsError extends WebAuthnVerificationError {
  constructor(message: string) {
    super("BACKUP_FLAGS_INVALID", message);
    this.name = "BackupFlagsError";
  }
}

export class BackupEligibilityTransitionError extends WebAuthnVerificationError {
  constructor(message: string) {
    super("BACKUP_ELIGIBILITY_TRANSITION", message);
    this.name = "BackupEligibilityTransitionError";
  }
}

export class DeviceBoundPolicyError extends WebAuthnVerificationError {
  constructor(message: string) {
    super("DEVICE_BOUND_POLICY", message);
    this.name = "DeviceBoundPolicyError";
  }
}

export class CredentialNotFoundError extends WebAuthnVerificationError {
  constructor(message: string) {
    super("CREDENTIAL_NOT_FOUND", message);
    this.name = "CredentialNotFoundError";
  }
}

export class CredentialLockedError extends WebAuthnVerificationError {
  constructor(message: string) {
    super("CREDENTIAL_LOCKED", message);
    this.name = "CredentialLockedError";
  }
}

export class CloneDetectedError extends WebAuthnVerificationError {
  readonly storedSignCount: number;
  readonly observedSignCount: number;

  constructor(message: string, storedSignCount: number, observedSignCount: number) {
    super("CLONE_DETECTED", message);
    this.name = "CloneDetectedError";
    this.storedSignCount = storedSignCount;
    this.observedSignCount = observedSignCount;
  }
}

export class SignatureError extends WebAuthnVerificationError {
  constructor(message: string, options?: { cause?: unknown }) {
    super("SIGNATURE_INVALID", message, options);
    this.name = "SignatureError";
  }
}

export class PublicKeyUnsupportedError extends WebAuthnVerificationError {
  constructor(message: string, options?: { cause?: unknown }) {
    super("PUBLIC_KEY_UNSUPPORTED", message, options);
    this.name = "PublicKeyUnsupportedError";
  }
}
