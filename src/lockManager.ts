import { ShrinkPhaseManager } from './monitor.ts';
import { DeadlockError, TransactionStateError } from './types.ts';
import type { LockMode } from './types.ts';

interface LockRequest {
  txId: number;
  mode: LockMode;
  resolve: () => void;
  reject: (error: Error) => void;
  settled: boolean;
}

interface ResourceLock {
  /** Current holders: txId -> mode. */
  holders: Map<number, LockMode>;
  /** FIFO wait queue of pending requests. */
  queue: LockRequest[];
}

function compatible(a: LockMode, b: LockMode): boolean {
  return a === 'S' && b === 'S';
}

/**
 * LockManager — SS2PL lock table.
 *
 * Growing-phase acquisitions go through `acquire`. There is intentionally
 * NO public single-lock release: the only exit path is `releaseAll`, which
 * the TransactionManager invokes exactly once at the commit/abort shrinking
 * point. Every acquire/release is reported to the ShrinkPhaseManager.
 *
 * Deadlocks are detected with a wait-for graph cycle check; the youngest
 * transaction in the cycle (highest id) is chosen as victim and its pending
 * `acquire` rejects with DeadlockError.
 */
export class LockManager {
  private readonly table = new Map<string, ResourceLock>();

  private readonly monitor: ShrinkPhaseManager;

  constructor(monitor: ShrinkPhaseManager) {
    this.monitor = monitor;
  }

  private entry(resource: string): ResourceLock {
    let lock = this.table.get(resource);
    if (!lock) {
      lock = { holders: new Map(), queue: [] };
      this.table.set(resource, lock);
    }
    return lock;
  }

  /** Locks currently held by a transaction (live view from the table). */
  heldBy(txId: number): Map<string, LockMode> {
    const result = new Map<string, LockMode>();
    for (const [resource, lock] of this.table) {
      const mode = lock.holders.get(txId);
      if (mode) result.set(resource, mode);
    }
    return result;
  }

  async acquire(txId: number, resource: string, mode: LockMode): Promise<void> {
    const lock = this.entry(resource);
    const held = lock.holders.get(txId);

    // Already covered by an equal or stronger lock.
    if (held === mode || (held === 'X' && mode === 'S')) return;

    if (this.canGrant(lock, txId, mode)) {
      lock.holders.set(txId, mode);
      this.monitor.onAcquire(txId, resource, mode);
      return;
    }

    // Must wait. Register the request, then check for deadlock cycles.
    return new Promise<void>((resolve, reject) => {
      const request: LockRequest = { txId, mode, resolve, reject, settled: false };
      lock.queue.push(request);
      const cycle = this.findCycle();
      if (cycle) {
        const victim = Math.max(...cycle);
        this.abortVictim(victim, cycle);
      }
    });
  }

  private canGrant(lock: ResourceLock, txId: number, mode: LockMode): boolean {
    for (const [holder, holderMode] of lock.holders) {
      if (holder !== txId && !compatible(holderMode, mode)) return false;
    }
    // Fairness: do not jump ahead of incompatible queued requests,
    // except for an upgrade by a transaction that already holds the lock.
    const isUpgrade = lock.holders.has(txId);
    if (!isUpgrade) {
      for (const queued of lock.queue) {
        if (queued.txId === txId) continue;
        if (!compatible(queued.mode, mode)) return false;
      }
    }
    return true;
  }

  /**
   * The ONLY release path: atomically drop every lock of a transaction.
   * Must be called inside the monitor's atomic shrinking point.
   */
  releaseAll(txId: number): void {
    for (const [resource, lock] of this.table) {
      const mode = lock.holders.get(txId);
      if (mode) {
        lock.holders.delete(txId);
        this.monitor.onRelease(txId, resource, mode);
      }
      // Drop any pending requests of this tx (e.g. deadlock victim).
      for (const request of lock.queue) {
        if (request.txId === txId && !request.settled) {
          request.settled = true;
          request.reject(new TransactionStateError(`tx${txId} released while waiting on ${resource}`));
        }
      }
      lock.queue = lock.queue.filter((request) => request.txId !== txId);
    }
    this.grantWaiters();
  }

  /**
   * Head-of-queue admissibility: only holders matter. Unlike `canGrant`
   * (used for new arrivals), a queued request is never blocked by
   * requests behind it — FIFO order already gives it priority.
   */
  private canGrantHead(lock: ResourceLock, request: LockRequest): boolean {
    for (const [holder, holderMode] of lock.holders) {
      if (holder !== request.txId && !compatible(holderMode, request.mode)) return false;
    }
    return true;
  }

  /** Grant queued requests that have become admissible, in FIFO order. */
  private grantWaiters(): void {
    let progressed = true;
    while (progressed) {
      progressed = false;
      for (const lock of this.table.values()) {
        for (const request of [...lock.queue]) {
          if (request.settled) continue;
          if (!this.canGrantHead(lock, request)) break; // FIFO per resource
          request.settled = true;
          lock.queue = lock.queue.filter((queued) => queued !== request);
          lock.holders.set(request.txId, request.mode);
          // Find the resource name back for the monitor event.
          const resource = [...this.table.entries()].find(([, l]) => l === lock)![0];
          this.monitor.onAcquire(request.txId, resource, request.mode);
          request.resolve();
          progressed = true;
        }
      }
    }
  }

  /** Wait-for graph: waiting tx -> every tx blocking its request. */
  private waitForEdges(): Map<number, Set<number>> {
    const edges = new Map<number, Set<number>>();
    for (const lock of this.table.values()) {
      const pending = lock.queue.filter((request) => !request.settled);
      for (let i = 0; i < pending.length; i++) {
        const request = pending[i];
        const blockers = new Set<number>();
        for (const [holder, holderMode] of lock.holders) {
          if (holder !== request.txId && !compatible(holderMode, request.mode)) {
            blockers.add(holder);
          }
        }
        // Upgrade requests are also blocked by other holders regardless.
        if (lock.holders.has(request.txId)) {
          for (const holder of lock.holders.keys()) {
            if (holder !== request.txId) blockers.add(holder);
          }
        }
        // FIFO fairness: incompatible requests queued ahead also block us.
        for (let j = 0; j < i; j++) {
          const ahead = pending[j];
          if (ahead.txId !== request.txId && !compatible(ahead.mode, request.mode)) {
            blockers.add(ahead.txId);
          }
        }
        if (blockers.size > 0) {
          const existing = edges.get(request.txId) ?? new Set<number>();
          for (const blocker of blockers) existing.add(blocker);
          edges.set(request.txId, existing);
        }
      }
    }
    return edges;
  }

  private findCycle(): number[] | null {
    const edges = this.waitForEdges();
    const visiting = new Set<number>();
    const done = new Set<number>();
    const stack: number[] = [];

    const visit = (node: number): number[] | null => {
      visiting.add(node);
      stack.push(node);
      for (const next of edges.get(node) ?? []) {
        if (done.has(next)) continue;
        if (visiting.has(next)) {
          return stack.slice(stack.indexOf(next)).concat(next);
        }
        const cycle = visit(next);
        if (cycle) return cycle;
      }
      visiting.delete(node);
      done.add(node);
      stack.pop();
      return null;
    };

    for (const node of edges.keys()) {
      if (!done.has(node)) {
        const cycle = visit(node);
        if (cycle) return cycle;
      }
    }
    return null;
  }

  private abortVictim(victimTxId: number, cycle: number[]): void {
    for (const lock of this.table.values()) {
      for (const request of lock.queue) {
        if (request.txId === victimTxId && !request.settled) {
          request.settled = true;
          lock.queue = lock.queue.filter((queued) => queued !== request);
          request.reject(new DeadlockError(victimTxId, cycle));
          // Removing the victim may unblock queued requests behind it.
          this.grantWaiters();
          return;
        }
      }
    }
  }

  /** Snapshot of the lock table, for monitoring and debugging. */
  snapshot(): Record<string, { holders: Record<string, LockMode>; waiting: number[] }> {
    const out: Record<string, { holders: Record<string, LockMode>; waiting: number[] }> = {};
    for (const [resource, lock] of this.table) {
      out[resource] = {
        holders: Object.fromEntries([...lock.holders].map(([tx, mode]) => [`tx${tx}`, mode])),
        waiting: lock.queue.filter((request) => !request.settled).map((request) => request.txId),
      };
    }
    return out;
  }
}
