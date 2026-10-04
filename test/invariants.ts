import assert from 'node:assert/strict';
import type { OrderEvent } from '../src/types.ts';
import type { Simulator } from '../src/simulator.ts';

/** 从订单库日志重建已提交事务，验证原子性与恢复正确性。 */
export function assertInvariants(sim: Simulator, events: OrderEvent[]): void {
  // 1. 每个订单事件恰好被业务应用一次（可重复投递下幂等）。
  for (const event of events) {
    assert.equal(
      sim.stats.orderApplications.get(event.eventId),
      1,
      `order event ${event.eventId} must be applied exactly once`,
    );
  }

  // 2. 每个通知在接收方恰好产生一次业务效果（按通知标识去重）。
  for (const event of events) {
    const ntfId = sim.expectedNotificationId(event);
    assert.equal(
      sim.stats.notificationEffects.get(ntfId),
      1,
      `notification ${ntfId} must take effect exactly once`,
    );
    assert.ok(
      sim.notificationReceiver.hasProcessed(ntfId),
      `notification ${ntfId} must not be permanently lost`,
    );
  }

  // 3. 原子性：每个已提交事务同时包含订单更新与通知登记，二者都不缺。
  const intents = new Map<string, { upsert: number; outbox: number }>();
  const committedTxns = new Set<string>();
  const sentMarkers = new Set<string>();
  for (const record of sim.dbLog) {
    switch (record.kind) {
      case 'txn-begin':
        intents.set(record.txnId, { upsert: 0, outbox: 0 });
        break;
      case 'order-upsert':
        intents.get(record.txnId)!.upsert += 1;
        break;
      case 'outbox-add':
        intents.get(record.txnId)!.outbox += 1;
        break;
      case 'txn-commit':
        committedTxns.add(record.txnId);
        break;
      case 'notification-sent':
        sentMarkers.add(record.notificationId);
        break;
    }
  }
  for (const txnId of committedTxns) {
    const intent = intents.get(txnId)!;
    assert.equal(intent.upsert, 1, `committed ${txnId} must carry exactly one order update`);
    assert.equal(intent.outbox, 1, `committed ${txnId} must carry exactly one outbox entry`);
  }

  // 4. 恢复后的发件箱与已提交事务一致，且静止时全部通知均已确认发送。
  const entries = sim.database.outboxEntries();
  assert.equal(entries.length, events.length, 'outbox must hold one entry per committed event');
  for (const entry of entries) {
    assert.ok(entry.sent, `outbox entry ${entry.notification.notificationId} must be sent`);
    assert.ok(sentMarkers.has(entry.notification.notificationId));
  }

  // 5. 订单终态与最后提交的事件一致。
  const lastByOrder = new Map<string, OrderEvent>();
  for (const event of events) lastByOrder.set(event.orderId, event);
  for (const [orderId, event] of lastByOrder) {
    const order = sim.database.getOrder(orderId);
    assert.ok(order, `order ${orderId} must exist`);
    assert.equal(order.status, event.status);
    assert.equal(order.amount, event.amount);
  }
}
