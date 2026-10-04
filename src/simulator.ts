import type { LogRecord, OrderEvent, ReceiverLogRecord } from './types.ts';
import { CrashError, FaultInjector } from './faults.ts';
import type { BoundaryPoint, FaultPhase } from './faults.ts';
import { SimulatedDisk } from './disk.ts';
import { Database } from './database.ts';
import { OutboxRelay } from './relay.ts';
import { NotificationReceiver } from './receiver.ts';

export interface SimStats {
  crashes: number;
  /** 每个订单事件被业务应用的次数（不变量：恰好为 1）。 */
  orderApplications: Map<string, number>;
  /** 每个通知在接收方产生业务效果的次数（不变量：恰好为 1）。 */
  notificationEffects: Map<string, number>;
  /** 事件因崩溃/确认丢失而被重复投递的次数。 */
  eventRedeliveries: number;
}

function bump(map: Map<string, number>, key: string): void {
  map.set(key, (map.get(key) ?? 0) + 1);
}

/**
 * 模拟器：事件源以至少一次语义投递订单事件（提交成功才确认，否则重投），
 * 系统处理后由中继派发通知。崩溃 = 丢弃全部易失状态，从两块磁盘日志恢复。
 */
export class Simulator {
  readonly faults = new FaultInjector();
  readonly stats: SimStats = {
    crashes: 0,
    orderApplications: new Map(),
    notificationEffects: new Map(),
    eventRedeliveries: 0,
  };

  private readonly dbDisk = new SimulatedDisk<LogRecord>(this.faults);
  private readonly receiverDisk = new SimulatedDisk<ReceiverLogRecord>(this.faults);
  private unacked: OrderEvent[];
  private db!: Database;
  private relay!: OutboxRelay;
  private receiver!: NotificationReceiver;

  constructor(events: OrderEvent[]) {
    this.unacked = [...events];
    this.boot();
  }

  private boot(): void {
    this.db = new Database(this.dbDisk, (event) => {
      bump(this.stats.orderApplications, event.eventId);
    });
    this.receiver = new NotificationReceiver(this.receiverDisk, (notification) => {
      bump(this.stats.notificationEffects, notification.notificationId);
    });
    this.relay = new OutboxRelay(this.db, this.receiver, this.faults);
  }

  /** 崩溃并重启：易失状态丢失，从持久日志恢复，未确认事件将被重投。 */
  crashAndRestart(): void {
    this.stats.crashes += 1;
    this.faults.disarm();
    this.boot();
  }

  /** 运行直到静止：所有事件已确认且发件箱清空。崩溃会向上抛出 CrashError。 */
  runToQuiescence(maxSteps = 10_000): void {
    for (let step = 0; step < maxSteps; step += 1) {
      let progress = false;
      if (this.unacked.length > 0) {
        const event = this.unacked[0];
        const result = this.db.processOrderEvent(event);
        if (result === 'duplicate') this.stats.eventRedeliveries += 1;
        this.unacked.shift(); // 提交成功后确认；崩溃则保留并重启后重投
        progress = true;
      }
      if (this.relay.dispatchPending() > 0) progress = true;
      if (!progress) return;
    }
    throw new Error('system did not reach quiescence');
  }

  /** 在指定边界注入一次崩溃后继续运行至静止。 */
  runWithCrashAt(point: BoundaryPoint, phase: FaultPhase): void {
    this.faults.arm(point, phase);
    try {
      this.runToQuiescence();
    } catch (error) {
      if (!(error instanceof CrashError)) throw error;
      this.crashAndRestart();
      this.runToQuiescence();
    }
  }

  expectedNotificationId(event: OrderEvent): string {
    return `ntf:${event.eventId}`;
  }

  get database(): Database {
    return this.db;
  }

  get notificationReceiver(): NotificationReceiver {
    return this.receiver;
  }

  get dbLog(): LogRecord[] {
    return this.dbDisk.readAll();
  }
}
