export interface LeaseGrant {
  leaseId: string;
  key: string;
  holder: string;
  fenceToken: number;
  ttlMs: number;
  expiresAt: number;
}

export class LeaseExpiredError extends Error {
  constructor(leaseId: string) {
    super(`lease ${leaseId} has expired or does not exist`);
    this.name = 'LeaseExpiredError';
  }
}

export class FencingTokenError extends Error {
  constructor(key: string, token: number, reason: string) {
    super(`write to "${key}" rejected for fence token ${token}: ${reason}`);
    this.name = 'FencingTokenError';
  }
}

export class AcquireTimeoutError extends Error {
  constructor(key: string, holder: string) {
    super(`"${holder}" timed out waiting for lock "${key}"`);
    this.name = 'AcquireTimeoutError';
  }
}

interface LeaseRecord {
  id: string;
  key: string;
  holder: string;
  fenceToken: number;
  ttlMs: number;
  expiresAt: number;
  timer: ReturnType<typeof setTimeout>;
}

interface Waiter {
  holder: string;
  ttlMs: number;
  resolve: (grant: LeaseGrant) => void;
  reject: (err: Error) => void;
  timer?: ReturnType<typeof setTimeout>;
}

/**
 * LeaseLockServer models the strongly-consistent store side of a distributed
 * lock (the role etcd/ZooKeeper play). Locks are bound to leases with a TTL;
 * a lease dies unless the holder keeps renewing it. Every grant carries a
 * monotonically increasing fencing token, and protected writes are validated
 * against the highest token the resource has ever seen.
 */
export class LeaseLockServer {
  private leases = new Map<string, LeaseRecord>();
  private currentLeaseByKey = new Map<string, string>();
  private waitQueues = new Map<string, Waiter[]>();
  private fenceCounters = new Map<string, number>();
  private maxFenceAccepted = new Map<string, number>();
  private store = new Map<string, { value: unknown; fenceToken: number; writer: string }>();
  private leaseSeq = 0;

  /**
   * Acquire the lock for `key`. Resolves immediately if free, otherwise the
   * caller is queued (FIFO) and granted the lock once the current lease is
   * released or expires. `waitTimeoutMs` bounds the queueing time.
   */
  acquire(key: string, holder: string, ttlMs: number, waitTimeoutMs = Infinity): Promise<LeaseGrant> {
    if (!this.currentLeaseByKey.has(key)) {
      return Promise.resolve(this.grant(key, holder, ttlMs));
    }
    return new Promise<LeaseGrant>((resolve, reject) => {
      const waiter: Waiter = { holder, ttlMs, resolve, reject };
      if (Number.isFinite(waitTimeoutMs)) {
        waiter.timer = setTimeout(() => {
          const queue = this.waitQueues.get(key);
          if (queue) {
            const idx = queue.indexOf(waiter);
            if (idx >= 0) queue.splice(idx, 1);
          }
          reject(new AcquireTimeoutError(key, holder));
        }, waitTimeoutMs);
      }
      const queue = this.waitQueues.get(key) ?? [];
      queue.push(waiter);
      this.waitQueues.set(key, queue);
    });
  }

  /** Extend a live lease by its original TTL. Throws if the lease is gone. */
  renew(leaseId: string): LeaseGrant {
    const lease = this.leases.get(leaseId);
    if (!lease) throw new LeaseExpiredError(leaseId);
    clearTimeout(lease.timer);
    lease.expiresAt = Date.now() + lease.ttlMs;
    lease.timer = setTimeout(() => this.expire(lease.id), lease.ttlMs);
    return this.snapshot(lease);
  }

  /** Voluntarily give up a lease. Returns false if it was already gone. */
  release(leaseId: string): boolean {
    const lease = this.leases.get(leaseId);
    if (!lease) return false;
    this.leases.delete(leaseId);
    clearTimeout(lease.timer);
    this.handoff(lease.key);
    return true;
  }

  /**
   * Fence-protected write to the shared resource guarded by `key`.
   * The token must belong to the current live lease and must be greater than
   * any token ever accepted for this key, so a crashed-then-recovered holder
   * can never overwrite a newer owner's data.
   */
  write(key: string, fenceToken: number, value: unknown): void {
    const currentId = this.currentLeaseByKey.get(key);
    const lease = currentId ? this.leases.get(currentId) : undefined;
    if (!lease || lease.fenceToken !== fenceToken) {
      throw new FencingTokenError(key, fenceToken, 'token does not belong to the current lock holder');
    }
    const maxSeen = this.maxFenceAccepted.get(key) ?? 0;
    if (fenceToken <= maxSeen) {
      throw new FencingTokenError(key, fenceToken, `stale token, resource already fenced at ${maxSeen}`);
    }
    this.maxFenceAccepted.set(key, fenceToken);
    this.store.set(key, { value, fenceToken, writer: lease.holder });
  }

  read(key: string): unknown {
    return this.store.get(key)?.value;
  }

  isLocked(key: string): boolean {
    return this.currentLeaseByKey.has(key);
  }

  currentHolder(key: string): string | undefined {
    const id = this.currentLeaseByKey.get(key);
    return id ? this.leases.get(id)?.holder : undefined;
  }

  private grant(key: string, holder: string, ttlMs: number): LeaseGrant {
    const fenceToken = (this.fenceCounters.get(key) ?? 0) + 1;
    this.fenceCounters.set(key, fenceToken);
    const id = `lease-${++this.leaseSeq}`;
    const lease: LeaseRecord = {
      id,
      key,
      holder,
      fenceToken,
      ttlMs,
      expiresAt: Date.now() + ttlMs,
      timer: setTimeout(() => this.expire(id), ttlMs),
    };
    this.leases.set(id, lease);
    this.currentLeaseByKey.set(key, id);
    return this.snapshot(lease);
  }

  private expire(leaseId: string): void {
    const lease = this.leases.get(leaseId);
    if (!lease) return;
    this.leases.delete(leaseId);
    this.handoff(lease.key);
  }

  private handoff(key: string): void {
    this.currentLeaseByKey.delete(key);
    const queue = this.waitQueues.get(key);
    if (!queue || queue.length === 0) return;
    const next = queue.shift()!;
    if (next.timer) clearTimeout(next.timer);
    next.resolve(this.grant(key, next.holder, next.ttlMs));
  }

  private snapshot(lease: LeaseRecord): LeaseGrant {
    return {
      leaseId: lease.id,
      key: lease.key,
      holder: lease.holder,
      fenceToken: lease.fenceToken,
      ttlMs: lease.ttlMs,
      expiresAt: lease.expiresAt,
    };
  }
}
