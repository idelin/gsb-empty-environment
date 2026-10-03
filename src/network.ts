import type { Message, MessageType } from './types.ts';

/**
 * 离散 tick 网络:消息按 deliverTick 排队投递;
 * 支持对节点注入/解除网络黑洞(双向丢弃)。
 */
export class Network {
  private queue: Message[] = [];
  private readonly isolated = new Set<string>();
  private nextId = 1;
  private readonly delay: (type: MessageType) => number;
  private readonly onDrop: (msg: Message) => void;
  private readonly now: () => number;

  constructor(
    delay: (type: MessageType) => number,
    onDrop: (msg: Message) => void,
    now: () => number,
  ) {
    this.delay = delay;
    this.onDrop = onDrop;
    this.now = now;
  }

  send(type: MessageType, from: string, to: string, term: number, reason?: string): Message | null {
    const sendTick = this.now();
    const msg: Message = {
      id: this.nextId++,
      type,
      from,
      to,
      term,
      sendTick,
      deliverTick: sendTick + this.delay(type),
      reason,
    };
    if (this.isolated.has(from) || this.isolated.has(to)) {
      this.onDrop(msg);
      return null;
    }
    this.queue.push(msg);
    return msg;
  }

  /** 取出所有 deliverTick <= tick 的消息(按发送顺序)。 */
  takeDue(tick: number): Message[] {
    const due = this.queue.filter((m) => m.deliverTick <= tick);
    this.queue = this.queue.filter((m) => m.deliverTick > tick);
    return due.sort((a, b) => a.id - b.id);
  }

  isolate(nodeId: string): void {
    this.isolated.add(nodeId);
  }

  heal(nodeId: string): void {
    this.isolated.delete(nodeId);
  }

  isIsolated(nodeId: string): boolean {
    return this.isolated.has(nodeId);
  }
}
