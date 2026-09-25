export type Priority = 'polite' | 'urgent';

/** Authoritative state of a status entity (e.g. an upload task). */
export interface StatusEntity {
  id: string;
  /** Monotonic version; a message older than this is stale. */
  version: number;
  /** Retracted entities invalidate all of their queued messages. */
  retracted: boolean;
}

export interface StatusMessage {
  id: string;
  entityId: string;
  /** Entity version this message reflects. */
  version: number;
  /** Idempotency key for the delivery acknowledgement. */
  ackId: string;
  priority: Priority;
  text: string;
  /** Time-to-live after enqueue; expired messages are never announced. */
  ttlMs: number;
  /** Messages sharing a mergeKey coalesce; the newest wins. */
  mergeKey?: string;
  /** Owning component; unmounting it drops the message. */
  ownerId?: string;
  /** Stamped by the announcer on enqueue. */
  enqueuedAt?: number;
}

export type DropReason =
  | 'expired'
  | 'entity-retracted'
  | 'stale-version'
  | 'cancelled'
  | 'merged'
  | 'owner-unmounted';
