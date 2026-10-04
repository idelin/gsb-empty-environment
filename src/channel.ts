import { TargetNode } from './target';
import { Entry, Key } from './types';

export interface DeltaMessage {
  key: Key;
  entry: Entry;
}

/**
 * 增量通道：不保证顺序、可能重复投递（乱序 + 重发）。
 * 消息在队列中被随机抽取投递，发送时按概率复制副本。
 */
export class UnreliableChannel {
  private queue: DeltaMessage[] = [];

  constructor(
    private readonly rng: () => number,
    private readonly duplicateRate = 0.35,
  ) {}

  send(msg: DeltaMessage): void {
    this.queue.push(msg);
    if (this.rng() < this.duplicateRate) {
      this.queue.push({ ...msg }); // 重发副本
    }
  }

  get pending(): number {
    return this.queue.length;
  }

  /** 随机抽取一条投递（乱序） */
  deliverOne(target: TargetNode): boolean {
    if (this.queue.length === 0) {
      return false;
    }
    const i = Math.floor(this.rng() * this.queue.length);
    const [msg] = this.queue.splice(i, 1);
    target.apply(msg.key, msg.entry);
    return true;
  }

  /** 切换前排空通道 */
  drain(target: TargetNode): void {
    while (this.deliverOne(target)) {
      /* 清空 */
    }
  }
}
