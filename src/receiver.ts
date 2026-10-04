import type { Notification } from './types.ts';
import type { ReceiverLogRecord } from './types.ts';
import { Boundary } from './faults.ts';
import type { SimulatedDisk } from './disk.ts';
import type { MessageChannel } from './relay.ts';

/**
 * 通知接收方：按通知标识去重。去重记录先持久化、与业务效果一起原子生效；
 * 崩溃恢复后重放自己的日志重建已处理集合，重复投递被安全忽略。
 */
export class NotificationReceiver implements MessageChannel {
  private processedIds = new Set<string>();
  private duplicateDeliveries = 0;

  private readonly disk: SimulatedDisk<ReceiverLogRecord>;
  private readonly onEffect: (notification: Notification) => void;

  constructor(
    disk: SimulatedDisk<ReceiverLogRecord>,
    onEffect: (notification: Notification) => void = () => {},
  ) {
    this.disk = disk;
    this.onEffect = onEffect;
    this.recover();
  }

  private recover(): void {
    for (const record of this.disk.readAll()) {
      if (record.kind === 'notification-received') {
        this.processedIds.add(record.notificationId);
      }
    }
  }

  deliver(notification: Notification): void {
    if (this.processedIds.has(notification.notificationId)) {
      this.duplicateDeliveries += 1;
      return;
    }
    this.disk.append(
      {
        kind: 'notification-received',
        notificationId: notification.notificationId,
        orderId: notification.orderId,
      },
      Boundary.ReceiverRecord,
      () => {
        this.processedIds.add(notification.notificationId);
        this.onEffect(notification);
      },
    );
  }

  hasProcessed(notificationId: string): boolean {
    return this.processedIds.has(notificationId);
  }

  get duplicates(): number {
    return this.duplicateDeliveries;
  }
}
