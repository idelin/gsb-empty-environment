import type { Clock } from './clock.ts';
import type { Message, NodeId } from './messages.ts';

/** 节点视角的传输层：发消息、收消息。 */
export interface Transport {
  send(to: NodeId, message: Message): void;
  onMessage(handler: (message: Message) => void): void;
}

export interface LogEntry {
  time: number;
  message: Message;
  dropped: boolean;
}

/**
 * 模拟网络：固定时延 + 可配置的单向丢包链路。
 * 用于在测试时钟下构造“直连丢包、代理通畅”等弱网场景。
 */
export class SimulatedNetwork {
  private readonly handlers = new Map<NodeId, (message: Message) => void>();
  private readonly droppedLinks = new Set<string>();
  readonly log: LogEntry[] = [];

  private readonly clock: Clock;
  private readonly latencyMs: number;

  constructor(clock: Clock, latencyMs = 10) {
    this.clock = clock;
    this.latencyMs = latencyMs;
  }

  attach(id: NodeId): Transport {
    return {
      send: (to, message) => this.send(id, to, message),
      onMessage: (handler) => {
        this.handlers.set(id, handler);
      },
    };
  }

  dropLink(from: NodeId, to: NodeId): void {
    this.droppedLinks.add(`${from}->${to}`);
  }

  healLink(from: NodeId, to: NodeId): void {
    this.droppedLinks.delete(`${from}->${to}`);
  }

  dropBidirectional(a: NodeId, b: NodeId): void {
    this.dropLink(a, b);
    this.dropLink(b, a);
  }

  healBidirectional(a: NodeId, b: NodeId): void {
    this.healLink(a, b);
    this.healLink(b, a);
  }

  private send(from: NodeId, to: NodeId, message: Message): void {
    const dropped = this.droppedLinks.has(`${from}->${to}`);
    this.log.push({ time: this.clock.now(), message, dropped });
    if (dropped) return;
    this.clock.setTimeout(() => {
      this.handlers.get(to)?.(message);
    }, this.latencyMs);
  }

  /** 统计已发送（未被丢弃的）某类消息数量，可按收发方过滤。 */
  count(type: Message['type'], from?: NodeId, to?: NodeId): number {
    return this.log.filter(
      (entry) =>
        !entry.dropped &&
        entry.message.type === type &&
        (from === undefined || entry.message.from === from) &&
        (to === undefined || entry.message.to === to),
    ).length;
  }
}
