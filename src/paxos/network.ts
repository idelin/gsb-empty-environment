import { AtomicCell } from '../atomic.ts';
import { LockFreeQueue } from '../lockfree/lockFreeQueue.ts';
import type { Message } from './types.ts';

/**
 * 确定性模拟网络：每个节点一个无锁 MPSC 收件箱，
 * 支持网络分区（按有向边丢弃）与消息类型统计（CAS 计数器加速）。
 * 消息不经过任何全局锁；发送即入队，由 Cluster.pump() 确定性投递。
 */
export class Network {
  #inboxes = new Map<string, LockFreeQueue<Message>>();
  #blocked = new Set<string>();
  #stats = new Map<string, AtomicCell<number>>();
  #dropped = new AtomicCell(0);

  register(id: string): void {
    if (this.#inboxes.has(id)) throw new Error(`duplicate node id: ${id}`);
    this.#inboxes.set(id, new LockFreeQueue());
  }

  nodeIds(): string[] {
    return [...this.#inboxes.keys()];
  }

  send(msg: Message): boolean {
    if (this.#blocked.has(`${msg.from}->${msg.to}`)) {
      this.#dropped.update((n) => n + 1);
      return false;
    }
    const inbox = this.#inboxes.get(msg.to);
    if (!inbox) throw new Error(`unknown node: ${msg.to}`);
    inbox.enqueue(msg);
    this.#bump(msg.type);
    return true;
  }

  dequeue(id: string): Message | undefined {
    return this.#inboxes.get(id)?.dequeue();
  }

  /** 切断两组节点之间的双向链路（模拟网络分区）。 */
  partition(groupA: string[], groupB: string[]): void {
    for (const a of groupA) {
      for (const b of groupB) {
        this.#blocked.add(`${a}->${b}`);
        this.#blocked.add(`${b}->${a}`);
      }
    }
  }

  heal(): void {
    this.#blocked.clear();
  }

  get isPartitioned(): boolean {
    return this.#blocked.size > 0;
  }

  /** 全网在途消息总数（泄漏断言用）。 */
  pendingCount(): number {
    let total = 0;
    for (const inbox of this.#inboxes.values()) total += inbox.size;
    return total;
  }

  get droppedCount(): number {
    return this.#dropped.load();
  }

  /** 已发送（未丢弃）的某类消息数量。 */
  stat(type: Message['type']): number {
    return this.#stats.get(type)?.load() ?? 0;
  }

  #bump(type: string): void {
    let cell = this.#stats.get(type);
    if (!cell) {
      cell = new AtomicCell(0);
      this.#stats.set(type, cell);
    }
    cell.update((n) => n + 1);
  }
}
