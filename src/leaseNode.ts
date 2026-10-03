import { LamportClock } from './logicalClock.ts';
import type { LeaseArbiter, InboundRequest } from './arbiter.ts';
import type { LeaseEvent, RequestOp } from './events.ts';

/**
 * A distributed node competing for leases. Each node owns a Lamport clock;
 * every outbound request is stamped with it so the arbiter (and the audit
 * trail) can order events causally across nodes.
 */
export class LeaseNode {
  readonly id: string;
  private readonly clock = new LamportClock();

  constructor(id: string) {
    this.id = id;
  }

  /** Send a request to the arbiter, arriving at `arriveMs` wall-clock time. */
  send(
    arbiter: LeaseArbiter,
    op: RequestOp,
    leaseId: string,
    arriveMs: number,
    extra: Partial<Pick<InboundRequest, 'ttlMs' | 'toNodeId'>> = {},
  ): LeaseEvent {
    const senderLamport = this.clock.tick();
    return arbiter.submit({
      op,
      leaseId,
      nodeId: this.id,
      senderLamport,
      arriveMs,
      ...extra,
    });
  }

  /** Merge the clock after observing an arbiter response event. */
  observe(event: LeaseEvent): void {
    this.clock.merge(event.lamport);
  }
}
