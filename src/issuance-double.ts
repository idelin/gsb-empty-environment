import { canonicalJson, cloneFrozen, randomId } from "./canonical.ts";
import type {
  IssuanceLookup,
  IssuanceRequest,
  IssuanceService,
  IssuedCredential,
} from "./types.ts";

type TransactionRecord =
  | { status: "issued"; credential: IssuedCredential }
  | { status: "pending"; reason?: string }
  | { status: "failed"; reason?: string };

export interface IssuanceDoubleOptions {
  clock?: () => number;
  credentialTtlMs?: number;
}

/**
 * Test double for the external signer. It records what actually happened before
 * a response is lost, which lets the host distinguish "not issued" from
 * "issued but the receipt was dropped".
 */
export class InMemoryIssuanceService implements IssuanceService {
  readonly transactions = new Map<string, TransactionRecord>();
  issueCalls = 0;
  lookupCalls = 0;
  duplicateIssueCalls = 0;
  #clock: () => number;
  #credentialTtlMs: number;
  #dropNextResponse = false;
  #failNextBeforeCommit = false;
  #pendingBeforeIssue: Promise<void> | null = null;
  #issueStartedWaiter: (() => void) | null = null;
  #issueStartedPromise: Promise<void> | null = null;

  constructor(options: IssuanceDoubleOptions = {}) {
    this.#clock = options.clock ?? (() => Date.now());
    this.#credentialTtlMs = options.credentialTtlMs ?? 5 * 60_000;
  }

  dropNextResponseAfterSuccess(): void {
    this.#dropNextResponse = true;
  }

  failNextBeforeCommit(): void {
    this.#failNextBeforeCommit = true;
  }

  blockNextIssue(): () => void {
    let release!: () => void;
    this.#pendingBeforeIssue = new Promise<void>((resolve) => {
      release = resolve;
    });
    this.#issueStartedPromise = new Promise<void>((resolve) => {
      this.#issueStartedWaiter = resolve;
    });
    return () => release();
  }

  waitForIssueStarted(): Promise<void> {
    return this.#issueStartedPromise ?? Promise.resolve();
  }

  async issue(request: IssuanceRequest): Promise<IssuedCredential> {
    this.issueCalls += 1;
    const existing = this.transactions.get(request.transactionId);
    if (existing) {
      this.duplicateIssueCalls += 1;
      if (existing.status === "issued") return existing.credential;
      throw new Error(`Transaction ${request.transactionId} already exists as ${existing.status}`);
    }
    if (this.#pendingBeforeIssue) {
      this.#issueStartedWaiter?.();
      this.#issueStartedWaiter = null;
      await this.#pendingBeforeIssue;
      this.#pendingBeforeIssue = null;
    }
    if (this.#failNextBeforeCommit) {
      this.#failNextBeforeCommit = false;
      this.transactions.set(request.transactionId, { status: "failed", reason: "injected_failure" });
      throw new Error(`Injected issuance failure for ${request.transactionId}`);
    }

    const now = this.#clock();
    const credential: IssuedCredential = {
      credentialId: randomId("cred"),
      transactionId: request.transactionId,
      accountId: request.accountId,
      browserId: request.browserId,
      browserSessionId: request.browserSessionId,
      browserKeyId: request.browserKeyId,
      purpose: request.purpose,
      issuedAt: now,
      expiresAt: now + this.#credentialTtlMs,
      issuanceDigest: canonicalJson(request).length,
    };
    const frozen = cloneFrozen(credential);
    this.transactions.set(request.transactionId, { status: "issued", credential: frozen });
    if (this.#dropNextResponse) {
      this.#dropNextResponse = false;
      throw new Error("Injected lost issuance response after commit");
    }
    return frozen;
  }

  async lookup(transactionId: string): Promise<IssuanceLookup> {
    this.lookupCalls += 1;
    const record = this.transactions.get(transactionId);
    if (!record) return { status: "not_found" };
    if (record.status === "issued") {
      return { status: "issued", credential: record.credential };
    }
    if (record.status === "pending") return { status: "pending", reason: record.reason };
    return { status: "failed", reason: record.reason };
  }

  markPending(transactionId: string, reason?: string): void {
    this.transactions.set(transactionId, { status: "pending", reason });
  }
}
