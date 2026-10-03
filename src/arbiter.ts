/**
 * Multi-node contention & conflict arbiter.
 *
 * Guarantees:
 * - Atomicity: a transaction's mutations are applied in one synchronous,
 *   non-interruptible step; partial application is impossible.
 * - Linearizability: every operation passes through a single serialized
 *   queue; its linearization point is the moment it is applied, and each
 *   commit receives a unique, monotonically increasing sequence number.
 * - No deadlock: the queue is a lock-free promise chain; no operation ever
 *   waits on another operation's completion, only on its own turn.
 * - No data races: shared state is mutated only inside the serialized
 *   apply step on the single-threaded event loop.
 * - No leaks: every submitted operation settles exactly once; live
 *   operations are tracked in a registry that must drain to zero.
 */
import { ConflictError } from './errors.ts';

export interface CasOp {
  key: string;
  /** Version the node observed; the op commits only if it still matches. */
  expectedVersion: number;
  value: unknown;
}

export interface CommitTicket {
  seq: number;
  nodeId: string;
  results: { key: string; version: number }[];
}

export interface ReadResult {
  key: string;
  version: number;
  value: unknown;
}

export interface ArbiterStats {
  seq: number;
  committed: number;
  conflicts: number;
  pending: number;
  liveOps: number;
}

interface Cell {
  version: number;
  value: unknown;
}

export class Arbiter {
  private readonly cells = new Map<string, Cell>();
  private tail: Promise<void> = Promise.resolve();
  private seq = 0;
  private committed = 0;
  private conflicts = 0;
  private pending = 0;
  private readonly liveOps = new Set<symbol>();
  private closed = false;
  private idleWaiters: (() => void)[] = [];

  /** Linearizable read: enqueued behind all previously submitted writes. */
  read(key: string): Promise<ReadResult> {
    return this.enqueue(`reader`, () => {
      const cell = this.cells.get(key);
      return { key, version: cell?.version ?? 0, value: cell?.value };
    });
  }

  /**
   * Atomic multi-key compare-and-swap transaction. Either every op commits
   * (all versions matched) or none do and a ConflictError is raised.
   */
  transact(nodeId: string, ops: CasOp[]): Promise<CommitTicket> {
    if (ops.length === 0) {
      return Promise.reject(new Error('transaction requires at least one op'));
    }
    return this.enqueue(nodeId, () => {
      // Phase 1: validate everything (no mutation yet).
      for (const op of ops) {
        const current = this.cells.get(op.key)?.version ?? 0;
        if (current !== op.expectedVersion) {
          this.conflicts += 1;
          throw new ConflictError(op.key, op.expectedVersion, current);
        }
      }
      // Phase 2: apply atomically. This block never yields, so no other
      // operation can observe an intermediate state.
      const ticketSeq = ++this.seq;
      const results = ops.map((op) => {
        const version = (this.cells.get(op.key)?.version ?? 0) + 1;
        this.cells.set(op.key, { version, value: op.value });
        return { key: op.key, version };
      });
      this.committed += 1;
      return { seq: ticketSeq, nodeId, results };
    });
  }

  /** Convenience single-key CAS. */
  compareAndSwap(nodeId: string, key: string, expectedVersion: number, value: unknown) {
    return this.transact(nodeId, [{ key, expectedVersion, value }]);
  }

  /**
   * Optimistic-concurrency helper: read, compute, CAS, retry on conflict.
   * Used by nodes racing on the same key; always terminates as long as
   * `update` is pure, because each retry observes the newest version.
   */
  async submitWithRetry<T>(
    nodeId: string,
    key: string,
    update: (current: T | undefined) => T,
    maxAttempts = 10_000,
  ): Promise<CommitTicket> {
    for (let attempt = 0; attempt < maxAttempts; attempt += 1) {
      const { version, value } = await this.read(key);
      try {
        return await this.compareAndSwap(nodeId, key, version, update(value as T | undefined));
      } catch (err) {
        if (!(err instanceof ConflictError)) throw err;
      }
    }
    throw new Error(`node ${nodeId} exceeded ${maxAttempts} contention retries on "${key}"`);
  }

  /** Current state snapshot (for tests/inspection; not linearizable). */
  snapshot(): Map<string, Cell> {
    return new Map(this.cells);
  }

  stats(): ArbiterStats {
    return {
      seq: this.seq,
      committed: this.committed,
      conflicts: this.conflicts,
      pending: this.pending,
      liveOps: this.liveOps.size,
    };
  }

  /** Resolves once every queued operation has settled. */
  async drain(): Promise<void> {
    await this.tail;
    if (this.pending > 0) {
      await new Promise<void>((resolve) => this.idleWaiters.push(resolve));
    }
  }

  /** Reject new submissions and wait for in-flight work to finish. */
  async close(): Promise<void> {
    this.closed = true;
    await this.drain();
  }

  private enqueue<T>(nodeId: string, task: () => T): Promise<T> {
    if (this.closed) {
      return Promise.reject(new Error(`arbiter is closed; node ${nodeId} cannot submit`));
    }
    this.pending += 1;
    const token = Symbol(`op:${nodeId}`);
    this.liveOps.add(token);
    const run = this.tail.then(() => task());
    // Keep the chain rejection-agnostic so one conflict cannot wedge the queue.
    this.tail = run.then(
      () => undefined,
      () => undefined,
    );
    const settled = run.finally(() => {
      this.pending -= 1;
      this.liveOps.delete(token);
      if (this.pending === 0) {
        const waiters = this.idleWaiters.splice(0);
        for (const wake of waiters) wake();
      }
    });
    return settled;
  }
}
