import type {
  AnswerId,
  AnswerPayload,
  ConsentScope,
  DeliveryAttempt,
  DeliveryVerdict,
  FactAcceptance,
  IdentityId,
  Invitation,
  MigrationApproval,
  MigrationId,
  MigrationRelease,
  MigrationTakeover,
  NodeId,
  RecoveryClassification,
  SessionEvent,
  SessionState,
  SignedFact,
  TransportAddress,
  BlockReason,
  FactAcceptResult,
} from "./types.ts";
import { invitationDigest } from "./canonical.ts";
import { mergeMigrationFact, migratingPartyOf, missingMigrationFacts } from "./migration.ts";

export function initialSessionState(): SessionState {
  return {
    sessionId: null,
    status: "forming",
    invitation: null,
    invitationDigest: null,
    parties: {},
    consents: {},
    answers: {},
    migrations: {},
    deliveries: {},
    appliedFactIds: {},
  };
}

/**
 * Rebuild state from a full event log. Two nodes replaying the same events
 * reach identical logical state, which is what makes takeover safe.
 */
export function replay(events: readonly SessionEvent[]): SessionState {
  return events.reduce((state, event) => applyEvent(state, event), initialSessionState());
}

export function applyEvent(state: SessionState, event: SessionEvent): SessionState {
  switch (event.kind) {
    case "fact-accepted":
      return applyAcceptedFact(state, event.fact);
    case "connection-registered":
      return applyConnection(
        state,
        event.identity,
        event.displayName,
        event.address,
        event.generation,
        event.node,
      );
    case "connection-released":
      return releaseConnection(state, event.identity, event.generation);
    case "delivery-attempt":
      return updateDelivery(state, event.answerId, (current) => ({
        status: "in-flight",
        attempts: current.attempts + 1,
        receiptId: current.receiptId,
        updatedAt: event.at,
      }));
    case "delivery-confirmed":
      return updateDelivery(state, event.answerId, (current) => ({
        ...current,
        status: "delivered",
        receiptId: event.receiptId,
        updatedAt: event.at,
      }));
  }
}

/**
 * Validate a signed fact against current state. Signature verification is
 * handled by the coordinator; this pure function checks domain rules.
 */
export function acceptFact(
  state: SessionState,
  fact: SignedFact,
  now: number,
  node: NodeId,
): FactAcceptResult {
  if (state.appliedFactIds[fact.factId]) {
    return rejected("malformed-fact", "duplicate fact id");
  }
  const decision = validateFact(state, fact, now);
  if (!decision.ok) {
    return decision;
  }
  return {
    ok: true,
    event: {
      eventId: deriveEventId(fact),
      kind: "fact-accepted",
      at: fact.signedAt,
      node,
      fact,
    },
  };
}

function validateFact(state: SessionState, fact: SignedFact, now: number): FactAcceptance {
  switch (fact.kind) {
    case "invitation":
      return validateInvitation(state, fact as SignedFact<Invitation>);
    case "consent":
      return validateConsent(state, fact as SignedFact<ConsentScope>, now);
    case "invitation-revocation":
      return validateRevocation(state, fact);
    case "answer":
      return validateAnswer(state, fact as SignedFact<AnswerPayload>, now);
    case "migration-release":
    case "migration-takeover":
    case "migration-approval":
      return validateMigration(state, fact, now);
  }
}

function validateInvitation(state: SessionState, fact: SignedFact<Invitation>): FactAcceptance {
  const payload = fact.payload;
  if (state.invitation) {
    return rejected("session-id-conflict", "session log already bound to an invitation");
  }
  if (fact.signer !== payload.initiator) {
    return rejected("wrong-signer", "only the initiator may invite");
  }
  if (payload.initiator === payload.receiver) {
    return rejected("malformed-fact", "initiator and receiver must differ");
  }
  if (payload.initiatorGeneration <= 0) {
    return rejected("stale-generation", "generation must be positive");
  }
  return accepted();
}

function validateConsent(
  state: SessionState,
  fact: SignedFact<ConsentScope>,
  now: number,
): FactAcceptance {
  const scope = fact.payload;
  if (!state.invitation || !state.invitationDigest) {
    return rejected("session-unknown", "consent before invitation");
  }
  if (state.status === "revoked") {
    return rejected("invitation-revoked", "late consent after revocation");
  }
  if (fact.signer !== scope.party) {
    return rejected("wrong-signer", "a party signs only its own consent scope");
  }
  if (scope.sessionId !== state.invitation.sessionId) {
    return rejected("session-id-conflict", "consent bound to another session id");
  }
  if (scope.invitationDigest !== state.invitationDigest) {
    return rejected("invitation-digest-mismatch", "consent not bound to original invitation");
  }
  if (scope.group !== state.invitation.group) {
    return rejected("group-mismatch", "consent covers a different content group");
  }
  if (![state.invitation.initiator, state.invitation.receiver].includes(scope.party)) {
    return rejected("unknown-party", "signer is not a session party");
  }
  if (scope.counterparty !== otherParty(state, scope.party)) {
    return rejected("unknown-party", "counterparty is not the other session party");
  }
  if (now < scope.notBefore || now >= scope.expiresAt) {
    return rejected("consent-expired", `valid window [${scope.notBefore}, ${scope.expiresAt})`);
  }
  if (scope.actions.length === 0) {
    return rejected("scope-action-denied", "empty consent scope");
  }
  if (state.consents[scope.party]) {
    return rejected("malformed-fact", "consent already recorded for party");
  }
  return accepted();
}

function validateRevocation(state: SessionState, fact: SignedFact): FactAcceptance {
  if (!state.invitation || !state.invitationDigest) {
    return rejected("session-unknown", "revocation before invitation");
  }
  if (fact.signer !== state.invitation.initiator) {
    return rejected("wrong-signer", "only the initiator may revoke the invitation");
  }
  if (fact.payload.invitationDigest !== state.invitationDigest) {
    return rejected("invitation-digest-mismatch", "revocation not bound to original invitation");
  }
  if (state.status === "revoked") {
    return rejected("malformed-fact", "invitation already revoked");
  }
  return accepted();
}

function validateAnswer(
  state: SessionState,
  fact: SignedFact<AnswerPayload>,
  now: number,
): FactAcceptance {
  const answer = fact.payload;
  if (!state.invitation || !state.invitationDigest) {
    return rejected("session-unknown", "answer before invitation");
  }
  if (state.status === "revoked") {
    return rejected("invitation-revoked", "late answer after revocation");
  }
  if (answer.sessionId !== state.invitation.sessionId) {
    return rejected("session-id-conflict", "answer bound to another session id");
  }
  if (answer.invitationDigest !== state.invitationDigest) {
    return rejected("invitation-digest-mismatch", "answer not bound to original invitation");
  }
  if (answer.group !== state.invitation.group) {
    return rejected("group-mismatch", "answer belongs to another content group");
  }
  if (fact.signer !== answer.from) {
    return rejected("wrong-signer", "answer must be signed by its sender");
  }
  if (![state.invitation.initiator, state.invitation.receiver].includes(answer.from)) {
    return rejected("unknown-party", "sender is not a session party");
  }
  if (answer.to !== otherParty(state, answer.from)) {
    return rejected("unknown-party", "recipient is not the other session party");
  }
  const consent = state.consents[answer.from];
  if (!consent) {
    return rejected("scope-action-denied", "sender has not signed a consent scope");
  }
  if (!consent.actions.includes("relay-answer")) {
    return rejected("scope-action-denied", "scope does not allow relay-answer");
  }
  if (now < consent.notBefore || now >= consent.expiresAt) {
    return rejected("consent-expired", "sender consent is no longer valid");
  }
  if (state.answers[answer.answerId]) {
    return rejected("malformed-fact", "duplicate answer id");
  }
  return accepted();
}

function validateMigration(state: SessionState, fact: SignedFact, now: number): FactAcceptance {
  if (!state.invitation) {
    return rejected("session-unknown", "migration before invitation");
  }
  if (state.status === "revoked") {
    return rejected("invitation-revoked", "migration after revocation");
  }
  const party = migratingPartyOf(
    fact as SignedFact<MigrationRelease | MigrationTakeover | MigrationApproval>,
  );
  if (![state.invitation.initiator, state.invitation.receiver].includes(party)) {
    return rejected("unknown-party", "migration subject is not a session party");
  }
  if (fact.kind === "migration-approval") {
    const approval = fact.payload as MigrationApproval;
    if (fact.signer !== approval.approvingParty) {
      return rejected("wrong-signer", "approval must be signed by the peer");
    }
    if (approval.approvingParty !== otherParty(state, approval.migratingParty)) {
      return rejected("unknown-party", "only the counterparty may approve");
    }
  } else if (fact.signer !== party) {
    return rejected("wrong-signer", "release and takeover must be self-signed");
  }
  const consent = state.consents[party];
  if (!consent || !consent.actions.includes("migrate-connection")) {
    return rejected("scope-action-denied", "party scope does not allow migrate-connection");
  }
  if (now < consent.notBefore || now >= consent.expiresAt) {
    return rejected("consent-expired", "consent window closed for migration");
  }

  const runtime = state.parties[party];
  if (fact.kind === "migration-release") {
    const release = fact.payload as MigrationRelease;
    if (!runtime || runtime.generation !== release.oldGeneration) {
      return rejected("stale-generation", "release does not match current connection generation");
    }
  }
  if (fact.kind === "migration-takeover") {
    const takeover = fact.payload as MigrationTakeover;
    const tracker = state.migrations[takeover.migrationId];
    if (!tracker?.release && (!runtime || runtime.generation >= takeover.newGeneration)) {
      return rejected("stale-generation", "takeover generation must advance the connection");
    }
  }

  const merge = mergeMigrationFact(
    state,
    fact as SignedFact<MigrationRelease | MigrationTakeover | MigrationApproval>,
  );
  if (!merge.ok) {
    return rejected(merge.reason, merge.detail);
  }
  return accepted();
}

function applyAcceptedFact(state: SessionState, fact: SignedFact): SessionState {
  const appliedFactIds = { ...state.appliedFactIds, [fact.factId]: true as const };
  switch (fact.kind) {
    case "invitation": {
      const invitation = fact.payload as Invitation;
      return {
        ...state,
        sessionId: invitation.sessionId,
        invitation,
        invitationDigest: invitationDigest(invitation),
        appliedFactIds,
      };
    }
    case "consent": {
      const scope = fact.payload as ConsentScope;
      return { ...state, consents: { ...state.consents, [scope.party]: scope }, appliedFactIds };
    }
    case "invitation-revocation":
      return { ...state, status: "revoked", appliedFactIds };
    case "answer": {
      const answer = fact.payload as AnswerPayload;
      const delivery: DeliveryAttempt = {
        status: "queued",
        attempts: 0,
        receiptId: null,
        updatedAt: fact.signedAt,
      };
      return {
        ...state,
        answers: { ...state.answers, [answer.answerId]: answer },
        deliveries: { ...state.deliveries, [answer.answerId]: delivery },
        appliedFactIds,
      };
    }
    case "migration-release":
    case "migration-takeover":
    case "migration-approval":
      return applyMigrationFact(state, fact, appliedFactIds);
  }
}

function applyMigrationFact(
  state: SessionState,
  fact: SignedFact,
  appliedFactIds: Record<string, true>,
): SessionState {
  const migrationId = fact.payload.migrationId as MigrationId;
  const party = migratingPartyOf(
    fact as SignedFact<MigrationRelease | MigrationTakeover | MigrationApproval>,
  );
  const previous = state.migrations[migrationId] ?? {
    migrationId,
    party,
    release: null,
    takeover: null,
    approval: null,
    completed: false,
  };
  const tracker = {
    migrationId,
    party,
    release:
      fact.kind === "migration-release" ? (fact as SignedFact<MigrationRelease>) : previous.release,
    takeover:
      fact.kind === "migration-takeover" ? (fact as SignedFact<MigrationTakeover>) : previous.takeover,
    approval:
      fact.kind === "migration-approval" ? (fact as SignedFact<MigrationApproval>) : previous.approval,
    completed: previous.completed,
  };

  if (!(tracker.release && tracker.takeover && tracker.approval)) {
    return {
      ...state,
      migrations: { ...state.migrations, [migrationId]: tracker },
      appliedFactIds,
    };
  }

  const runtime = state.parties[party];
  const newGeneration = tracker.release.payload.newGeneration;
  const parties = {
    ...state.parties,
    [party]: {
      identity: party,
      displayName: runtime?.displayName ?? "",
      address: runtime?.address ?? "",
      generation: newGeneration,
      node: tracker.takeover.payload.toNode,
      connected: true,
    },
  };
  return {
    ...state,
    parties,
    migrations: { ...state.migrations, [migrationId]: { ...tracker, completed: true } },
    appliedFactIds,
  };
}

function applyConnection(
  state: SessionState,
  identity: IdentityId,
  displayName: string,
  address: TransportAddress,
  generation: number,
  node: NodeId,
): SessionState {
  const previous = state.parties[identity];
  if (previous && generation < previous.generation) {
    return state;
  }
  return {
    ...state,
    parties: {
      ...state.parties,
      [identity]: { identity, displayName, address, generation, node, connected: true },
    },
  };
}

function releaseConnection(
  state: SessionState,
  identity: IdentityId,
  generation: number,
): SessionState {
  const previous = state.parties[identity];
  if (!previous || previous.generation !== generation) {
    return state;
  }
  return {
    ...state,
    parties: {
      ...state.parties,
      [identity]: { ...previous, connected: false, node: null },
    },
  };
}

function updateDelivery(
  state: SessionState,
  answerId: AnswerId,
  mutate: (current: DeliveryAttempt) => DeliveryAttempt,
): SessionState {
  const current = state.deliveries[answerId];
  if (!current) {
    return state;
  }
  return { ...state, deliveries: { ...state.deliveries, [answerId]: mutate(current) } };
}

/**
 * Current routing decision for one answer over a partial event log.
 * Returns the deliverable target, a blocked reason, or the exact facts that
 * are still missing. Display names and addresses never influence the result.
 */
export function evaluateAnswer(state: SessionState, answerId: AnswerId): DeliveryVerdict {
  const answer = state.answers[answerId];
  if (!answer) {
    return { status: "blocked", answerId, reason: "answer-unknown" };
  }
  const delivery = state.deliveries[answerId];
  if (delivery?.status === "delivered" && delivery.receiptId) {
    return { status: "delivered", answerId, receiptId: delivery.receiptId };
  }
  if (delivery?.status === "in-flight") {
    return { status: "in-flight", answerId, attempts: delivery.attempts };
  }

  if (!state.invitation || !state.invitationDigest) {
    return { status: "waiting", answerId, missing: [{ kind: "invitation" }] };
  }
  if (state.status === "revoked") {
    return { status: "blocked", answerId, reason: "invitation-revoked" };
  }
  if (answer.invitationDigest !== state.invitationDigest) {
    return { status: "blocked", answerId, reason: "invitation-digest-mismatch" };
  }
  if (answer.group !== state.invitation.group) {
    return { status: "blocked", answerId, reason: "group-mismatch" };
  }

  const missing = missingConsentFacts(state);
  const pendingMigration = latestPendingMigration(state, answer.to);
  if (pendingMigration) {
    missing.push(...missingMigrationFacts(pendingMigration, pendingMigration.migrationId));
  }
  if (missing.length > 0) {
    return { status: "waiting", answerId, missing };
  }

  const target = state.parties[answer.to];
  if (!target) {
    return {
      status: "waiting",
      answerId,
      missing: [...missing, { kind: "connection" as const, party: answer.to }],
    };
  }
  if (!target.connected || !target.node) {
    return { status: "blocked", answerId, reason: "no-live-connection" };
  }
  return {
    status: "deliverable",
    answerId,
    to: answer.to,
    generation: target.generation,
    node: target.node,
  };
}

function missingConsentFacts(
  state: SessionState,
): Array<{ kind: "consent"; party: IdentityId }> {
  if (!state.invitation) {
    return [];
  }
  const missing: Array<{ kind: "consent"; party: IdentityId }> = [];
  for (const party of [state.invitation.initiator, state.invitation.receiver]) {
    if (!state.consents[party]) {
      missing.push({ kind: "consent", party });
    }
  }
  return missing;
}

function latestPendingMigration(
  state: SessionState,
  party: IdentityId,
): SessionState["migrations"][string] | null {
  let found: SessionState["migrations"][string] | null = null;
  for (const tracker of Object.values(state.migrations)) {
    if (!tracker.completed && tracker.party === party) {
      found = tracker;
    }
  }
  return found;
}

/**
 * Recovery classification. A persisted "delivered" with a queryable receiver
 * receipt is certain; an in-flight send whose receipt cannot be found has no
 * determined outcome yet and must be retried rather than redirected.
 */
export async function classifyRecovery(
  state: SessionState,
  answerId: AnswerId,
  lookupReceipt: (answerId: AnswerId) => Promise<{ receiptId: string } | null>,
): Promise<RecoveryClassification> {
  const answer = state.answers[answerId];
  if (!answer) {
    return { kind: "not-attempted", answerId };
  }
  const delivery = state.deliveries[answerId];
  if (!delivery || delivery.status === "queued" || delivery.attempts === 0) {
    return { kind: "not-attempted", answerId };
  }
  if (delivery.status === "delivered" && delivery.receiptId) {
    return { kind: "delivered", answerId, receiptId: delivery.receiptId };
  }
  const receipt = await lookupReceipt(answerId);
  if (receipt) {
    return {
      kind: "delivered-reconciled",
      answerId,
      receiptId: receipt.receiptId,
    };
  }
  return {
    kind: "indeterminate",
    answerId,
    detail:
      "send was in-flight when the node stopped and the receiver directory has no receipt; retry against the same target",
  };
}

function otherParty(state: SessionState, party: IdentityId): IdentityId {
  if (!state.invitation) {
    throw new Error("session has no invitation");
  }
  return state.invitation.initiator === party
    ? state.invitation.receiver
    : state.invitation.initiator;
}

function rejected(reason: BlockReason, detail?: string): FactAcceptance {
  return { ok: false, reason, detail };
}

function accepted(): FactAcceptance {
  return { ok: true };
}

function deriveEventId(fact: SignedFact): string {
  return `evt:${fact.factId}`;
}
