export { CrossDeviceAuthHost } from "./host.ts";
export type {
  BrowserRegistration,
  DeviceRegistration,
  RevocationResult,
} from "./host.ts";

export {
  createEd25519CredentialDouble,
  createDeviceCredentialDouble,
  createBrowserCredentialDouble,
} from "./credentials.ts";
export type {
  CredentialDoubleOptions,
  Ed25519CredentialDouble,
} from "./credentials.ts";

export {
  createBrowserProof,
  createDeviceConfirmation,
  createEmailToken,
  emailTokenHash,
  parseEmailToken,
  verifyBrowserProof,
  verifyDeviceConfirmation,
  verifyEmailToken,
} from "./messages.ts";

export {
  createEd25519HostKeyPair,
  hostKeyPairFromPem,
  keyIdForPublicKey,
  normalizePublicKey,
} from "./keys.ts";

export {
  createJournalRecord,
  verifyJournalRecord,
  AsyncMutex,
  FaultInjectingJournalStore,
  FileJournalStore,
  MemoryJournalStore,
  journalDigest,
} from "./journal.ts";

export {
  verifyEmailDeliveryPair,
  verifyIssuedAuthorization,
  verifyReceipt,
  verifyReceiptChain,
} from "./receipts.ts";

export {
  authorizationAudit,
  challengeStatus,
  challengeStatusByToken,
  challengeStatusByTokenId,
  emptyState,
  applyEvent,
  publicChallengeStatus,
} from "./state.ts";

export { InMemoryIssuanceService } from "./issuance-double.ts";
export type { IssuanceDoubleOptions } from "./issuance-double.ts";

export { deriveTransactionId } from "./protocol-ids.ts";
export {
  canonicalJson,
  deriveUserCode,
  hashDomain,
  sha256,
} from "./canonical.ts";
export { ProtocolError } from "./types.ts";

export type {
  AuthEvent,
  AuthState,
  AuthorizationAudit,
  AuthorizationAuditEntry,
  BeginLoginInput,
  BrowserProof,
  BrowserProofPayload,
  BrowserRecord,
  ChallengeState,
  ChallengeStatus,
  ChallengeView,
  Clock,
  ConfirmationResult,
  DeliveryResult,
  DeviceConfirmation,
  DeviceConfirmationPayload,
  DeviceRecord,
  Ed25519Credential,
  EmailOpenRecord,
  EmailToken,
  EmailTokenClaims,
  HostKeyPair,
  HostOptions,
  IssuanceLookup,
  IssuanceRequest,
  IssuanceService,
  IssuedCredential,
  JournalRecord,
  JournalStore,
  LoginStart,
  OfflineAcceptanceResult,
  OpenResult,
  ReceiptType,
  RedeemInput,
  RedeemResult,
  SignedReceipt,
} from "./types.ts";

import { CrossDeviceAuthHost } from "./host.ts";
import type { HostOptions } from "./types.ts";

export async function createAuthHost(options: HostOptions): Promise<CrossDeviceAuthHost> {
  return CrossDeviceAuthHost.open(options);
}

export const startAuthHost = createAuthHost;
