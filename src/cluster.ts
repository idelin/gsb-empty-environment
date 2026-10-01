import { EventEmitter } from "node:events";
import { MessageBus } from "./messageBus.ts";
import { ClusterNode } from "./node.ts";
import type { ClusterMessage } from "./types.ts";

export interface ClusterOptions {
  /** 平静期时长（毫秒）：期间阻断新流量，超时后物理销毁路由条目。 */
  quiescentMs: number;
}

export interface RouteEvent {
  readonly observer: string;
  readonly peer: string;
}

/**
 * 集群：负责节点装配、广播总线与 RPC 入口。
 *
 * 事件（用于可观测性）：
 *  - "leaving"  某观察者节点把某对等节点移出就绪路由池
 *  - "removed"  某观察者节点物理销毁某对等节点的路由条目
 *  - "gone"     离开节点自身完成下线
 */
export class Cluster {
  readonly quiescentMs: number;
  readonly events = new EventEmitter();

  private readonly bus = new MessageBus();
  private readonly nodes = new Map<string, ClusterNode>();

  constructor(options: ClusterOptions) {
    this.quiescentMs = options.quiescentMs;
  }

  addNode(id: string): ClusterNode {
    if (this.nodes.has(id)) {
      throw new Error(`node "${id}" already exists`);
    }
    const node = new ClusterNode(id, this, {
      onLeaving: (peer) =>
        this.events.emit("leaving", { observer: id, peer } satisfies RouteEvent),
      onRemoved: (peer) =>
        this.events.emit("removed", { observer: id, peer } satisfies RouteEvent),
    });
    for (const existing of this.nodes.values()) {
      existing.pool.addReady(id);
      node.pool.addReady(existing.id);
    }
    this.nodes.set(id, node);
    this.bus.subscribe((message) => node.onMessage(message));
    return node;
  }

  getNode(id: string): ClusterNode | undefined {
    return this.nodes.get(id);
  }

  nodeIds(): string[] {
    return [...this.nodes.keys()];
  }

  broadcast(message: ClusterMessage): void {
    this.bus.broadcast(message);
  }

  notifyGone(id: string): void {
    this.events.emit("gone", { peer: id });
  }

  messageHistory(): readonly ClusterMessage[] {
    return this.bus.history();
  }

  async rpc(fromId: string, toId: string, payload: unknown): Promise<unknown> {
    const from = this.nodes.get(fromId);
    if (!from) {
      throw new Error(`unknown caller node "${fromId}"`);
    }
    return from.sendRpc(toId, payload);
  }

  dispose(): void {
    for (const node of this.nodes.values()) {
      node.halt();
    }
  }
}
