import { ledgerError } from "./errors.ts";
import type { AtomicPersistence, PersistedLedgerState } from "./types.ts";

function cloneState(state: PersistedLedgerState | null | undefined): PersistedLedgerState | null {
  if (state === null || state === undefined) return null;
  return structuredClone(state);
}

export interface MemoryPersistenceOptions {
  initialState?: PersistedLedgerState | null;
  /** Throw before the N-th commit changes storage. */
  failAtCommit?: number;
  failOnCommit?: (commitNumber: number, state: PersistedLedgerState) => Error | undefined;
}

/**
 * Atomic in-memory persistence. A commit swaps one complete ledger bundle, which
 * mirrors the contract every durable adapter must implement.
 */
export class MemoryPersistence implements AtomicPersistence {
  private stored: PersistedLedgerState | null;
  private readonly failAtCommit?: number;
  private readonly failOnCommit?: MemoryPersistenceOptions["failOnCommit"];
  commitCount = 0;

  constructor(options: MemoryPersistenceOptions = {}) {
    this.stored = cloneState(options.initialState);
    this.failAtCommit = options.failAtCommit;
    this.failOnCommit = options.failOnCommit;
  }

  async load(): Promise<PersistedLedgerState | null> {
    return cloneState(this.stored);
  }

  async commit(state: PersistedLedgerState): Promise<void> {
    const commitNumber = this.commitCount + 1;
    const customFailure = this.failOnCommit?.(commitNumber, state);
    if (customFailure) throw customFailure;
    if (this.failAtCommit !== undefined && commitNumber === this.failAtCommit) {
      throw ledgerError("PERSISTENCE_ERROR", `simulated power loss at commit ${commitNumber}`);
    }
    // The clone and assignment are one atomic memory operation for this adapter.
    this.stored = cloneState(state);
    this.commitCount = commitNumber;
  }

  peek(): PersistedLedgerState | null {
    return cloneState(this.stored);
  }
}

export const InMemoryPersistence = MemoryPersistence;

interface PendingCommit {
  state: PersistedLedgerState;
}

/**
 * A pauseable facade around an atomic persistence adapter. While paused, commits
 * are held. Resuming flushes them in order, so observers never see a partial
 * head/finality/history tuple.
 */
export class PausablePersistence implements AtomicPersistence {
  private readonly inner: AtomicPersistence;
  private pauseDepth = 0;
  private pending: PendingCommit[] = [];
  private flushing: Promise<void> | null = null;

  constructor(inner: AtomicPersistence) {
    this.inner = inner;
  }

  pause(): void {
    this.pauseDepth += 1;
  }

  get paused(): boolean {
    return this.pauseDepth > 0;
  }

  load(): Promise<PersistedLedgerState | null | undefined> {
    return this.inner.load();
  }

  commit(state: PersistedLedgerState): Promise<void> {
    if (this.pauseDepth === 0) {
      return this.inner.commit(state);
    }
    this.pending.push({ state });
    // The caller accepts the write into the pause buffer; durability completes
    // when resume() flushes the buffer.
    return Promise.resolve();
  }

  async resume(): Promise<void> {
    if (this.pauseDepth === 0) {
      throw ledgerError("PERSISTENCE_ERROR", "persistence is not paused");
    }
    this.pauseDepth -= 1;
    if (this.pauseDepth > 0 || this.pending.length === 0) return;
    if (this.flushing) return this.flushing;

    this.flushing = (async () => {
      const pending = this.pending;
      this.pending = [];
      for (const entry of pending) {
        await this.inner.commit(entry.state);
      }
    })();
    try {
      await this.flushing;
    } finally {
      this.flushing = null;
    }
  }

  pendingCount(): number {
    return this.pending.length;
  }
}

export const PauseablePersistence = PausablePersistence;
