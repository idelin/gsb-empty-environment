import type { ClockFn } from './clocks.ts';

export interface Lease {
  readonly id: string;
  readonly owner: string;
  /** Expiry on the monotonic time base (immune to wall-clock chaos). */
  readonly expiresAtMonoMs: number;
  revoked: boolean;
  revokeReason: string | null;
}

export class LeaseRejectedError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'LeaseRejectedError';
  }
}

/**
 * Tracks active leases. All expiry math uses the monotonic clock so that
 * wall-clock jumps can never extend or shorten a lease's real lifetime.
 */
export class LeaseManager {
  private readonly monotonicNow: ClockFn;
  private readonly leases = new Map<string, Lease>();
  private accepting = true;

  constructor(monotonicNow: ClockFn) {
    this.monotonicNow = monotonicNow;
  }

  /** Reject all new leases (safe mode after a circuit-breaker trip). */
  stopAccepting(): void {
    this.accepting = false;
  }

  get isAccepting(): boolean {
    return this.accepting;
  }

  acquire(id: string, owner: string, ttlMs: number): Lease {
    if (!this.accepting) {
      throw new LeaseRejectedError(
        `lease "${id}" rejected: manager is in safe mode`,
      );
    }
    if (this.leases.has(id)) {
      throw new LeaseRejectedError(`lease "${id}" already exists`);
    }
    const lease: Lease = {
      id,
      owner,
      expiresAtMonoMs: this.monotonicNow() + ttlMs,
      revoked: false,
      revokeReason: null,
    };
    this.leases.set(id, lease);
    return lease;
  }

  /** Voluntarily release a lease. */
  release(id: string): boolean {
    return this.leases.delete(id);
  }

  /**
   * Emergency revocation: force-revoke every active lease immediately.
   * Returns the revoked leases. Idempotent.
   */
  revokeAll(reason: string): Lease[] {
    const revoked: Lease[] = [];
    for (const lease of this.leases.values()) {
      lease.revoked = true;
      lease.revokeReason = reason;
      revoked.push(lease);
    }
    this.leases.clear();
    return revoked;
  }

  /** Drop leases whose monotonic expiry has passed. Returns expired leases. */
  sweepExpired(): Lease[] {
    const now = this.monotonicNow();
    const expired: Lease[] = [];
    for (const [id, lease] of this.leases) {
      if (now >= lease.expiresAtMonoMs) {
        lease.revoked = true;
        lease.revokeReason = 'expired';
        this.leases.delete(id);
        expired.push(lease);
      }
    }
    return expired;
  }

  activeLeases(): Lease[] {
    this.sweepExpired();
    return [...this.leases.values()];
  }

  activeCount(): number {
    return this.activeLeases().length;
  }
}
