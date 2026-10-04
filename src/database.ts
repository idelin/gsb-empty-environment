import type { LogRecord, Notification, Order, OrderEvent } from './types.ts';
import { Boundary } from './faults.ts';
import type { SimulatedDisk } from './disk.ts';

export interface OutboxEntry {
  notification: Notification;
  sent: boolean;
}

export type ProcessResult = 'applied' | 'duplicate';

/**
 * 订单库：以 WAL 实现内存事务。订单更新与待发通知登记在同一事务中
 * 先写意图记录、最后后以一条 commit 记录原子生效；崩溃后重放日志，
 * 只应用已提交事务，未提交的意图记录被丢弃。
 */
export class Database {
  private orders = new Map<string, Order>();
  private appliedEventIds = new Set<string>();
  private outbox = new Map<string, OutboxEntry>();
  private txnCounter = 0;

  private readonly disk: SimulatedDisk<LogRecord>;
  private readonly onOrderApplied: (event: OrderEvent) => void;

  constructor(
    disk: SimulatedDisk<LogRecord>,
    onOrderApplied: (event: OrderEvent) => void = () => {},
  ) {
    this.disk = disk;
    this.onOrderApplied = onOrderApplied;
    this.recover();
  }

  private recover(): void {
    const intents = new Map<string, LogRecord[]>();
    for (const record of this.disk.readAll()) {
      switch (record.kind) {
        case 'txn-begin':
          this.txnCounter += 1;
          intents.set(record.txnId, []);
          break;
        case 'order-upsert':
        case 'outbox-add':
          intents.get(record.txnId)?.push(record);
          break;
        case 'txn-commit':
          for (const intent of intents.get(record.txnId) ?? []) {
            this.applyCommitted(intent, true);
          }
          intents.delete(record.txnId);
          break;
        case 'notification-sent': {
          const entry = this.outbox.get(record.notificationId);
          if (entry) entry.sent = true;
          break;
        }
      }
    }
  }

  private applyCommitted(record: LogRecord, replay: boolean): void {
    if (record.kind === 'order-upsert') {
      const { event } = record;
      const existing = this.orders.get(event.orderId);
      this.orders.set(event.orderId, {
        orderId: event.orderId,
        status: event.status,
        amount: event.amount,
        version: (existing?.version ?? 0) + 1,
      });
      this.appliedEventIds.add(event.eventId);
      if (!replay) this.onOrderApplied(event);
    } else if (record.kind === 'outbox-add') {
      this.outbox.set(record.notification.notificationId, {
        notification: record.notification,
        sent: false,
      });
    }
  }

  /** 处理订单事件（可重复投递）。返回 applied 或 duplicate（幂等跳过）。 */
  processOrderEvent(event: OrderEvent): ProcessResult {
    if (this.appliedEventIds.has(event.eventId)) {
      return 'duplicate';
    }
    const txnId = `txn-${++this.txnCounter}`;
    const notification: Notification = {
      notificationId: `ntf:${event.eventId}`,
      orderId: event.orderId,
      eventId: event.eventId,
      payload: `order ${event.orderId} -> ${event.status}`,
    };
    this.disk.append({ kind: 'txn-begin', txnId, eventId: event.eventId }, Boundary.TxnBegin);
    this.disk.append({ kind: 'order-upsert', txnId, event }, Boundary.TxnOrderUpsert);
    this.disk.append({ kind: 'outbox-add', txnId, notification }, Boundary.TxnOutboxAdd);
    // commit 是原子点：落盘与内存应用一起成功或一起丢失。
    this.disk.append({ kind: 'txn-commit', txnId }, Boundary.TxnCommit, () => {
      this.applyCommitted({ kind: 'order-upsert', txnId, event }, false);
      this.applyCommitted({ kind: 'outbox-add', txnId, notification }, false);
    });
    return 'applied';
  }

  pendingNotifications(): Notification[] {
    return [...this.outbox.values()]
      .filter((entry) => !entry.sent)
      .map((entry) => entry.notification);
  }

  /** 通知已交付的确认标记；该确认允许丢失（丢失后会重投，由接收方去重）。 */
  markSent(notificationId: string): void {
    this.disk.append({ kind: 'notification-sent', notificationId }, Boundary.RelayMarkSent, () => {
      const entry = this.outbox.get(notificationId);
      if (entry) entry.sent = true;
    });
  }

  getOrder(orderId: string): Order | undefined {
    const order = this.orders.get(orderId);
    return order ? { ...order } : undefined;
  }

  outboxEntries(): OutboxEntry[] {
    return [...this.outbox.values()].map((entry) => ({
      notification: entry.notification,
      sent: entry.sent,
    }));
  }
}
