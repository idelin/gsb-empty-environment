export {
  verifyAssertion,
  InMemoryChallengeStore,
  type AssertionInput,
  type VerifyAssertionOptions,
  type VerifiedAssertion,
  type ChallengeStore,
} from "./assertion-verifier.ts";

export {
  parseAuthenticatorData,
  AUTHENTICATOR_DATA_MIN_LENGTH,
  FLAG_UP,
  FLAG_UV,
  FLAG_BE,
  FLAG_BS,
  FLAG_AT,
  FLAG_ED,
  type ParsedAuthenticatorData,
} from "./authenticator-data.ts";

export {
  parseClientDataJSON,
  encodeClientDataJSON,
  WEB_AUTHN_GET_TYPE,
  type ParsedClientData,
} from "./client-data.ts";

export {
  InMemoryCredentialStore,
  toIdentifierBytes,
  type CredentialRecord,
  type CredentialStore,
  type PublicKeyInput,
} from "./credential-store.ts";

export {
  verifyAssertionSignature,
  parseStrictDerEcdsaSignature,
  assertLowSP256,
  loadPublicKey,
  sha256,
  type SignatureVerifier,
  type EcdsaSignature,
} from "./signature.ts";

export {
  WebAuthnVerificationError,
  WebAuthnVerificationError as WebAuthnError,
  type WebAuthnErrorCode,
  AuthenticatorDataError,
  ClientDataError,
  RelyingPartyIdMismatchError,
  ClientTypeMismatchError,
  OriginMismatchError,
  CrossOriginForbiddenError,
  ChallengeError,
  UserPresenceError,
  UserVerificationError,
  BackupFlagsError,
  BackupEligibilityTransitionError,
  DeviceBoundPolicyError,
  CredentialNotFoundError,
  CredentialLockedError,
  CloneDetectedError,
  SignatureError,
  PublicKeyUnsupportedError,
} from "./errors.ts";

export {
  base64UrlEncode,
  base64UrlDecode,
  base64Encode,
  toBytes,
  concatBytes,
  timingSafeEqual,
  type BytesInput,
} from "./bytes.ts";

export { COSE_ALG, algorithmNameFromCose, type JsonWebKey, type KeyAlgorithmName } from "./webauthn-types.ts";
