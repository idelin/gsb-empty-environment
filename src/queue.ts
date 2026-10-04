import type { Executor } from './executor.ts';
import { MemoryJournal, type Journal } from './journal.ts';
import type { FileStore } from './store.ts';
import {
  ReceiptLostError,
  VersionConflictError,
  type CancelResult,
  type PublishHandle,
  type PublishOutcome,
  type PublishReceipt,
  type PublishRequest,
} from './types.ts';

type TaskState = 'queued' | 'checking' | 'committing' | 'settled';

interface Task {
  request: PublishRequest;
  state: TaskState;
  cancelRequested: boolean;
  outcome?: PublishOutcome;
  resolve: (outcome: PublishOutcome) => void;
}

export interface PublishQueueOptions {
  executor: Executor;
  store: FileStore;
  journal?: Journal;
  now?: () => number;
}

/**
 * Publish queue: requests to the same path run strictly in order, requests to
 * different paths run concurrently. Cancellation is honoured up to the
 * confirmation point; committed results are journaled for idempotent recovery.
 */
export class PublishQueue {
  private readonly executor: Executor;
  private readonly store: FileStore;
  private readonly journal: Journal;
  private readonly now: () => number;

  /** Per-path tail promise; chaining onto it serializes that path. */
  private readonly tails = new Map<string, Promise<void>>();
  /** Idempotency: key -> settled committed receipt. */
  private readonly committedByKey = new Map<string, PublishReceipt>();

  constructor(options: PublishQueueOptions) {
    this.executor = options.executor;
    this.store = options.store;
    this.journal = options.journal ?? new MemoryJournal();
    this.now = options.now ?? (() => Date.now());
  }

  /** Replay journaled commits so resubmissions return the recorded result. */
  async recover(): Promise<void> {
    for (const entry of await this.journal.entries()) {
      const key = entry.request.idempotencyKey;
      if (key !== undefined && !this.committedByKey.has(key)) {
        this.committedByKey.set(key, entry.receipt);
      }
    }
  }

  publish(request: PublishRequest): PublishHandle {
    const key = request.idempotencyKey;
    if (key !== undefined) {
      const recorded = this.committedByKey.get(key);
      if (recorded !== undefined) {
        return settledHandle(request, { kind: 'committed', receipt: recorded });
      }
    }

    let resolveDone!: (outcome: PublishOutcome) => void;
    const done = new Promise<PublishOutcome>((resolve) => {
      resolveDone = resolve;
    });
    const task: Task = {
      request,
      state: 'queued',
      cancelRequested: false,
      resolve: resolveDone,
    };

    const tail = this.tails.get(request.path) ?? Promise.resolve();
    const run = tail.then(() => this.runTask(task));
    this.tails.set(request.path, run.catch(() => undefined));

    return {
      request,
      done,
      cancel: () => this.cancel(task),
    };
  }

  private cancel(task: Task): CancelResult {
    switch (task.state) {
      case 'queued':
      case 'checking':
        task.cancelRequested = true;
        return { ok: true, status: 'cancelled' };
      case 'committing':
        return { ok: false, status: 'committing' };
      case 'settled': {
        const outcome = task.outcome!;
        if (outcome.kind === 'committed') {
          return { ok: false, status: 'committed', receipt: outcome.receipt };
        }
        return { ok: false, status: 'settled', outcome };
      }
    }
  }

  private async runTask(task: Task): Promise<void> {
    const { request } = task;
    const settle = (outcome: PublishOutcome): void => {
      task.state = 'settled';
      task.outcome = outcome;
      if (outcome.kind === 'committed' && request.idempotencyKey !== undefined) {
        this.committedByKey.set(request.idempotencyKey, outcome.receipt);
      }
      task.resolve(outcome);
    };

    if (task.cancelRequested) {
      settle({ kind: 'cancelled' });
      return;
    }

    task.state = 'checking';
    try {
      await this.executor.check(request);
    } catch (error) {
      settle({ kind: 'failed', error: asError(error) });
      return;
    }

    if (task.cancelRequested) {
      settle({ kind: 'cancelled' });
      return;
    }

    // Confirmation point: once commit begins, cancellation is no longer possible.
    task.state = 'committing';
    let receipt: PublishReceipt;
    try {
      receipt = await this.executor.commit(request);
    } catch (error) {
      if (error instanceof ReceiptLostError) {
        const reconciled = await this.reconcile(request);
        if (reconciled !== undefined) {
          await this.journal.recordCommitted({ request, receipt: reconciled });
          settle({ kind: 'committed', receipt: reconciled });
          return;
        }
        settle({ kind: 'failed', error });
        return;
      }
      if (error instanceof VersionConflictError) {
        // Idempotent resubmit: the commit may already have landed before a crash.
        const replayed = await this.matchesIntent(request);
        if (replayed !== undefined) {
          await this.journal.recordCommitted({ request, receipt: replayed });
          settle({ kind: 'committed', receipt: replayed });
          return;
        }
        settle({ kind: 'conflict', currentVersion: error.currentVersion });
        return;
      }
      settle({ kind: 'failed', error: asError(error) });
      return;
    }

    await this.journal.recordCommitted({ request, receipt });
    settle({ kind: 'committed', receipt });
  }

  /** After a lost receipt, read the store to learn whether the commit landed. */
  private async reconcile(request: PublishRequest): Promise<PublishReceipt | undefined> {
    return this.matchesIntent(request);
  }

  private async matchesIntent(request: PublishRequest): Promise<PublishReceipt | undefined> {
    const state = await this.store.read(request.path);
    if (
      state !== undefined &&
      state.version === request.expectedVersion + 1 &&
      state.digest === request.digest
    ) {
      return {
        path: request.path,
        digest: state.digest,
        version: state.version,
        committedAt: this.now(),
        idempotencyKey: request.idempotencyKey,
      };
    }
    return undefined;
  }
}

function settledHandle(request: PublishRequest, outcome: PublishOutcome): PublishHandle {
  return {
    request,
    done: Promise.resolve(outcome),
    cancel: () => ({ ok: false, status: 'settled', outcome }),
  };
}

function asError(error: unknown): Error {
  return error instanceof Error ? error : new Error(String(error));
}
