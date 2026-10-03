import { DeadlockError } from './errors.ts';

export type LockMode = 'shared' | 'exclusive';

export interface LockAuditEvent {
  kind: 'acquire' | 'wait' | 'release';
  txnId: number;
  resource: string;
  mode: LockMode;
  at: number;
}

export interface ReleasedLock {
  resource: string;
  mode: LockMode;
}

interface QueueEntry {
  txnId: number;
  mode: LockMode;
  resolve: () => void;
  reject: (err: Error) => void;
}

interface ResourceLock {
  holders: Map<number, LockMode>;
  queue: QueueEntry[];
}

/**
 * Lock table with shared/exclusive modes, FIFO fair queues, lock upgrades
 * and wait-for-graph deadlock detection. This class knows nothing about
 * transaction phases; the SS2PL discipline (no release before commit/abort)
 * is enforced by the ShrinkingPhaseManager, which is the only sanctioned
 * caller of releaseAll().
 */
export class LockManager {
  private readonly locks = new Map<string, ResourceLock>();
  private readonly heldByTxn = new Map<number, Map<string, LockMode>>();
  private readonly listeners: Array<(event: LockAuditEvent) => void> = [];

  onAudit(listener: (event: LockAuditEvent) => void): void {
    this.listeners.push(listener);
  }

  private emit(kind: LockAuditEvent['kind'], txnId: number, resource: string, mode: LockMode): void {
    const event: LockAuditEvent = { kind, txnId, resource, mode, at: Date.now() };
    for (const listener of this.listeners) listener(event);
  }

  private getOrCreate(resource: string): ResourceLock {
    let lock = this.locks.get(resource);
    if (!lock) {
      lock = { holders: new Map(), queue: [] };
      this.locks.set(resource, lock);
    }
    return lock;
  }

  holdersOf(resource: string): ReadonlyMap<number, LockMode> {
    return this.locks.get(resource)?.holders ?? new Map();
  }

  queueLength(resource: string): number {
    return this.locks.get(resource)?.queue.length ?? 0;
  }

  heldResources(txnId: number): ReadonlyMap<string, LockMode> {
    return this.heldByTxn.get(txnId) ?? new Map();
  }

  private track(txnId: number, resource: string, mode: LockMode): void {
    let held = this.heldByTxn.get(txnId);
    if (!held) {
      held = new Map();
      this.heldByTxn.set(txnId, held);
    }
    held.set(resource, mode);
  }

  private static compatible(mode: LockMode, other: LockMode): boolean {
    return mode === 'shared' && other === 'shared';
  }

  private canGrant(lock: ResourceLock, txnId: number, mode: LockMode): boolean {
    for (const [holderId, holderMode] of lock.holders) {
      if (holderId === txnId) continue;
      if (!LockManager.compatible(mode, holderMode)) return false;
    }
    return true;
  }

  acquire(txnId: number, resource: string, mode: LockMode): Promise<void> {
    const lock = this.getOrCreate(resource);
    const heldMode = lock.holders.get(txnId);
    if (heldMode === 'exclusive' || heldMode === mode) {
      return Promise.resolve();
    }
    const isUpgrade = heldMode === 'shared' && mode === 'exclusive';
    if (lock.queue.length === 0 && this.canGrant(lock, txnId, mode)) {
      lock.holders.set(txnId, mode);
      this.track(txnId, resource, mode);
      this.emit('acquire', txnId, resource, mode);
      return Promise.resolve();
    }
    return new Promise<void>((resolve, reject) => {
      const entry: QueueEntry = { txnId, mode, resolve, reject };
      if (isUpgrade) {
        // Upgrades jump ahead of ordinary waiters (but keep order among
        // themselves) so a reader that only needs a brief write is not
        // starved by readers arriving later.
        const firstNonUpgrade = lock.queue.findIndex(
          (queued) => !(lock.holders.get(queued.txnId) === 'shared' && queued.mode === 'exclusive'),
        );
        if (firstNonUpgrade === -1) lock.queue.push(entry);
        else lock.queue.splice(firstNonUpgrade, 0, entry);
      } else {
        lock.queue.push(entry);
      }
      this.emit('wait', txnId, resource, mode);
      if (this.createsCycle(txnId)) {
        this.removeQueued(txnId, resource);
        reject(new DeadlockError(
          txnId,
          `deadlock detected: transaction ${txnId} waiting for ${mode} lock on ${resource} closes a wait-for cycle`,
        ));
      }
    });
  }

  /**
   * Releases every lock held by the transaction in one shot and grants
   * whatever queued requests become compatible. Only meant to be invoked
   * through the ShrinkingPhaseManager at commit/abort time.
   */
  releaseAll(txnId: number): ReleasedLock[] {
    const held = this.heldByTxn.get(txnId);
    if (!held || held.size === 0) return [];
    const released: ReleasedLock[] = [];
    const touched: string[] = [];
    for (const [resource, mode] of held) {
      const lock = this.locks.get(resource);
      if (lock) {
        lock.holders.delete(txnId);
        touched.push(resource);
      }
      released.push({ resource, mode });
      this.emit('release', txnId, resource, mode);
    }
    this.heldByTxn.delete(txnId);
    for (const resource of touched) this.processQueue(resource);
    return released;
  }

  /** Rejects and removes every queued (not yet granted) request of a transaction. */
  cancelWaiting(txnId: number, error: Error): void {
    for (const [resource, lock] of this.locks) {
      for (let i = lock.queue.length - 1; i >= 0; i--) {
        if (lock.queue[i].txnId === txnId) {
          const [entry] = lock.queue.splice(i, 1);
          entry.reject(error);
        }
      }
      void resource;
    }
  }

  private removeQueued(txnId: number, resource: string): void {
    const lock = this.locks.get(resource);
    if (!lock) return;
    for (let i = lock.queue.length - 1; i >= 0; i--) {
      if (lock.queue[i].txnId === txnId) lock.queue.splice(i, 1);
    }
  }

  private processQueue(resource: string): void {
    const lock = this.locks.get(resource);
    if (!lock) return;
    while (lock.queue.length > 0) {
      const front = lock.queue[0];
      if (!this.canGrant(lock, front.txnId, front.mode)) return;
      lock.queue.shift();
      lock.holders.set(front.txnId, front.mode);
      this.track(front.txnId, resource, front.mode);
      this.emit('acquire', front.txnId, resource, front.mode);
      front.resolve();
    }
  }

  /**
   * Builds the wait-for graph (waiter -> holders and waiters ahead of it in
   * every queue) and checks whether the given transaction can reach itself.
   */
  private createsCycle(start: number): boolean {
    const edges = new Map<number, Set<number>>();
    for (const lock of this.locks.values()) {
      lock.queue.forEach((entry, index) => {
        let targets = edges.get(entry.txnId);
        if (!targets) {
          targets = new Set();
          edges.set(entry.txnId, targets);
        }
        for (const holderId of lock.holders.keys()) {
          if (holderId !== entry.txnId) targets.add(holderId);
        }
        for (let j = 0; j < index; j++) {
          const ahead = lock.queue[j].txnId;
          if (ahead !== entry.txnId) targets.add(ahead);
        }
      });
    }
    const visited = new Set<number>();
    const stack = [...(edges.get(start) ?? [])];
    while (stack.length > 0) {
      const node = stack.pop()!;
      if (node === start) return true;
      if (visited.has(node)) continue;
      visited.add(node);
      for (const next of edges.get(node) ?? []) stack.push(next);
    }
    return false;
  }
}
