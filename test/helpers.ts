import {
  Ed25519SignatureService,
  InMemoryCasStore,
  CasReceiptDirectory,
  SessionCoordinator,
  buildInvitation,
  buildConsentScope,
  signInvitation,
  signConsent,
  signRevocation,
  signAnswer,
  signMigration,
  digestOf,
  replay,
} from "../src/index.ts";
import type {
  AnswerPayload,
  IdentityKeyPair,
  Invitation,
  SessionEvent,
  SignedFact,
  VerdictNotifier,
  DeliveryVerdict,
} from "../src/index.ts";

export interface Harness {
  sig: Ed25519SignatureService;
  alice: IdentityKeyPair;
  bob: IdentityKeyPair;
  carol: IdentityKeyPair;
  clock: { now: number };
  nodeA: SessionCoordinator;
  nodeB: SessionCoordinator;
  store: InMemoryCasStore;
  storeB: InMemoryCasStore;
  receipts: CasReceiptDirectory;
  notifierA: RecordingNotifier;
  notifierB: RecordingNotifier;
}

export class RecordingNotifier implements VerdictNotifier {
  readonly calls: DeliveryVerdict[] = [];
  onVerdict(verdict: DeliveryVerdict): void {
    this.calls.push(verdict);
  }
}

export function makeHarness(startTime = 1_000_000): Harness {
  const sig = new Ed25519SignatureService();
  const alice = sig.generateIdentity();
  const bob = sig.generateIdentity();
  const carol = sig.generateIdentity();
  const clock = { now: startTime };
  const store = new InMemoryCasStore();
  const storeB = new InMemoryCasStore();
  const receipts = new CasReceiptDirectory(store);
  const notifierA = new RecordingNotifier();
  const notifierB = new RecordingNotifier();
  const nodeA = new SessionCoordinator({
    nodeId: "node-A",
    store,
    signatures: sig,
    receipts,
    notifier: notifierA,
    clock: () => clock.now,
  });
  const nodeB = new SessionCoordinator({
    nodeId: "node-B",
    store: storeB,
    signatures: sig,
    receipts: new CasReceiptDirectory(storeB),
    notifier: notifierB,
    clock: () => clock.now,
  });
  return { sig, alice, bob, carol, clock, nodeA, nodeB, store, storeB, receipts, notifierA, notifierB };
}

export function invite(
  h: Harness,
  sessionId: string,
  group: string,
  from: IdentityKeyPair = h.alice,
  to: IdentityKeyPair = h.bob,
  generation = 1,
  nonce = `nonce-${sessionId}`,
): Invitation {
  const invitation = buildInvitation({
    sessionId,
    initiator: from.identity,
    receiver: to.identity,
    group,
    initiatorGeneration: generation,
    nonce,
    createdAt: h.clock.now,
  });
  return invitation;
}

export function signedInvite(h: Harness, invitation: Invitation): SignedFact<Invitation> {
  return signInvitation(h.sig, h.alice, invitation, h.clock.now);
}

export function consent(
  h: Harness,
  invitation: Invitation,
  party: IdentityKeyPair,
  actions: readonly ("relay-answer" | "migrate-connection")[] = ["relay-answer", "migrate-connection"],
) {
  const scope = buildConsentScope({
    invitation,
    party: party.identity,
    generationAtConsent: 1,
    actions,
    notBefore: h.clock.now - 60_000,
    expiresAt: h.clock.now + 600_000,
  });
  return signConsent(h.sig, party, scope, h.clock.now);
}

export function revoke(h: Harness, invitation: Invitation, initiator: IdentityKeyPair) {
  return signRevocation(
    h.sig,
    initiator,
    invitation.sessionId,
    digestOf(invitation),
    "cancelled",
    h.clock.now,
  );
}

export function answer(
  h: Harness,
  invitation: Invitation,
  answerId: string,
  from: IdentityKeyPair,
  to: IdentityKeyPair,
  body = "payload",
): SignedFact<AnswerPayload> {
  return signAnswer(
    h.sig,
    from,
    {
      answerId,
      sessionId: invitation.sessionId,
      invitationDigest: digestOf(invitation),
      group: invitation.group,
      from: from.identity,
      to: to.identity,
      body,
      createdAt: h.clock.now,
    },
    h.clock.now,
  );
}

export interface MigrationFacts {
  migrationId: string;
  release: SignedFact;
  takeover: SignedFact;
  approval: SignedFact;
}

export function migration(
  h: Harness,
  invitation: Invitation,
  migrating: IdentityKeyPair,
  peer: IdentityKeyPair,
  oldGeneration: number,
  newGeneration: number,
  fromNode: string,
  toNode: string,
  migrationId = `mig-${invitation.sessionId}-${newGeneration}`,
): MigrationFacts {
  const base = { migrationId, sessionId: invitation.sessionId };
  const release = signMigration(
    h.sig,
    migrating,
    "migration-release",
    { ...base, party: migrating.identity, oldGeneration, newGeneration, fromNode },
    h.clock.now,
  );
  const takeover = signMigration(
    h.sig,
    migrating,
    "migration-takeover",
    { ...base, party: migrating.identity, newGeneration, toNode },
    h.clock.now,
  );
  const approval = signMigration(
    h.sig,
    peer,
    "migration-approval",
    {
      ...base,
      migratingParty: migrating.identity,
      approvingParty: peer.identity,
      newGeneration,
    },
    h.clock.now,
  );
  return { migrationId, release, takeover, approval };
}

export async function collectEvents(h: Harness, sessionId: string): Promise<SessionEvent[]> {
  const record = await h.store.get<{ events: SessionEvent[] }>(`session:${sessionId}`);
  return record ? record.value.events : [];
}

export function replayAll(events: readonly SessionEvent[]) {
  return replay(events);
}

export function permutations<T>(items: readonly T[]): T[][] {
  if (items.length <= 1) {
    return [items.slice()];
  }
  const result: T[][] = [];
  items.forEach((item, index) => {
    const rest = [...items.slice(0, index), ...items.slice(index + 1)];
    for (const tail of permutations(rest)) {
      result.push([item, ...tail]);
    }
  });
  return result;
}
