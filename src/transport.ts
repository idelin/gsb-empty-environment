import type { Message } from './types.ts';

export type MessageHandler = (msg: Message) => Message | void | Promise<Message | void>;

/** 传输层抽象：节点只依赖该接口，便于替换为 TCP/HTTP 实现。 */
export interface Transport {
  bind(address: string, handler: MessageHandler): void | Promise<void>;
  unbind(address: string): void;
  send(address: string, msg: Message): Promise<Message | void>;
}

/** 进程内仿真网络：确定性、零依赖，用于测试与运行样例。 */
export class InMemoryNetwork implements Transport {
  private readonly latencyMs: number;
  private handlers = new Map<string, MessageHandler>();

  constructor(latencyMs = 0) {
    this.latencyMs = latencyMs;
  }

  bind(address: string, handler: MessageHandler): void {
    if (this.handlers.has(address)) {
      throw new Error(`address already bound: ${address}`);
    }
    this.handlers.set(address, handler);
  }

  unbind(address: string): void {
    this.handlers.delete(address);
  }

  async send(address: string, msg: Message): Promise<Message | void> {
    const handler = this.handlers.get(address);
    if (!handler) {
      throw new Error(`peer unreachable: ${address}`);
    }
    if (this.latencyMs > 0) {
      await new Promise((resolve) => setTimeout(resolve, this.latencyMs));
    }
    return handler(msg);
  }

  get size(): number {
    return this.handlers.size;
  }
}
