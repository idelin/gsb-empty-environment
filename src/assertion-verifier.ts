import { createHash } from "node:crypto";
import { parseAuthenticatorData } from "./authenticator-data.ts";
import {
  base64UrlEncode,
  concatBytes,
  timingSafeEqual,
  toBytes,
  utf8Encode,
  type BytesInput,
} from "./bytes.ts";
import {
  ChallengeError,
  ClientTypeMismatchError,
  CloneDetectedError,
  CredentialLockedError,
  CredentialNotFoundError,
  CrossOriginForbiddenError,
  BackupEligibilityTransitionError,
  BackupFlagsError,
  DeviceBoundPolicyError,
  OriginMismatchError,
  RelyingPartyIdMismatchError,
  SignatureError,
  UserPresenceError,
  UserVerificationError,
  WebAuthnVerificationError,
} from "./errors.ts";
import { WEB_AUTHN_GET_TYPE, challengeMatches, parseClientDataJSON } from "./client-data.ts";
import { InMemoryChallengeStore, type ChallengeStore } from "./challenge-store.ts";
import { type CredentialRecord, type CredentialStore } from "./credential-store.ts";
import { sha256, verifyAssertionSignature, type SignatureVerifier } from "./signature.ts";

export interface AssertionInput {
  /** Raw authenticator data bytes (or base64url string). */
  authenticatorData: BytesInput;
  /** Raw clientDataJSON bytes (or base64url string). */
  clientDataJSON: BytesInput;
  /** Raw assertion signature bytes (or base64url string). */
  signature: BytesInput;
  /** Credential identifier carried by the browser assertion. */
  credentialId: BytesInput;
}

export interface VerifyAssertionOptions {
  assertion: AssertionInput;
  /** Expected relying party identifier, e.g. `example.com`. */
  rpId: string;
  /** Exact allowed origin string, or several exact origins. */
  expectedOrigin: string | readonly string[];
  /**
   * One-time challenge handling:
   * - `ChallengeStore`: atomic store-backed consumption
   * - raw bytes/string: one-shot expected value (replay protection requires a store)
   */
  challenge: BytesInput | ChallengeStore;
  credentialStore: CredentialStore;
  /** Reject `crossOrigin === true`; defaults to true. */
  rejectCrossOrigin?: boolean;
   /** Require user verification (UV flag); defaults to true. */
  requireUserVerification?: boolean;
  /** Reject any BE=1 synced passkey (device-bound-only tenant policy). */
  requireDeviceBoundCredential?: boolean;
  /** Inject an external signature verifier (HSM, KMS, WebCrypto bridge...). */
  signatureVerifier?: SignatureVerifier;
  /** Enforce P-256 low-S canonical signatures; defaults to true. */
  requireLowS?: boolean;
  /** Clock injection for tests. */
  now?: () => number;
}

export interface VerifiedAssertion {
  credentialId: Uint8Array;
  signCount: number;
  previousSignCount: number;
  backupEligible: boolean;
  backupState: boolean;
  credential: CredentialRecord;
}

function isChallengeStore(value: BytesInput | ChallengeStore): value is ChallengeStore {
  return (
    typeof value === "object" &&
    value !== null &&
    typeof (value as ChallengeStore).consume === "function"
  );
}

function sha256RelyingPartyId(rpId: string): Uint8Array {
  return new Uint8Array(createHash("sha256").update(utf8Encode(rpId)).digest());
}

function assertOrigin(observed: string, expected: string | readonly string[]): void {
  const allowed = Array.isArray(expected) ? expected : [expected];
  if (!allowed.some((origin) => origin === observed)) {
    throw new OriginMismatchError(
      `Origin ${observed} does not match any expected origin (${allowed.join(", ")})`,
    );
  }
}

/**
 * Full WebAuthn L3 `webauthn.get` assertion verification pipeline.
 *
 * Ordering matters: the challenge is atomically consumed before any await,
 * structural/flag checks precede signature verification, and clone/state
 * machine failures lock the credential before any state is persisted.
 */
export async function verifyAssertion(
  options: VerifyAssertionOptions,
): Promise<VerifiedAssertion> {
  const now = options.now ?? (() => Date.now());
  const rejectCrossOrigin = options.rejectCrossOrigin ?? true;
  const requireUserVerification = options.requireUserVerification ?? true;
  const requireLowS = options.requireLowS ?? true;

  const authDataBytes = toBytes(options.assertion.authenticatorData);
  const clientDataBytes = toBytes(options.assertion.clientDataJSON);
  const signatureBytes = toBytes(options.assertion.signature);
  const credentialId = toBytes(options.assertion.credentialId);

  // 1. Parse both structures first so malformed inputs fail fast.
  const authData = parseAuthenticatorData(authDataBytes);
  const clientData = parseClientDataJSON(clientDataBytes);

  // 2. Atomic one-time challenge consumption, before any await boundary.
  if (isChallengeStore(options.challenge)) {
    options.challenge.consume({ expected: toBytes(clientData.challenge), now: now() });
  } else {
    const expected = toBytes(options.challenge);
    if (!challengeMatches(expected, clientData.challenge)) {
      throw new ChallengeError("CHALLENGE_MISMATCH", "Assertion challenge does not match expected value");
    }
  }

  // 3. Relying party binding.
  const expectedRpIdHash = sha256RelyingPartyId(options.rpId);
  if (!timingSafeEqual(authData.rpIdHash, expectedRpIdHash)) {
    throw new RelyingPartyIdMismatchError("rpIdHash does not equal SHA-256(rpId)");
  }

  // 4. Client data semantics.
  if (clientData.type !== WEB_AUTHN_GET_TYPE) {
    throw new ClientTypeMismatchError(
      `clientDataJSON type must be ${WEB_AUTHN_GET_TYPE}, got ${clientData.type}`,
    );
  }
  assertOrigin(clientData.origin, options.expectedOrigin);
  if (rejectCrossOrigin && clientData.crossOrigin) {
    throw new CrossOriginForbiddenError("crossOrigin=true is forbidden by policy");
  }

  // 5. User presence/verification flags.
  if (!authData.up) {
    throw new UserPresenceError("UP flag must be set for a WebAuthn assertion");
  }
  if (requireUserVerification && !authData.uv) {
    throw new UserVerificationError("UV flag is required by policy but was not set");
  }

  // 6. Backup flag invariants.
  if (!authData.be && authData.bs) {
    throw new BackupFlagsError("Illegal flag combination: BS=1 while BE=0");
  }
  if (options.requireDeviceBoundCredential && authData.be) {
    throw new DeviceBoundPolicyError("Tenant policy requires device-bound credentials; BE=1 is rejected");
  }

  // 7. Load credential and reject locked records before cryptographic work.
  const record = await options.credentialStore.get(credentialId);
  if (record === null) {
    throw new CredentialNotFoundError(`No credential registered for id ${base64UrlEncode(credentialId)}`);
  }
  if (record.locked) {
    throw new CredentialLockedError(
      `Credential is locked${record.lockedReason ? `: ${record.lockedReason}` : ""}`,
    );
  }

  // 8. Backup Eligibility state machine: a device-bound key can never become
  //    syncable later; per L3 this transition is rejected outright.
  if (record.backupEligible === false && authData.be) {
    await lockCredential(
      options.credentialStore,
      record,
      now,
      "Backup Eligibility transitioned from false to true",
    );
    throw new BackupEligibilityTransitionError(
      "BE flag transitioned from false (registered) to true (assertion)",
    );
  }

  // 9. Signature over authData || SHA-256(clientDataJSON).
  const signedData = concatBytes(authData.raw, sha256(clientData.raw));
  const signatureValid = verifyAssertionSignature({
    algorithm: record.algorithm,
    publicKey: record.publicKey,
    signedData,
    signature: signatureBytes,
    verifier: options.signatureVerifier,
    requireLowS,
  });
  if (!signatureValid) {
    throw new SignatureError("Assertion signature verification failed");
  }

  // 10. Clone detection via the signature counter state machine.
  const storedSignCount = record.signCount;
  const newSignCount = authData.signCount;
  if (
    (storedSignCount !== 0 || newSignCount !== 0) &&
    newSignCount <= storedSignCount
  ) {
    await lockCredential(
      options.credentialStore,
      record,
      now,
      `signCount regression/stagnation: stored=${storedSignCount}, observed=${newSignCount}`,
    );
    throw new CloneDetectedError(
      "Potential cloned authenticator: signCount did not strictly increase",
      storedSignCount,
      newSignCount,
    );
  }

  // 11. Persist the new state only after every check passed.
  const updated: CredentialRecord = {
    ...record,
    signCount: newSignCount,
    backupEligible: authData.be,
    backupState: authData.bs,
    updatedAt: now(),
  };
  await options.credentialStore.save(updated);

  return {
    credentialId: new Uint8Array(credentialId),
    signCount: newSignCount,
    previousSignCount: storedSignCount,
    backupEligible: authData.be,
    backupState: authData.bs,
    credential: updated,
  };
}

async function lockCredential(
  store: CredentialStore,
  record: CredentialRecord,
  now: () => number,
  reason: string,
): Promise<void> {
  await store.save({
    ...record,
    locked: true,
    lockedReason: reason,
    updatedAt: now(),
  });
}

export { InMemoryChallengeStore };
export type { ChallengeStore };
export { WebAuthnVerificationError };
