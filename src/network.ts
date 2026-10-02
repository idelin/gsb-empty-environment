import type { Message, TransferMessage } from './messages.ts';

const sleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));

/**
 * 进程间异步消息网络。
 * 每条有向信道 (from -> to) 严格 FIFO 投递，投递延迟随机，
 * 用于模拟真实网络中"在途消息"的存在。
 */
export class Network {
  private queues = new Map<string, Message[]>();
  private pumping = new Set<string>();
  /** 当前仍在网络中（未投递）的转账消息，用于全局守恒审计。 */
  readonly inFlight = new Set<TransferMessage>();

  private readonly deliver: (msg: Message) => void;
  private readonly latency: () => number;
  private readonly onDeliver?: (msg: Message) => void;

  constructor(
    deliver: (msg: Message) => void,
    latency: () => number,
    onDeliver?: (msg: Message) => void,
  ) {
    this.deliver = deliver;
    this.latency = latency;
    this.onDeliver = onDeliver;
  }

  private key(from: string, to: string): string {
    return `${from}->${to}`;
  }

  /** 当前在途消息总数（含 Marker 与转账）。 */
  get inFlightCount(): number {
    let n = 0;
    for (const q of this.queues.values()) n += q.length;
    return n;
  }

  send(msg: Message): void {
    const key = this.key(msg.from, msg.to);
    let q = this.queues.get(key);
    if (!q) {
      q = [];
      this.queues.set(key, q);
    }
    q.push(msg);
    if (msg.kind === 'transfer') this.inFlight.add(msg);
    if (!this.pumping.has(key)) void this.pump(key);
  }

  /** 每条信道一个串行泵，保证同信道消息严格按发送顺序投递。 */
  private async pump(key: string): Promise<void> {
    this.pumping.add(key);
    const q = this.queues.get(key)!;
    while (q.length > 0) {
      const msg = q.shift()!;
      await sleep(this.latency());
      if (msg.kind === 'transfer') this.inFlight.delete(msg);
      this.deliver(msg);
      this.onDeliver?.(msg);
    }
    this.pumping.delete(key);
  }

  /** 等待网络排空（所有在途消息投递完毕）。 */
  async drain(): Promise<void> {
    while (this.inFlightCount > 0) await sleep(1);
  }
}
