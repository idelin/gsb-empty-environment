/** All event kinds recorded by the audit trail. */
export type LeaseEventType =
  | 'LEASE_REQUESTED'      // an acquire/renew/transfer/release request arrived at the arbiter
  | 'LEASE_GRANTED'        // arbiter granted the lease to a node
  | 'LEASE_RENEWED'        // holder extended the lease
  | 'LEASE_TRANSFERRED'    // holder handed the lease to another node
  | 'LEASE_RELEASED'       // holder explicitly released the lease
  | 'LEASE_EXPIRED'        // lease TTL elapsed without renewal
  | 'LEASE_REJECTED'       // arbiter rejected a request (lock held, not holder, ...)
  | 'CONTENTION_DETECTED'  // >=2 acquire requests for the same lease inside the contention window
  | 'LOCK_STATE_CHANGED';  // arbiter internal lock state machine transition

export type RequestOp = 'acquire' | 'renew' | 'transfer' | 'release';

export type LockState = 'UNLOCKED' | 'LOCKED';

export interface LeaseEvent {
  /** Globally unique, monotonically assigned by the trail: evt-1, evt-2, ... */
  id: string;
  /** Trail-local sequence number (total arrival order at the recorder). */
  seq: number;
  /** Lamport logical timestamp at the moment the event was recorded. */
  lamport: number;
  /** Wall-clock time in milliseconds (float, sub-ms precision). */
  wallClockMs: number;
  type: LeaseEventType;
  leaseId: string;
  /** Node that triggered the event, if any (absent for arbiter-internal events). */
  nodeId?: string;
  /** Ids of events that directly caused this one (causal evidence edges). */
  causedBy: string[];
  /** Type-specific payload, kept structured for machine analysis. */
  details: Record<string, unknown>;
}

/** Payload for events recorded by the trail (id/seq are assigned by the trail). */
export type NewLeaseEvent = Omit<LeaseEvent, 'id' | 'seq'>;
