/**
 * 确定性内存模拟网络：支持任意点对点链路开关、网络分区、
 * 消息延迟（虚拟时间）与丢包，便于测试网络分区场景。
 */
import type { Message } from "./types.ts";

interface PendingMessage {
  deliverAt: number;
  seq: number;
  message: Message;
}

export class SimulatedNetwork {
  private connected = new Map<string, boolean>();
  private queue: PendingMessage[] = [];
  private seq = 0;
  private now = 0;
  private handlers = new Map<number, (m: Message) => void>();
  /** 统计：发送/丢弃的消息数，供测试断言无泄漏。 */
  public sentCount = 0;
  public droppedCount = 0;
  public dropRate = 0; // 0..1，配合 seed 的确定性随机
  private rngState = 0x2f6e2b1;
  readonly nodeIds: number[];

  constructor(nodeIds: number[]) {
    this.nodeIds = nodeIds;
    for (const a of nodeIds) {
      for (const b of nodeIds) {
        this.connected.set(this.key(a, b), a !== b);
      }
    }
  }

  private key(a: number, b: number): string {
    return `${a}->${b}`;
  }

  private rand(): number {
    // xorshift32，确定性
    let x = this.rngState;
    x ^= x << 13;
    x ^= x >>> 17;
    x ^= x << 5;
    this.rngState = x >>> 0;
    return this.rngState / 0xffffffff;
  }

  register(nodeId: number, handler: (m: Message) => void): void {
    this.handlers.set(nodeId, handler);
  }

  setLink(from: number, to: number, up: boolean): void {
    this.connected.set(this.key(from, to), up);
  }

  /** 将集群划分为若干连通组，组间链路全部断开。 */
  partition(groups: number[][]): void {
    for (const a of this.nodeIds) {
      for (const b of this.nodeIds) {
        if (a === b) continue;
        const same = groups.some((g) => g.includes(a) && g.includes(b));
        this.setLink(a, b, same);
      }
    }
  }

  healAll(): void {
    for (const a of this.nodeIds) {
      for (const b of this.nodeIds) {
        if (a !== b) this.setLink(a, b, true);
      }
    }
  }

  /** 发送消息；delayMs 为虚拟时间延迟。 */
  send(message: Message, delayMs = 0): void {
    this.sentCount++;
    if (!this.connected.get(this.key(message.from, message.to))) {
      this.droppedCount++;
      return;
    }
    if (this.dropRate > 0 && this.rand() < this.dropRate) {
      this.droppedCount++;
      return;
    }
    this.queue.push({ deliverAt: this.now + delayMs, seq: this.seq++, message });
  }

  /** 推进虚拟时钟并投递所有到期消息，返回投递条数。 */
  flush(advanceMs = 0): number {
    this.now += advanceMs;
    let delivered = 0;
    for (;;) {
      let best = -1;
      for (let i = 0; i < this.queue.length; i++) {
        const p = this.queue[i];
        if (p.deliverAt <= this.now && (best < 0 || p.seq < this.queue[best].seq)) {
          best = i;
        }
      }
      if (best < 0) break;
      const [p] = this.queue.splice(best, 1);
      const handler = this.handlers.get(p.message.to);
      if (handler) {
        handler(p.message);
        delivered++;
      }
    }
    return delivered;
  }

  get pending(): number {
    return this.queue.length;
  }

  get time(): number {
    return this.now;
  }
}
