/** 传输层抽象：面向帧的可靠有序投递（协议本身不处理丢包重传） */
export interface Transport {
  send(data: Buffer): void;
  onMessage(handler: (data: Buffer) => void): void;
}

/** 内存双工链路，可注入单向时延，用于示例与测试 */
export class MemoryDuplex implements Transport {
  private peer: MemoryDuplex | null = null;
  private handler: ((data: Buffer) => void) | null = null;
  private readonly latencyMs: number;

  constructor(latencyMs = 0) {
    this.latencyMs = latencyMs;
  }

  static pair(latencyMs = 0): [MemoryDuplex, MemoryDuplex] {
    const a = new MemoryDuplex(latencyMs);
    const b = new MemoryDuplex(latencyMs);
    a.peer = b;
    b.peer = a;
    return [a, b];
  }

  send(data: Buffer): void {
    const peer = this.peer;
    if (!peer) throw new Error("transport not paired");
    const deliver = () => peer.handler?.(data);
    if (this.latencyMs > 0) setTimeout(deliver, this.latencyMs);
    else queueMicrotask(deliver);
  }

  onMessage(handler: (data: Buffer) => void): void {
    this.handler = handler;
  }
}
