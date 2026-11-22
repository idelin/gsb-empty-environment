import {
  CasReceiptDirectory,
  Ed25519SignatureService,
  InMemoryCasStore,
  SessionCoordinator,
  classifyRecovery,
  buildConsentScope,
  buildInvitation,
  digestOf,
  initialSessionState,
  applyEvent,
} from "../index.ts";
import type {
  CasStore,
  IdentityKeyPair,
  Invitation,
  ReceiptDirectory,
  SessionEvent,
  SessionState,
  SignatureService,
  SignedFact,
  VerdictNotifier,
  DeliveryVerdict,
  SessionId,
} from "../index.ts";

export interface ScenarioCheck {
  readonly name: string;
  readonly pass: boolean;
  readonly detail: string;
}

export interface ScenarioReport {
  readonly scenario: string;
  readonly checks: readonly ScenarioCheck[];
  readonly events: readonly SessionEvent[];
}

export class RecordingNotifier implements VerdictNotifier {
  readonly calls: DeliveryVerdict[] = [];
  onVerdict(verdict: DeliveryVerdict): void {
    this.calls.push(verdict);
  }
}

/** A two-node cluster reference: node A accepts, node B replays the same log. */
export class World {
  readonly sig: SignatureService = new Ed25519SignatureService();
  readonly alice: IdentityKeyPair = this.sig.generateIdentity();
  readonly bob: IdentityKeyPair = this.sig.generateIdentity();
  readonly carol: IdentityKeyPair = this.sig.generateIdentity();
  readonly storeA: CasStore = new InMemoryCasStore();
  readonly storeB: CasStore = new InMemoryCasStore();
  readonly receiptStore: CasStore = new InMemoryCasStore();
  readonly receipts: ReceiptDirectory = new CasReceiptDirectory(this.receiptStore);
  readonly notifierA = new RecordingNotifier();
  readonly notifierB = new RecordingNotifier();
  now = 1_000_000;

  readonly nodeA = new SessionCoordinator({
    nodeId: "node-A",
    store: this.storeA,
    signatures: this.sig,
    receipts: this.receipts,
    notifier: this.notifierA,
    clock: () => this.now,
  });

  readonly nodeB = new SessionCoordinator({
    nodeId: "node-B",
    store: this.storeB,
    signatures: this.sig,
    receipts: new CasReceiptDirectory(new InMemoryCasStore()),
    notifier: this.notifierB,
    clock: () => this.now,
  });

  tick(step = 1): number {
    this.now += step;
    return this.now;
  }

  async submit(sessionId: SessionId, fact: SignedFact) {
    return this.nodeA.submitFact(sessionId, fact);
  }

  async events(sessionId: SessionId): Promise<SessionEvent[]> {
    const record = await this.storeA.get<{ events: SessionEvent[] }>(`session:${sessionId}`);
    return record ? record.value.events : [];
  }

  async replayOnB(sessionId: SessionId): Promise<SessionState> {
    let state = initialSessionState();
    for (const event of await this.events(sessionId)) {
      state = applyEvent(state, event);
    }
    return state;
  }

  /** Recovery lookup against a directory with no receipts: simulates total receipt loss. */
  async recoverWithEmptyDirectory(sessionId: SessionId, answerId: string) {
    const state = await this.nodeA.getState(sessionId);
    return classifyRecovery(state, answerId, async () => null);
  }

  check(name: string, pass: boolean, detail: string): ScenarioCheck {
    return { name, pass, detail };
  }

  invitation(
    sessionId: string,
    group: string,
    initiator: IdentityKeyPair,
    receiver: IdentityKeyPair,
  ): Invitation {
    return buildInvitation({
      sessionId,
      initiator: initiator.identity,
      receiver: receiver.identity,
      group,
      initiatorGeneration: 1,
      nonce: `nonce-${sessionId}`,
      createdAt: this.now,
    });
  }

  consent(invitation: Invitation, party: IdentityKeyPair): SignedFact {
    const scope = buildConsentScope({
      invitation,
      party: party.identity,
      generationAtConsent: 1,
      actions: ["relay-answer", "migrate-connection"],
      notBefore: this.now - 60_000,
      expiresAt: this.now + 600_000,
    });
    return this.sig.signFact(party, "consent", scope, { signedAt: this.tick() });
  }

  digest(invitation: Invitation): string {
    return digestOf(invitation);
  }

  migration(
    invitation: Invitation,
    migrating: IdentityKeyPair,
    peer: IdentityKeyPair,
    oldGeneration: number,
    newGeneration: number,
    fromNode: string,
    toNode: string,
    migrationId: string,
  ) {
    const base = { migrationId, sessionId: invitation.sessionId };
    const release = this.sig.signFact(
      migrating,
      "migration-release",
      { ...base, party: migrating.identity, oldGeneration, newGeneration, fromNode },
      { signedAt: this.tick() },
    );
    const takeover = this.sig.signFact(
      migrating,
      "migration-takeover",
      { ...base, party: migrating.identity, newGeneration, toNode },
      { signedAt: this.tick() },
    );
    const approval = this.sig.signFact(
      peer,
      "migration-approval",
      {
        ...base,
        migratingParty: migrating.identity,
        approvingParty: peer.identity,
        newGeneration,
      },
      { signedAt: this.tick() },
    );
    return { migrationId, release, takeover, approval };
  }
}
