import { LogRecord, Notification, TransactionLog } from './log';

export interface OrderEvent {
  eventId: string;
  orderId: string;
  /** Non-idempotent effect (adds to the order total) so double application is detectable. */
  amount: number;
}

/**
 * Order service using the transactional outbox pattern:
 * the order update, the event dedup marker and the outbox notification
 * are written in ONE transaction (BEGIN..COMMIT) on the WAL.
 */
export class OrderSystem {
  // ---- volatile state (lost on crash, rebuilt by recover) ----
  private orders = new Map<string, number>();
  private appliedEvents = new Set<string>();
  private pendingOutbox = new Map<string, Notification>();
  private nextTxId = 1;

  // ---- run-level instrumentation (survives crashes on purpose) ----
  /** Number of order updates applied on the live (non-recovery) path. */
  businessApplies = 0;

  constructor(private readonly log: TransactionLog) {}

  /** Idempotent: duplicate deliveries of the same event are no-ops. */
  processEvent(event: OrderEvent): void {
    if (this.appliedEvents.has(event.eventId)) return;

    const txId = this.nextTxId++;
    this.log.append({ kind: 'BEGIN', txId });
    this.log.append({ kind: 'ORDER_APPLY', txId, orderId: event.orderId, amount: event.amount });
    this.log.append({ kind: 'EVENT_MARK', txId, eventId: event.eventId });
    this.log.append({
      kind: 'OUTBOX_ADD',
      txId,
      notification: {
        notificationId: `ntf:${event.eventId}`,
        eventId: event.eventId,
        orderId: event.orderId,
        amount: event.amount,
      },
    });
    this.log.append({ kind: 'COMMIT', txId });

    // Volatile apply, only after the commit record is durable.
    this.applyOrder(event.orderId, event.amount);
    this.appliedEvents.add(event.eventId);
    this.pendingOutbox.set(`ntf:${event.eventId}`, {
      notificationId: `ntf:${event.eventId}`,
      eventId: event.eventId,
      orderId: event.orderId,
      amount: event.amount,
    });
    this.businessApplies++;
  }

  /** At-least-once dispatch: unacked notifications are (re)sent. */
  dispatchPending(channel: Channel, receiver: Receiver): void {
    const pending = Array.from(this.pendingOutbox.values());
    for (const notification of pending) {
      const acked = channel.deliver(notification, receiver);
      if (acked) {
        this.log.append({ kind: 'OUTBOX_ACK', notificationId: notification.notificationId });
        this.pendingOutbox.delete(notification.notificationId);
      }
    }
  }

  /** Crash: every piece of volatile state is gone. */
  crashAndRecover(): void {
    this.orders.clear();
    this.appliedEvents.clear();
    this.pendingOutbox.clear();
    this.recover();
  }

  /** Redo committed transactions; uncommitted records are rolled back. */
  private recover(): void {
    const committed = new Set<number>();
    let maxTxId = 0;
    for (const record of this.log.replay()) {
      if (record.kind === 'COMMIT') committed.add(record.txId);
      if ('txId' in record) maxTxId = Math.max(maxTxId, record.txId);
    }

    const acked = new Set<string>();
    for (const record of this.log.replay()) {
      switch (record.kind) {
        case 'ORDER_APPLY':
          if (committed.has(record.txId)) this.applyOrder(record.orderId, record.amount);
          break;
        case 'EVENT_MARK':
          if (committed.has(record.txId)) this.appliedEvents.add(record.eventId);
          break;
        case 'OUTBOX_ADD':
          if (committed.has(record.txId)) {
            this.pendingOutbox.set(record.notification.notificationId, record.notification);
          }
          break;
        case 'OUTBOX_ACK':
          acked.add(record.notificationId);
          break;
      }
    }
    for (const id of acked) this.pendingOutbox.delete(id);
    this.nextTxId = maxTxId + 1;
  }

  private applyOrder(orderId: string, amount: number): void {
    this.orders.set(orderId, (this.orders.get(orderId) || 0) + amount);
  }

  orderTotal(orderId: string): number {
    return this.orders.get(orderId) || 0;
  }

  appliedEventCount(): number {
    return this.appliedEvents.size;
  }

  pendingCount(): number {
    return this.pendingOutbox.size;
  }
}

/** Receiving side: deduplicates by notification id, exactly-once effect. */
export class Receiver {
  private seen = new Set<string>();
  readonly applied: Notification[] = [];

  receive(notification: Notification): void {
    if (this.seen.has(notification.notificationId)) return;
    this.seen.add(notification.notificationId);
    this.applied.push(notification);
  }
}

/** Unreliable channel: deliveries and acks can be dropped on demand. */
export class Channel {
  private attempt = 0;
  private dropDeliveryAt = new Set<number>();
  private dropAckAt = new Set<number>();

  dropDeliveryOnAttempts(...attempts: number[]): void {
    for (const a of attempts) this.dropDeliveryAt.add(a);
  }

  dropAckOnAttempts(...attempts: number[]): void {
    for (const a of attempts) this.dropAckAt.add(a);
  }

  /** Returns true iff the sender may consider the notification acknowledged. */
  deliver(notification: Notification, receiver: Receiver): boolean {
    this.attempt++;
    if (this.dropDeliveryAt.delete(this.attempt)) return false; // lost in transit
    receiver.receive(notification);
    if (this.dropAckAt.delete(this.attempt)) return false; // ack lost on the way back
    return true;
  }
}
