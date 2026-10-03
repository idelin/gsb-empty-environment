import type { Message } from './messages.ts';

export type DeliveryHandler = (msg: Message, tick: number) => void;

/**
 * 确定性网络模拟器：
 * - send() 入队，下一个 tick 的 deliver() 统一投递（单 tick 延迟）；
 * - partition(a, b) 切断两组之间的所有链路（双向丢包）；
 * - heal() 恢复全部链路。
 */
export class SimulatedNetwork {
  private blockedPairs = new Set<string>();
  private queue: Message[] = [];
  private handlers = new Map<number, DeliveryHandler>();
  readonly deliveredByKind = new Map<string, number>();
  dropped = 0;

  register(id: number, handler: DeliveryHandler): void {
    this.handlers.set(id, handler);
  }

  private static key(a: number, b: number): string {
    return a < b ? `${a}|${b}` : `${b}|${a}`;
  }

  partition(groupA: number[], groupB: number[]): void {
    for (const a of groupA) {
      for (const b of groupB) {
        this.blockedPairs.add(SimulatedNetwork.key(a, b));
      }
    }
  }

  heal(): void {
    this.blockedPairs.clear();
  }

  isOpen(a: number, b: number): boolean {
    return !this.blockedPairs.has(SimulatedNetwork.key(a, b));
  }

  send(msg: Message): void {
    this.queue.push(msg);
  }

  /** 投递上一 tick 入队的所有消息，返回成功投递数。 */
  deliver(tick: number): number {
    const batch = this.queue;
    this.queue = [];
    let delivered = 0;
    for (const msg of batch) {
      const handler = this.isOpen(msg.from, msg.to) ? this.handlers.get(msg.to) : undefined;
      if (!handler) {
        this.dropped++;
        continue;
      }
      this.deliveredByKind.set(msg.kind, (this.deliveredByKind.get(msg.kind) ?? 0) + 1);
      delivered++;
      handler(msg, tick);
    }
    return delivered;
  }

  pending(): number {
    return this.queue.length;
  }
}
