import type { KeyObject } from "node:crypto";

export type Clock = () => number;

export interface HostKeyPair {
  privateKey: KeyObject;
  publicKey: KeyObject;
  publicKeyPem: string;
  keyId: string;
}

export interface Ed25519Credential {
  keyId: string;
  publicKey: string;
  privateKey: KeyObject;
  signPayload(domain: string, payload: unknown): string;
}

export interface KeyRecord {
  keyId: string;
  publicKey: string;
  generation: number;
  registeredAt: number;
  retiredAt?: number;
}

export interface BrowserRecord {
  accountId: string;
  browserId: string;
  keys: Record<string, KeyRecord>;
  activeKeyId: string;
  revokedAt?: number;
  revocationReceipt?: SignedReceipt;
  createdAt: number;
}

export interface DeviceRecord {
  accountId: string;
  deviceId: string;
  keyId: string;
  publicKey: string;
  generation: number;
  registeredAt: number;
  revokedAt?: number;
  replacedBy?: string;
}

export interface EmailTokenClaims {
  v: 1;
  tokenId: string;
  challengeId: string;
  accountId: string;
  email: string;
  browserId: string;
  browserSessionId: string | null;
  browserKeyId: string;
  browserPublicKey: string;
  challenge: string;
  purpose: string;
  userCode: string;
  issuedAt: number;
  expiresAt: number;
}

export interface EmailToken {
  claims: EmailTokenClaims;
  signature: string;
  encoded: string;
}

export interface DeviceConfirmationPayload {
  v: 1;
  confirmationId: string;
  tokenId: string;
  challengeId: string;
  challenge: string;
  accountId: string;
  email: string;
  purpose: string;
  expiresAt: number;
  browserId: string;
  browserSessionId: string | null;
  browserKeyId: string;
  selectedBrowserId: string;
  selectedBrowserSessionId: string | null;
  deviceId: string;
  deviceKeyId: string;
  confirmedAt: number;
}

export interface DeviceConfirmation {
  payload: DeviceConfirmationPayload;
  signature: string;
  signerPublicKey: string;
}

export interface BrowserProofPayload {
  v: 1;
  proofId: string;
  tokenId: string;
  challengeId: string;
  challenge: string;
  accountId: string;
  browserId: string;
  browserSessionId: string | null;
  browserKeyId: string;
  purpose: string;
  signedAt: number;
}

export interface BrowserProof {
  payload: BrowserProofPayload;
  signature: string;
  signerPublicKey: string;
}

export type ReceiptType =
  | "email.dispatch"
  | "email.delivery"
  | "device.confirmation"
  | "browser.revoked"
  | "device.replaced"
  | "authorization.issued";

export interface SignedReceipt {
  v: 1;
  receiptId: string;
  type: ReceiptType;
  issuedAt: number;
  prevReceiptHash: string | null;
  payload: Record<string, unknown>;
  hash: string;
  signature: string;
}

export interface EmailOpenRecord {
  accessId: string;
  kind: "human" | "scanner" | "prefetch" | "unknown";
  openedAt: number;
  userAgentHash?: string;
}

export interface IssuanceRequest {
  transactionId: string;
  accountId: string;
  challengeId: string;
  tokenId: string;
  browserId: string;
  browserSessionId: string | null;
  browserKeyId: string;
  purpose: string;
  expiresAt: number;
  authorizedAt: number;
  confirmationHash: string;
  proofHash: string;
}

export interface IssuedCredential {
  credentialId: string;
  transactionId: string;
  accountId: string;
  browserId: string;
  browserSessionId: string | null;
  browserKeyId: string;
  purpose: string;
  issuedAt: number;
  expiresAt: number;
  [key: string]: unknown;
}

export type IssuanceLookup =
  | { status: "issued"; credential: IssuedCredential }
  | { status: "pending"; reason?: string }
  | { status: "not_found"; reason?: string }
  | { status: "failed"; reason?: string };

export interface IssuanceService {
  issue(request: IssuanceRequest): Promise<IssuedCredential>;
  lookup(transactionId: string): Promise<IssuanceLookup>;
}

export type ChallengeState =
  | "awaiting_device_confirmation"
  | "awaiting_browser_proof"
  | "issued"
  | "expired"
  | "revoked"
  | "unknown"
  | "failed"
  | "not_found";

export interface ChallengeStatus {
  state: ChallengeState;
  status: ChallengeState;
  reason?: string;
  tokenId?: string;
  challengeId?: string;
  accountId?: string;
  email?: string;
  browserId?: string;
  browserSessionId?: string | null;
  browserKeyId?: string;
  purpose?: string;
  userCode?: string;
  issuedAt?: number;
  expiresAt?: number;
  emailDelivered: boolean;
  deliveryId?: string;
  emailOpenCount: number;
  scannerAccessCount: number;
  deviceConfirmed: boolean;
  browserProofAccepted: boolean;
  transactionId?: string;
  credentialId?: string;
  credential?: IssuedCredential;
  receipt?: SignedReceipt;
  receiptHash?: string;
  dispatchReceipt?: SignedReceipt;
  deliveryReceipt?: SignedReceipt;
  confirmationReceipt?: SignedReceipt;
  browserRevoked?: boolean;
  browserKeyRetired?: boolean;
}

export interface ChallengeView {
  tokenId: string;
  challengeId: string;
  claims: EmailTokenClaims;
  token: EmailToken;
  tokenHash: string;
  dispatchReceipt: SignedReceipt;
  delivery?: {
    receipt: SignedReceipt;
    deliveryId: string;
    providerRef?: string;
    deliveredAt: number;
  };
  opens: EmailOpenRecord[];
  confirmation?: {
    confirmation: DeviceConfirmation;
    payloadHash: string;
    acceptedAt: number;
    receipt: SignedReceipt;
  };
  proof?: {
    proof: BrowserProof;
    payloadHash: string;
    verifiedAt: number;
  };
  issuance?: {
    transactionId: string;
    request: IssuanceRequest;
    requestHash: string;
    requestedAt: number;
    result?: {
      kind: "issued" | "unknown" | "failed";
      credential?: IssuedCredential;
      receipt?: SignedReceipt;
      resolvedAt: number;
      reason?: string;
    };
  };
}

export interface AuthState {
  browsers: Map<string, BrowserRecord>;
  devices: Map<string, DeviceRecord>;
  challenges: Map<string, ChallengeView>;
  tokenToChallenge: Map<string, string>;
  receiptsByHash: Map<string, SignedReceipt>;
  lastReceiptHash: string | null;
  lastEventHash: string | null;
  eventCount: number;
  now: number;
}

export interface JournalRecord {
  v: 1;
  seq: number;
  event: AuthEvent;
  prevHash: string | null;
  hash: string;
  signature: string;
}

export type AuthEvent =
  | {
      type: "browser.registered";
      data: {
        accountId: string;
        browserId: string;
        key: KeyRecord;
        at: number;
      };
    }
  | {
      type: "browser.key_rotated";
      data: {
        accountId: string;
        browserId: string;
        key: KeyRecord;
        retiredKeyId: string;
        at: number;
      };
    }
  | {
      type: "browser.revoked";
      data: {
        accountId: string;
        browserId: string;
        at: number;
        receipt: SignedReceipt;
      };
    }
  | {
      type: "device.registered";
      data: {
        accountId: string;
        deviceId: string;
        keyId: string;
        publicKey: string;
        generation: number;
        at: number;
      };
    }
  | {
      type: "device.replaced";
      data: {
        accountId: string;
        oldDeviceId: string;
        newDeviceId: string;
        keyId: string;
        publicKey: string;
        generation: number;
        at: number;
        receipt: SignedReceipt;
      };
    }
  | {
      type: "device.revoked";
      data: {
        accountId: string;
        deviceId: string;
        at: number;
      };
    }
  | {
      type: "login.started";
      data: {
        claims: EmailTokenClaims;
        token: EmailToken;
        tokenHash: string;
        dispatchReceipt: SignedReceipt;
        at: number;
      };
    }
  | {
      type: "email.delivered";
      data: {
        tokenId: string;
        deliveryId: string;
        deliveredAt: number;
        providerRef?: string;
        receipt: SignedReceipt;
      };
    }
  | {
      type: "email.opened";
      data: {
        tokenId: string;
        access: EmailOpenRecord;
      };
    }
  | {
      type: "device.confirmed";
      data: {
        tokenId: string;
        confirmation: DeviceConfirmation;
        payloadHash: string;
        acceptedAt: number;
        receipt: SignedReceipt;
      };
    }
  | {
      type: "browser.proof_accepted";
      data: {
        tokenId: string;
        proof: BrowserProof;
        payloadHash: string;
        verifiedAt: number;
      };
    }
  | {
      type: "issuance.intent";
      data: {
        tokenId: string;
        request: IssuanceRequest;
        requestHash: string;
        at: number;
      };
    }
  | {
      type: "issuance.result";
      data: {
        tokenId: string;
        transactionId: string;
        kind: "issued" | "unknown" | "failed";
        credential?: IssuedCredential;
        receipt?: SignedReceipt;
        at: number;
        reason?: string;
      };
    };

export interface JournalStore {
  load(): Promise<JournalRecord[]>;
  append(record: JournalRecord): Promise<void>;
}

export interface BeginLoginInput {
  accountId: string;
  email: string;
  browserId: string;
  browserSessionId?: string | null;
  browserKeyId?: string;
  purpose?: string;
  ttlMs?: number;
  userCode?: string;
}

export interface LoginStart {
  token: EmailToken;
  emailToken: string;
  tokenId: string;
  challengeId: string;
  accountId: string;
  email: string;
  browserId: string;
  browserSessionId: string | null;
  browserKeyId: string;
  purpose: string;
  userCode: string;
  expiresAt: number;
  dispatchReceipt: SignedReceipt;
  status: ChallengeStatus;
}

export interface DeliveryResult {
  delivered: boolean;
  duplicate: boolean;
  reason?: string;
  receipt?: SignedReceipt;
  status: ChallengeStatus;
}

export interface OpenResult {
  recorded: boolean;
  consumedAuthorization: false;
  duplicate: boolean;
  reason?: string;
  status: ChallengeStatus;
}

export interface ConfirmationResult {
  accepted: boolean;
  duplicate: boolean;
  reason?: string;
  receipt?: SignedReceipt;
  status: ChallengeStatus;
}

export interface RedeemInput {
  token?: EmailToken | string;
  tokenId?: string;
  proof: BrowserProof;
}

export interface RedeemResult extends ChallengeStatus {
  redeemed: boolean;
  newlyIssued: boolean;
}

export interface AuthorizationAuditEntry {
  transactionId: string;
  receiptHash: string;
  credentialId: string;
  accountId: string;
  browserId: string;
  browserSessionId: string | null;
  browserKeyId: string;
  purpose: string;
  issuedAt: number;
  expiresAt: number;
  browserRevoked: boolean;
  browserKeyRetired: boolean;
  valid: boolean;
}

export interface AuthorizationAudit {
  generatedAt: number;
  accounts: Array<{
    accountId: string;
    browsers: Array<{
      browserId: string;
      activeKeyId: string;
      revoked: boolean;
      revokedAt?: number;
      keys: Array<{ keyId: string; generation: number; active: boolean }>;
    }>;
    devices: Array<{
      deviceId: string;
      keyId: string;
      generation: number;
      active: boolean;
      revokedAt?: number;
    }>;
    authorizations: AuthorizationAuditEntry[];
    pendingChallenges: Array<{ tokenId: string; challengeId: string; state: ChallengeState }>;
  }>;
  authorizedPrincipals: Array<{
    principal: string;
    accountId: string;
    valid: boolean;
    authorizationReceiptHash?: string;
    reason?: string;
  }>;
}

export interface HostOptions {
  store: JournalStore;
  issuance: IssuanceService;
  hostKeyPair?: HostKeyPair;
  clock?: Clock;
  defaultTokenTtlMs?: number;
  maxTokenTtlMs?: number;
}

export interface OfflineAcceptanceResult {
  accepted: boolean;
  reason?: string;
  tokenId?: string;
  challengeId?: string;
  accountId?: string;
  browserId?: string;
  browserSessionId?: string | null;
  browserKeyId?: string;
  purpose?: string;
  transactionId?: string;
  credentialId?: string;
  receiptHash?: string;
}

export class ProtocolError extends Error {
  code: string;
  details?: unknown;

  constructor(code: string, message: string, details?: unknown) {
    super(message);
    this.name = "ProtocolError";
    this.code = code;
    this.details = details;
  }
}
