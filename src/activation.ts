import type { Clock } from './clock.ts';
import {
  CrossDocumentTransferError,
  TokenUnavailableError,
} from './errors.ts';

export type UserEventType =
  | 'click'
  | 'keydown'
  | 'pointerdown'
  | 'touchstart'
  | 'contextmenu';

export interface TrustedUserEvent {
  readonly type: UserEventType;
  readonly isTrusted: boolean;
  readonly eventId: string;
  readonly timestamp: number;
}

export type TokenStatus =
  | 'active'
  | 'consumed'
  | 'expired'
  | 'revoked'
  | 'superseded';

export type ConsumeBlockedReason =
  | 'no-activation'
  | 'unknown-document'
  | 'unknown-token'
  | 'expired'
  | 'consumed'
  | 'revoked'
  | 'superseded'
  | 'cross-document';

export interface ConsumeSuccess {
  readonly ok: true;
  readonly documentId: string;
  readonly token: ActivationToken;
}

export interface ConsumeFailure {
  readonly ok: false;
  readonly documentId: string;
  readonly reason: ConsumeBlockedReason;
  readonly detail: string;
  readonly token: ActivationToken | null;
}

export type ConsumeResult = ConsumeSuccess | ConsumeFailure;

export interface Inspection {
  readonly live: boolean;
  readonly status: TokenStatus | null;
  readonly token: ActivationToken | null;
}

export interface LedgerEntry {
  readonly at: number;
  readonly documentId: string;
  readonly operation: 'consume' | 'inspect';
  readonly result: 'granted' | ConsumeBlockedReason;
  readonly tokenId: string | null;
}

export interface UserActivationServiceOptions {
  readonly ttlMs: number;
  readonly clock: Clock;
}

let tokenSequence = 0;

/**
 * One-shot user activation token.
 *
 * A token is bound to exactly one document and one trusted user event, and is
 * only valid until its deadline. State transitions:
 *
 *   active -> consumed   (synchronous claim, happens at most once)
 *   active -> expired    (lazy, evaluated against the clock)
 *   active -> revoked    (document navigation)
 *   active -> superseded (a newer trusted event re-armed the document)
 *
 * Every non-active state is terminal.
 */
export class ActivationToken {
  readonly #id: string;
  readonly #documentId: string;
  readonly #event: TrustedUserEvent;
  readonly #issuedAt: number;
  readonly #expiresAt: number;
  #status: TokenStatus = 'active';
  #consumedAt: number | null = null;

  constructor(
    documentId: string,
    event: TrustedUserEvent,
    issuedAt: number,
    ttlMs: number,
  ) {
    if (ttlMs <= 0) {
      throw new Error(`ttlMs must be positive, got ${ttlMs}`);
    }
    this.#id = `token-${++tokenSequence}`;
    this.#documentId = documentId;
    this.#event = event;
    this.#issuedAt = issuedAt;
    this.#expiresAt = issuedAt + ttlMs;
  }

  get id(): string {
    return this.#id;
  }

  get documentId(): string {
    return this.#documentId;
  }

  get event(): TrustedUserEvent {
    return this.#event;
  }

  get issuedAt(): number {
    return this.#issuedAt;
  }

  get expiresAt(): number {
    return this.#expiresAt;
  }

  get consumedAt(): number | null {
    return this.#consumedAt;
  }

  #evaluate(now: number): TokenStatus {
    if (this.#status === 'active' && now >= this.#expiresAt) {
      this.#status = 'expired';
    }
    return this.#status;
  }

  status(now: number): TokenStatus {
    return this.#evaluate(now);
  }

  isLive(now: number): boolean {
    return this.#evaluate(now) === 'active';
  }

  /** Atomic check-and-flip. Returns the resulting status; only `active` flips. */
  claim(now: number): TokenStatus {
    if (this.#evaluate(now) === 'active') {
      this.#status = 'consumed';
      this.#consumedAt = now;
    }
    return this.#status;
  }

  revoke(now: number): boolean {
    if (this.#evaluate(now) === 'active') {
      this.#status = 'revoked';
      return true;
    }
    return false;
  }

  supersede(now: number): boolean {
    if (this.#evaluate(now) === 'active') {
      this.#status = 'superseded';
      return true;
    }
    return false;
  }
}

interface DocumentState {
  current: ActivationToken | null;
  history: ActivationToken[];
  sticky: boolean;
}

export class UserActivationService {
  readonly #ttlMs: number;
  readonly #clock: Clock;
  readonly #documents = new Map<string, DocumentState>();
  readonly #ledger: LedgerEntry[] = [];

  constructor(options: UserActivationServiceOptions) {
    if (options.ttlMs <= 0) {
      throw new Error(`ttlMs must be positive, got ${options.ttlMs}`);
    }
    this.#ttlMs = options.ttlMs;
    this.#clock = options.clock;
  }

  registerDocument(documentId: string): void {
    if (this.#documents.has(documentId)) {
      throw new Error(`document "${documentId}" is already registered`);
    }
    this.#documents.set(documentId, {
      current: null,
      history: [],
      sticky: false,
    });
  }

  /**
   * Arms activation from a user event. Only trusted events count. A new
   * trusted event supersedes any still-live token and mints a fresh token
   * with a fresh deadline.
   */
  notifyUserEvent(documentId: string, event: TrustedUserEvent): boolean {
    if (!event.isTrusted) {
      return false;
    }
    const state = this.#documents.get(documentId);
    if (!state) {
      return false;
    }
    const now = this.#clock.now();
    if (state.current?.isLive(now)) {
      state.current.supersede(now);
    }
    const token = new ActivationToken(documentId, event, now, this.#ttlMs);
    state.current = token;
    state.history.push(token);
    state.sticky = true;
    return true;
  }

  /** Non-consuming inspection. Never flips token state beyond lazy expiry. */
  inspect(documentId: string): Inspection {
    const state = this.#documents.get(documentId);
    if (!state) {
      this.#record('inspect', documentId, null, 'unknown-document');
      return { live: false, status: null, token: null };
    }
    const token = state.current;
    if (!token) {
      this.#record('inspect', documentId, null, 'no-activation');
      return { live: false, status: null, token: null };
    }
    const status = token.status(this.#clock.now());
    this.#record(
      'inspect',
      documentId,
      token,
      status === 'active' ? 'granted' : status,
    );
    return { live: status === 'active', status, token };
  }

  hasTransientActivation(documentId: string): boolean {
    return this.inspect(documentId).live;
  }

  hasStickyActivation(documentId: string): boolean {
    return this.#documents.get(documentId)?.sticky ?? false;
  }

  currentToken(documentId: string): ActivationToken | null {
    return this.#documents.get(documentId)?.current ?? null;
  }

  getToken(documentId: string, tokenId: string): ActivationToken | null {
    const state = this.#documents.get(documentId);
    if (!state) {
      return null;
    }
    return state.history.find((candidate) => candidate.id === tokenId) ?? null;
  }

  /** Consuming operation on the document's current token. Atomic, one-shot. */
  tryConsume(documentId: string): ConsumeResult {
    const state = this.#documents.get(documentId);
    if (!state) {
      return this.#failure(
        documentId,
        null,
        'unknown-document',
        `document "${documentId}" is not registered`,
      );
    }
    const token = state.current;
    if (!token) {
      return this.#failure(
        documentId,
        null,
        'no-activation',
        'document has no current activation token',
      );
    }
    return this.#claim(documentId, token);
  }

  /**
   * Consuming operation on a specific token. Tokens minted for another
   * document are rejected without touching their state, so a transfer attempt
   * can never steal or burn the origin document's activation.
   */
  tryConsumeToken(documentId: string, token: ActivationToken): ConsumeResult {
    if (!this.#documents.has(documentId)) {
      return this.#failure(
        documentId,
        null,
        'unknown-document',
        `document "${documentId}" is not registered`,
      );
    }
    if (token.documentId !== documentId) {
      this.#record('consume', documentId, token, 'cross-document');
      return this.#failure(
        documentId,
        token,
        'cross-document',
        `token ${token.id} is bound to document "${token.documentId}"`,
      );
    }
    return this.#claim(documentId, token);
  }

  strictConsume(documentId: string): ActivationToken {
    const result = this.tryConsume(documentId);
    if (result.ok) {
      return result.token;
    }
    throw this.#toError(result);
  }

  strictConsumeToken(documentId: string, token: ActivationToken): ActivationToken {
    const result = this.tryConsumeToken(documentId, token);
    if (result.ok) {
      return result.token;
    }
    throw this.#toError(result);
  }

  /**
   * Document navigation (or destruction). Revokes the live token and clears
   * both transient and sticky activation for that document.
   */
  navigate(documentId: string): boolean {
    const state = this.#documents.get(documentId);
    if (!state) {
      return false;
    }
    const now = this.#clock.now();
    state.current?.revoke(now);
    state.current = null;
    state.sticky = false;
    return true;
  }

  ledger(): readonly LedgerEntry[] {
    return this.#ledger;
  }

  #claim(documentId: string, token: ActivationToken): ConsumeResult {
    const now = this.#clock.now();
    const wasActive = token.isLive(now);
    const status = token.claim(now);
    if (wasActive && status === 'consumed') {
      this.#record('consume', documentId, token, 'granted');
      return { ok: true, documentId, token };
    }
    const reason = status as Exclude<TokenStatus, 'active'>;
    this.#record('consume', documentId, token, reason);
    return this.#failure(
      documentId,
      token,
      reason,
      `token ${token.id} is ${status}`,
    );
  }

  #failure(
    documentId: string,
    token: ActivationToken | null,
    reason: ConsumeBlockedReason,
    detail: string,
  ): ConsumeResult {
    this.#record('consume', documentId, token, reason);
    return { ok: false, documentId, reason, detail, token };
  }

  #record(
    operation: LedgerEntry['operation'],
    documentId: string,
    token: ActivationToken | null,
    result: LedgerEntry['result'],
  ): void {
    this.#ledger.push({
      at: this.#clock.now(),
      documentId,
      operation,
      result,
      tokenId: token?.id ?? null,
    });
  }

  #toError(result: ConsumeResult): ActivationErrorLike {
    if (result.reason === 'cross-document' && result.token) {
      return new CrossDocumentTransferError(
        result.token.documentId,
        result.documentId,
      );
    }
    return new TokenUnavailableError(result.reason, result.detail);
  }
}

type ActivationErrorLike = CrossDocumentTransferError | TokenUnavailableError;
