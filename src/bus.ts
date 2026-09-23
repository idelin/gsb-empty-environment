import type { Batch } from './ops.ts';

type Handler = (batch: Batch) => void;

interface Envelope {
  batch: Batch;
  from: string;
  to: string;
}

/**
 * 可控消息总线：
 * - 消息先入队，deliver() 显式投递（测试可精确控制时序）；
 * - 支持分区 / 恢复，分区期间消息滞留队列；
 * - 保留每个发送者的完整日志，断线重连后可按序重放；
 * - 支持注入重复消息，验证接收端幂等。
 */
export class MessageBus {
  private handlers = new Map<string, Handler>();
  private queue: Envelope[] = [];
  private links = new Map<string, boolean>(); // "a|b" -> connected
  private retained = new Map<string, Batch[]>(); // sender -> 全部已发送批次

  subscribe(replicaId: string, handler: Handler): void {
    this.handlers.set(replicaId, handler);
  }

  private linkKey(a: string, b: string): string {
    return [a, b].sort().join('|');
  }

  /** 断开两个副本间的链路（模拟网络分区）。 */
  partition(a: string, b: string): void {
    this.links.set(this.linkKey(a, b), false);
  }

  /** 恢复链路；滞留消息在下次 deliver() 时送达。 */
  heal(a: string, b: string): void {
    this.links.set(this.linkKey(a, b), true);
  }

  private connected(a: string, b: string): boolean {
    return this.links.get(this.linkKey(a, b)) !== false;
  }

  /** 发送一个事务批次：广播给所有订阅者（含发送者自身，即本地回声）。 */
  send(batch: Batch): void {
    const log = this.retained.get(batch.replicaId) ?? [];
    log.push(batch);
    this.retained.set(batch.replicaId, log);
    for (const to of this.handlers.keys()) {
      this.queue.push({ batch, from: batch.replicaId, to });
    }
  }

  /** 投递所有当前可达的消息；不可达的滞留。返回本次投递数。 */
  deliver(): number {
    const pending: Envelope[] = [];
    let delivered = 0;
    for (const env of this.queue) {
      if (this.connected(env.from, env.to)) {
        this.handlers.get(env.to)?.(env.batch);
        delivered++;
      } else {
        pending.push(env);
      }
    }
    this.queue = pending;
    return delivered;
  }

  /** 注入重复消息（模拟 at-least-once 重投）。 */
  injectDuplicate(batch: Batch, to: string): void {
    this.queue.push({ batch, from: batch.replicaId, to });
  }

  /** 断线重连：取回某发送者保留的全部批次，用于重放补偿。 */
  fetchRetained(senderId: string): readonly Batch[] {
    return this.retained.get(senderId) ?? [];
  }

  get pendingCount(): number {
    return this.queue.length;
  }
}
