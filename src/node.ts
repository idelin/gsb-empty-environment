import type { Cluster } from "./cluster.ts";
import { NodeNotFoundError } from "./errors.ts";
import { RoutingPool, type RoutingPoolEvents } from "./routingPool.ts";
import type { ClusterMessage, NodeLifecycle } from "./types.ts";

export interface RpcRequest {
  readonly from: string;
  readonly payload: unknown;
}

export type RpcHandler = (request: RpcRequest) => unknown | Promise<unknown>;

/**
 * 集群节点。生命周期：active -> leaving -> gone。
 */
export class ClusterNode {
  readonly id: string;
  lifecycle: NodeLifecycle = "active";
  readonly pool: RoutingPool;

  private handler: RpcHandler;
  private readonly cluster: Cluster;
  private goneTimer: ReturnType<typeof setTimeout> | undefined;

  constructor(id: string, cluster: Cluster, poolEvents: RoutingPoolEvents = {}) {
    this.id = id;
    this.cluster = cluster;
    this.pool = new RoutingPool([], poolEvents);
    this.handler = (request) => ({ echoed: request.payload, servedBy: this.id });
  }

  setRpcHandler(handler: RpcHandler): void {
    this.handler = handler;
  }

  /**
   * 主动下线：进入 Leaving 状态，广播离开意向，并启动自身的
   * 平静期定时器（超时后物理下线）。重复调用幂等。
   */
  initiateLeave(): boolean {
    if (this.lifecycle !== "active") {
      return false;
    }
    this.lifecycle = "leaving";
    this.cluster.broadcast({
      type: "leave-intent",
      nodeId: this.id,
      quiescentMs: this.cluster.quiescentMs,
      sentAt: Date.now(),
    });
    this.goneTimer = setTimeout(
      () => this.finalizeDeparture(),
      this.cluster.quiescentMs,
    );
    return true;
  }

  private finalizeDeparture(): void {
    if (this.lifecycle === "gone") {
      return;
    }
    this.lifecycle = "gone";
    this.cluster.broadcast({
      type: "leave-complete",
      nodeId: this.id,
      sentAt: Date.now(),
    });
    this.cluster.notifyGone(this.id);
  }

  onMessage(message: ClusterMessage): void {
    if (message.nodeId === this.id) {
      return;
    }
    if (message.type === "leave-intent") {
      this.pool.beginQuiescent(message.nodeId, message.quiescentMs);
    }
    // leave-complete 无需额外处理：各节点的平静期定时器已负责物理移除。
  }

  /**
   * 向对等节点发起 RPC。准入失败（平静期/已移除）时立即抛出结构化
   * 错误，保证无突发 RPC 超时。
   */
  async sendRpc(targetId: string, payload: unknown): Promise<unknown> {
    this.pool.acquire(targetId);
    try {
      const target = this.cluster.getNode(targetId);
      if (!target || target.lifecycle === "gone") {
        throw new NodeNotFoundError(targetId);
      }
      return await target.dispatch({ from: this.id, payload });
    } finally {
      this.pool.release(targetId);
    }
  }

  private dispatch(request: RpcRequest): unknown | Promise<unknown> {
    return this.handler(request);
  }

  halt(): void {
    if (this.goneTimer !== undefined) {
      clearTimeout(this.goneTimer);
      this.goneTimer = undefined;
    }
    this.pool.dispose();
  }
}
