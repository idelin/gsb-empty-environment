import type {
  AnswerId,
  DeliveryVerdict,
  FactAcceptResult,
  IdentityId,
  NodeId,
  RecoveryClassification,
  SessionEvent,
  SessionId,
  SessionState,
  SignedFact,
  TransportAddress,
} from "./types.ts";
import { withCasRetry } from "./store.ts";
import type { CasStore } from "./store.ts";
import type { SignatureService } from "./crypto.ts";
import type { ReceiptDirectory } from "./receipts.ts";
import {
  acceptFact,
  classifyRecovery,
  evaluateAnswer,
  initialSessionState,
  replay,
} from "./session.ts";

export interface VerdictNotifier {
  /**
   * Called once per answer whenever its verdict changes. Repeated queries
   * with the same verdict never trigger another call.
   */
  onVerdict(verdict: DeliveryVerdict): void;
}

export interface CoordinatorOptions {
  readonly nodeId: NodeId;
  readonly store: CasStore;
  readonly signatures: SignatureService;
  readonly receipts: ReceiptDirectory;
  readonly notifier?: VerdictNotifier;
  readonly clock?: () => number;
}

interface SessionRecord {
  readonly events: readonly SessionEvent[];
}

interface SessionIndex {
  readonly sessionIds: readonly SessionId[];
}

const INDEX_KEY = "index:sessions";

export class SessionCoordinator {
  readonly #nodeId: NodeId;
  readonly #store: CasStore;
  readonly #signatures: SignatureService;
  readonly #receipts: ReceiptDirectory;
  readonly #notifier?: VerdictNotifier;
  readonly #clock: () => number;
  readonly #lastVerdict = new Map<AnswerId, string>();

  constructor(options: CoordinatorOptions) {
    this.#nodeId = options.nodeId;
    this.#store = options.store;
    this.#signatures = options.signatures;
    this.#receipts = options.receipts;
    this.#notifier = options.notifier;
    this.#clock = options.clock ?? (() => Date.now());
  }

  get nodeId(): NodeId {
    return this.#nodeId;
  }

  /** Validate signatures and domain rules, then durably append the fact. */
  async submitFact(sessionId: SessionId, fact: SignedFact): Promise<FactAcceptResult> {
    const signatureOk = await this.#signatures.verifyFact(fact);
    if (!signatureOk) {
      return { ok: false, reason: "bad-signature", detail: fact.factId };
    }
    const now = this.#clock();
    const key = sessionKey(sessionId);
    for (;;) {
      const existing = await this.#store.get<SessionRecord>(key);
      const state = existing ? replay(existing.value.events) : initialSessionState();
      const decision = acceptFact(state, fact, now, this.#nodeId);
      if (!decision.ok) {
        return decision;
      }
      const nextEvents = [...(existing?.value.events ?? []), decision.event];
      const swapped = await this.#store.cas<SessionRecord>(
        key,
        existing ? existing.version : null,
        { events: nextEvents },
      );
      if (swapped) {
        break;
      }
    }
    await this.#indexSession(sessionId);
    const result = await this.#store.get<SessionRecord>(key);
    if (!result) {
      throw new Error("session record vanished after append");
    }
    const state = replay(result.value.events);
    await this.#notifyAnswers(state);
    return {
      ok: true,
      event: result.value.events[result.value.events.length - 1] as SessionEvent,
    };
  }

  /**
   * Record a transport connection. Display name and address are metadata only;
   * identity and generation drive every routing decision.
   */
  async registerConnection(
    sessionId: SessionId,
    identity: IdentityId,
    displayName: string,
    address: TransportAddress,
    generation: number,
  ): Promise<void> {
    const event: SessionEvent = {
      eventId: `evt:conn:${sessionId}:${identity}:${generation}`,
      kind: "connection-registered",
      at: this.#clock(),
      node: this.#nodeId,
      identity,
      displayName,
      address,
      generation,
    };
    const record = await withCasRetry<SessionRecord>(this.#store, sessionKey(sessionId), (current) => {
      const events = current?.events ?? [];
      if (events.some((entry) => entry.eventId === event.eventId)) {
        return { events };
      }
      return { events: [...events, event] };
    });
    await this.#indexSession(sessionId);
    await this.#notifyAnswers(replay(record.value.events));
  }

  /** Mark a currently registered connection as disconnected. */
  async releaseConnection(
    sessionId: SessionId,
    identity: IdentityId,
    generation: number,
  ): Promise<void> {
    const record = await this.#appendEvent(sessionId, {
      eventId: `evt:disc:${sessionId}:${identity}:${generation}`,
      kind: "connection-released",
      at: this.#clock(),
      node: this.#nodeId,
      identity,
      generation,
    });
    await this.#notifyAnswers(replay(record.events));
  }

  /** Replay one session log into pure state. */
  async getState(sessionId: SessionId): Promise<SessionState> {
    const record = await this.#store.get<SessionRecord>(sessionKey(sessionId));
    return record ? replay(record.value.events) : initialSessionState();
  }

  /** Current delivery verdict, with change-only callback notification. */
  async queryVerdict(sessionId: SessionId, answerId: AnswerId): Promise<DeliveryVerdict> {
    const state = await this.getState(sessionId);
    const verdict = evaluateAnswer(state, answerId);
    this.#emitIfChanged(verdict);
    return verdict;
  }

  /**
   * Attempt delivery of a deliverable answer. The receiver-side receipt is
   * claimed first, making the send idempotent and queryable. A crash between
   * claim and confirmation is recoverable instead of silently re-routed.
   */
  async attemptDelivery(sessionId: SessionId, answerId: AnswerId): Promise<DeliveryVerdict> {
    const state = await this.getState(sessionId);
    const verdict = evaluateAnswer(state, answerId);
    if (verdict.status !== "deliverable") {
      this.#emitIfChanged(verdict);
      return verdict;
    }
    const answer = state.answers[answerId];
    const receipt = await this.#receipts.claim({
      answerId,
      sessionId,
      invitationDigest: answer.invitationDigest,
      recipient: answer.to,
      generation: verdict.generation,
      node: verdict.node,
    });
    await this.#appendEvent(sessionId, {
      eventId: `evt:attempt:${answerId}:${receipt.receiptId}`,
      kind: "delivery-attempt",
      at: this.#clock(),
      answerId,
    });
    return this.queryVerdict(sessionId, answerId);
  }

  /** Mark an in-flight send as confirmed by its queryable receipt. */
  async confirmDelivery(
    sessionId: SessionId,
    answerId: AnswerId,
    receiptId: string,
  ): Promise<void> {
    const receipt = await this.#receipts.getByAnswer(answerId);
    if (!receipt || receipt.receiptId !== receiptId) {
      throw new Error(`receipt ${receiptId} not found for answer ${answerId}`);
    }
    await this.#appendEvent(sessionId, {
      eventId: `evt:confirm:${answerId}:${receiptId}`,
      kind: "delivery-confirmed",
      at: this.#clock(),
      answerId,
      receiptId,
    });
    await this.queryVerdict(sessionId, answerId);
  }

  /**
   * Recover one answer after a restart. Distinguishes confirmed delivery,
   * receiver-confirmed delivery the relay had lost the response for, and
   * genuinely indeterminate sends.
   */
  async recoverAnswer(sessionId: SessionId, answerId: AnswerId): Promise<RecoveryClassification> {
    const state = await this.getState(sessionId);
    return classifyRecovery(state, answerId, (id) => this.#receipts.getByAnswer(id));
  }

  /** Every verdict in the session log, evaluated on the current partial log. */
  async currentVerdicts(sessionId: SessionId): Promise<readonly DeliveryVerdict[]> {
    const state = await this.getState(sessionId);
    return Object.keys(state.answers).map((answerId) => evaluateAnswer(state, answerId));
  }

  async #appendEvent(sessionId: SessionId, event: SessionEvent): Promise<SessionRecord> {
    const record = await withCasRetry<SessionRecord>(this.#store, sessionKey(sessionId), (current) => {
      const events = current?.events ?? [];
      if (events.some((entry) => entry.eventId === event.eventId)) {
        return { events };
      }
      return { events: [...events, event] };
    });
    await this.#indexSession(sessionId);
    return record.value;
  }

  async #indexSession(sessionId: SessionId): Promise<void> {
    await withCasRetry<SessionIndex>(this.#store, INDEX_KEY, (current) => {
      const sessionIds = current?.sessionIds ?? [];
      if (sessionIds.includes(sessionId)) {
        return { sessionIds };
      }
      return { sessionIds: [...sessionIds, sessionId] };
    });
  }

  async #notifyAnswers(state: SessionState): Promise<void> {
    for (const answerId of Object.keys(state.answers)) {
      this.#emitIfChanged(evaluateAnswer(state, answerId));
    }
  }

  #emitIfChanged(verdict: DeliveryVerdict): void {
    if (!this.#notifier) {
      return;
    }
    const fingerprint = JSON.stringify(verdict);
    if (this.#lastVerdict.get(verdict.answerId) === fingerprint) {
      return;
    }
    this.#lastVerdict.set(verdict.answerId, fingerprint);
    this.#notifier.onVerdict(verdict);
  }
}

function sessionKey(sessionId: SessionId): string {
  return `session:${sessionId}`;
}
