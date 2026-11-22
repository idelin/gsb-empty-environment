import type {
  ConsentScope,
  IdentityId,
  Invitation,
  InvitationDigest,
  SignedFact,
  GroupId,
  SessionId,
  Generation,
  NodeId,
  MigrationId,
  AnswerId,
} from "./types.ts";
import type { IdentityKeyPair, SignatureService } from "./crypto.ts";
import { invitationDigest } from "./canonical.ts";

export interface InvitationInput {
  readonly sessionId: SessionId;
  readonly initiator: IdentityId;
  readonly receiver: IdentityId;
  readonly group: GroupId;
  readonly initiatorGeneration: Generation;
  readonly nonce: string;
  readonly createdAt: number;
}

export function buildInvitation(input: InvitationInput): Invitation {
  return { ...input };
}

export function digestOf(invitation: Invitation): InvitationDigest {
  return invitationDigest(invitation);
}

export interface ConsentInput {
  readonly invitation: Invitation;
  readonly party: IdentityId;
  readonly generationAtConsent: Generation;
  readonly actions: readonly ("relay-answer" | "migrate-connection")[];
  readonly notBefore: number;
  readonly expiresAt: number;
}

export function buildConsentScope(input: ConsentInput): ConsentScope {
  const { invitation } = input;
  const counterparty =
    invitation.initiator === input.party ? invitation.receiver : invitation.initiator;
  return {
    sessionId: invitation.sessionId,
    party: input.party,
    counterparty,
    group: invitation.group,
    invitationDigest: invitationDigest(invitation),
    generationAtConsent: input.generationAtConsent,
    actions: input.actions,
    notBefore: input.notBefore,
    expiresAt: input.expiresAt,
  };
}

export function signInvitation(
  signatures: SignatureService,
  pair: IdentityKeyPair,
  invitation: Invitation,
  signedAt: number,
): SignedFact<Invitation> {
  return signatures.signFact(pair, "invitation", invitation, { signedAt });
}

export function signConsent(
  signatures: SignatureService,
  pair: IdentityKeyPair,
  scope: ConsentScope,
  signedAt: number,
): SignedFact<ConsentScope> {
  return signatures.signFact(pair, "consent", scope, { signedAt });
}

export function signRevocation(
  signatures: SignatureService,
  pair: IdentityKeyPair,
  sessionId: SessionId,
  digest: InvitationDigest,
  reason: string,
  signedAt: number,
) {
  return signatures.signFact(
    pair,
    "invitation-revocation",
    { sessionId, invitationDigest: digest, reason, revokedAt: signedAt },
    { signedAt },
  );
}

export function signAnswer(
  signatures: SignatureService,
  pair: IdentityKeyPair,
  answer: {
    answerId: AnswerId;
    sessionId: SessionId;
    invitationDigest: InvitationDigest;
    group: GroupId;
    from: IdentityId;
    to: IdentityId;
    body: string;
    createdAt: number;
  },
  signedAt: number,
) {
  return signatures.signFact(pair, "answer", answer, { signedAt });
}

export function signMigration(
  signatures: SignatureService,
  pair: IdentityKeyPair,
  kind: "migration-release" | "migration-takeover" | "migration-approval",
  payload:
    | {
        migrationId: MigrationId;
        sessionId: SessionId;
        party: IdentityId;
        oldGeneration: Generation;
        newGeneration: Generation;
        fromNode: NodeId;
      }
    | {
        migrationId: MigrationId;
        sessionId: SessionId;
        party: IdentityId;
        newGeneration: Generation;
        toNode: NodeId;
      }
    | {
        migrationId: MigrationId;
        sessionId: SessionId;
        migratingParty: IdentityId;
        approvingParty: IdentityId;
        newGeneration: Generation;
      },
  signedAt: number,
) {
  return signatures.signFact(pair, kind, payload, { signedAt });
}
