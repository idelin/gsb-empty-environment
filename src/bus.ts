import { EventLoop } from './eventLoop.ts';
import type { Message } from './types.ts';

export interface Node {
  crashed: boolean;
  receive(from: string, msg: Message): void;
}

/** 消息总线：固定延迟投递；目标节点崩溃时消息丢失（模拟网络/进程故障）。 */
export class Bus {
  private nodes = new Map<string, Node>();
  readonly delivered: Array<{ from: string; to: string; msg: Message }> = [];
  private loop: EventLoop;
  private latency: number;

  constructor(loop: EventLoop, latency = 1) {
    this.loop = loop;
    this.latency = latency;
  }

  register(id: string, node: Node): void {
    this.nodes.set(id, node);
  }

  send(from: string, to: string, msg: Message): void {
    this.loop.schedule(this.latency, () => {
      const node = this.nodes.get(to);
      if (!node || node.crashed) return; // 消息丢失
      this.delivered.push({ from, to, msg });
      node.receive(from, msg);
    });
  }
}
