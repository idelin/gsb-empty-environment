import type { KeyRange } from './keyrange.ts';
import { rangeKey } from './keyrange.ts';

export type LeaseStatus = 'active' | 'revoked';

export interface Lease {
  readonly id: number;
  readonly range: KeyRange;
  readonly nodeId: string;
  /** Monotonic version; children of a split inherit parent epoch + 1. */
  readonly epoch: number;
  /** Lease id this lease was split-inherited from, if any. */
  readonly inheritedFrom: number | null;
  status: LeaseStatus;
}

export class LeaseManager {
  private nextId = 1;
  private leases = new Map<number, Lease>();

  grant(range: KeyRange, nodeId: string, epoch: number, inheritedFrom: number | null = null): Lease {
    const lease: Lease = { id: this.nextId++, range, nodeId, epoch, inheritedFrom, status: 'active' };
    this.leases.set(lease.id, lease);
    return lease;
  }

  revoke(lease: Lease): void {
    if (lease.status !== 'active') return;
    lease.status = 'revoked';
  }

  assertUsable(lease: Lease): void {
    if (lease.status !== 'active') {
      throw new Error(`lease #${lease.id} for ${rangeKey(lease.range)} is ${lease.status}`);
    }
  }
}
