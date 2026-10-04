import type { Notification } from './types.ts';
import { Boundary } from './faults.ts';
import type { FaultInjector } from './faults.ts';
import type { Database } from './database.ts';

export interface MessageChannel {
  deliver(notification: Notification): void;
}

/**
 * 发件箱中继：把待发通知发布到消息通道，然后持久化“已发送”确认。
 * 确认可能丢失（崩溃发生在 publish 之后、markSent 之前/之时），
 * 恢复后通知会再次发布——至少一次交付，由接收方去重。
 */
export class OutboxRelay {
  private readonly db: Database;
  private readonly channel: MessageChannel;
  private readonly faults: FaultInjector;

  constructor(db: Database, channel: MessageChannel, faults: FaultInjector) {
    this.db = db;
    this.channel = channel;
    this.faults = faults;
  }

  dispatchPending(): number {
    let dispatched = 0;
    for (const notification of this.db.pendingNotifications()) {
      this.faults.check(Boundary.RelayPublish, 'before');
      this.channel.deliver(notification);
      this.faults.check(Boundary.RelayPublish, 'after');
      this.db.markSent(notification.notificationId);
      dispatched += 1;
    }
    return dispatched;
  }
}
