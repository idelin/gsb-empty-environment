/**
 * Core domain types for the relay session coordination package.
 *
 * Identity model: an {@link IdentityId} is the fingerprint of a signing key.
 * Display names and transport addresses are never used as identity inputs:
 * reusing either one never inherits consent.
 */

export type IdentityId = string;
export type SessionId = string;
export type NodeId = string;
export type GroupId = string;
export type EventId = string;
export type FactId = string;
export type MigrationId = string;
export type AnswerId = string;
export type InvitationDigest = string;
export type Generation = number;
export type TransportAddress = string;

/** Actions that a party may grant inside a signed consent scope. */
export type ConsentAction = "relay-answer" | "migrate-connection";

export interface Invitation {
  readonly sessionId: SessionId;
  readonly initiator: IdentityId;
  readonly receiver: IdentityId;
  readonly group: GroupId;
  readonly initiatorGeneration: Generation;
  readonly nonce: string;
  readonly createdAt: number;
}

/**
 * The exact scope a party signs. Consent is bound to the session id, both
 * identities, the content group and the original invitation digest, so it
 * cannot be replayed under a reused display name, address or session id.
 */
export interface ConsentScope {
  readonly sessionId: SessionId;
  readonly party: IdentityId;
  readonly counterparty: IdentityId;
  readonly group: GroupId;
  readonly invitationDigest: InvitationDigest;
  readonly generationAtConsent: Generation;
  readonly actions: readonly ConsentAction[];
  readonly notBefore: number;
  readonly expiresAt: number;
}

export interface InvitationRevocation {
  readonly sessionId: SessionId;
  readonly invitationDigest: InvitationDigest;
  readonly reason: string;
  readonly revokedAt: number;
}

/** A relayed answer is always bound to the digest of the original invitation. */
export interface AnswerPayload {
  readonly answerId: AnswerId;
  readonly sessionId: SessionId;
  readonly invitationDigest: InvitationDigest;
  readonly group: GroupId;
  readonly from: IdentityId;
  readonly to: IdentityId;
  readonly body: string;
  readonly createdAt: number;
}

export interface MigrationRelease {
  readonly migrationId: MigrationId;
  readonly sessionId: SessionId;
  readonly party: IdentityId;
  readonly oldGeneration: Generation;
  readonly newGeneration: Generation;
  readonly fromNode: NodeId;
}

export interface MigrationTakeover {
  readonly migrationId: MigrationId;
  readonly sessionId: SessionId;
  readonly party: IdentityId;
  readonly newGeneration: Generation;
  readonly toNode: NodeId;
}

export interface MigrationApproval {
  readonly migrationId: MigrationId;
  readonly sessionId: SessionId;
  readonly migratingParty: IdentityId;
  readonly approvingParty: IdentityId;
  readonly newGeneration: Generation;
}

export type FactKind =
  | "invitation"
  | "consent"
  | "invitation-revocation"
  | "answer"
  | "migration-release"
  | "migration-takeover"
  | "migration-approval";

export type FactPayload =
  | Invitation
  | ConsentScope
  | InvitationRevocation
  | AnswerPayload
  | MigrationRelease
  | MigrationTakeover
  | MigrationApproval;

/** Envelope for every signed statement. The signature covers the full envelope. */
export interface SignedFact<F extends FactPayload = FactPayload> {
  readonly factId: FactId;
  readonly kind: FactKind;
  readonly payload: F;
  readonly signer: IdentityId;
  readonly signedAt: number;
  readonly signature: string;
}

export type SessionEvent =
  | {
      readonly eventId: EventId;
      readonly kind: "fact-accepted";
      readonly at: number;
      readonly node: NodeId;
      readonly fact: SignedFact;
    }
  | {
      readonly eventId: EventId;
      readonly kind: "connection-registered";
      readonly at: number;
      readonly node: NodeId;
      readonly identity: IdentityId;
      readonly displayName: string;
      readonly address: TransportAddress;
      readonly generation: Generation;
    }
  | {
      readonly eventId: EventId;
      readonly kind: "connection-released";
      readonly at: number;
      readonly node: NodeId;
      readonly identity: IdentityId;
      readonly generation: Generation;
    }
  | {
      readonly eventId: EventId;
      readonly kind: "delivery-attempt";
      readonly at: number;
      readonly answerId: AnswerId;
    }
  | {
      readonly eventId: EventId;
      readonly kind: "delivery-confirmed";
      readonly at: number;
      readonly answerId: AnswerId;
      readonly receiptId: string;
    };

export type SessionStatus = "forming" | "revoked";

export interface PartyRuntime {
  readonly identity: IdentityId;
  readonly displayName: string;
  readonly address: TransportAddress;
  readonly generation: Generation;
  readonly node: NodeId | null;
  readonly connected: boolean;
}

export interface MigrationTracker {
  readonly migrationId: MigrationId;
  readonly party: IdentityId;
  readonly release: SignedFact<MigrationRelease> | null;
  readonly takeover: SignedFact<MigrationTakeover> | null;
  readonly approval: SignedFact<MigrationApproval> | null;
  readonly completed: boolean;
}

export type DeliveryState = "queued" | "in-flight" | "delivered";

export interface DeliveryAttempt {
  readonly status: DeliveryState;
  readonly attempts: number;
  readonly receiptId: string | null;
  readonly updatedAt: number;
}

export interface SessionState {
  readonly sessionId: SessionId | null;
  readonly status: SessionStatus;
  readonly invitation: Invitation | null;
  readonly invitationDigest: InvitationDigest | null;
  readonly parties: Readonly<Record<IdentityId, PartyRuntime>>;
  readonly consents: Readonly<Record<IdentityId, ConsentScope>>;
  readonly answers: Readonly<Record<AnswerId, AnswerPayload>>;
  readonly migrations: Readonly<Record<MigrationId, MigrationTracker>>;
  readonly deliveries: Readonly<Record<AnswerId, DeliveryAttempt>>;
  readonly appliedFactIds: Readonly<Record<FactId, true>>;
}

export type MissingFact =
  | { readonly kind: "invitation" }
  | { readonly kind: "consent"; readonly party: IdentityId }
  | { readonly kind: "connection"; readonly party: IdentityId }
  | {
      readonly kind: "migration-release" | "migration-takeover" | "migration-approval";
      readonly migrationId: MigrationId;
    };

export type BlockReason =
  | "session-unknown"
  | "answer-unknown"
  | "invitation-revoked"
  | "invitation-digest-mismatch"
  | "group-mismatch"
  | "unknown-party"
  | "scope-action-denied"
  | "no-live-connection"
  | "session-id-conflict"
  | "consent-expired"
  | "bad-signature"
  | "wrong-signer"
  | "stale-generation"
  | "migration-already-pending"
  | "malformed-fact";

export type DeliveryVerdict =
  | {
      readonly status: "deliverable";
      readonly answerId: AnswerId;
      readonly to: IdentityId;
      readonly generation: Generation;
      readonly node: NodeId;
    }
  | {
      readonly status: "waiting";
      readonly answerId: AnswerId;
      readonly missing: readonly MissingFact[];
    }
  | { readonly status: "blocked"; readonly answerId: AnswerId; readonly reason: BlockReason; readonly detail?: string }
  | { readonly status: "in-flight"; readonly answerId: AnswerId; readonly attempts: number }
  | { readonly status: "delivered"; readonly answerId: AnswerId; readonly receiptId: string };

export type FactAcceptance =
  | { readonly ok: true }
  | { readonly ok: false; readonly reason: BlockReason; readonly detail?: string };

export type FactAcceptResult =
  | { readonly ok: true; readonly event: SessionEvent }
  | { readonly ok: false; readonly reason: BlockReason; readonly detail?: string };

export type RecoveryClassification =
  | { readonly kind: "delivered"; readonly answerId: AnswerId; readonly receiptId: string }
  | { readonly kind: "delivered-reconciled"; readonly answerId: AnswerId; readonly receiptId: string }
  | { readonly kind: "indeterminate"; readonly answerId: AnswerId; readonly detail: string }
  | { readonly kind: "not-attempted"; readonly answerId: AnswerId };
