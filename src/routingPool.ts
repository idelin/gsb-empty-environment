import { NodeDrainingError, NodeNotFoundError } from "./errors.ts";

export type RouteState = "ready" | "leaving" | "removed";

export interface RoutingPoolEvents {
  onLeaving?: (nodeId: string) => void;
  onRemoved?: (nodeId: string) => void;
}

/**
 * 每个节点本地维护的对等节点路由视图。
 *
 * 状态机：ready -> leaving（平静期，阻断新流量、允许在途请求完成）-> removed
 * （定时器超时后物理销毁路由条目）。
 */
export class RoutingPool {
  private readonly states = new Map<string, RouteState>();
  private readonly timers = new Map<string, ReturnType<typeof setTimeout>>();
  private readonly inflight = new Map<string, number>();
  private readonly events: RoutingPoolEvents;

  constructor(nodeIds: readonly string[] = [], events: RoutingPoolEvents = {}) {
    this.events = events;
    for (const id of nodeIds) {
      this.states.set(id, "ready");
    }
  }

  addReady(nodeId: string): void {
    if (!this.states.has(nodeId)) {
      this.states.set(nodeId, "ready");
    }
  }

  stateOf(nodeId: string): RouteState {
    return this.states.get(nodeId) ?? "removed";
  }

  isRoutable(nodeId: string): boolean {
    return this.states.get(nodeId) === "ready";
  }

  readyNodes(): string[] {
    return [...this.states.entries()]
      .filter(([, state]) => state === "ready")
      .map(([id]) => id);
  }

  /**
   * 收到离开意向后进入平静期：立即把节点移出就绪路由池，
   * 并启动定时器，超时后物理销毁路由条目。
   */
  beginQuiescent(nodeId: string, quiescentMs: number): boolean {
    if (this.states.get(nodeId) !== "ready") {
      return false;
    }
    this.states.set(nodeId, "leaving");
    const timer = setTimeout(() => this.evict(nodeId), quiescentMs);
    this.timers.set(nodeId, timer);
    this.events.onLeaving?.(nodeId);
    return true;
  }

  private evict(nodeId: string): void {
    if (!this.timers.has(nodeId)) {
      return;
    }
    this.timers.delete(nodeId);
    this.states.delete(nodeId);
    this.inflight.delete(nodeId);
    this.events.onRemoved?.(nodeId);
  }

  /**
   * 路由准入检查。平静期/已移除的节点立即抛出结构化错误，
   * 调用方因此得到即时失败，而不是悬挂后的 RPC 超时。
   */
  acquire(nodeId: string): void {
    const state = this.stateOf(nodeId);
    if (state === "leaving") {
      throw new NodeDrainingError(nodeId);
    }
    if (state === "removed") {
      throw new NodeNotFoundError(nodeId);
    }
    this.inflight.set(nodeId, (this.inflight.get(nodeId) ?? 0) + 1);
  }

  release(nodeId: string): void {
    const count = this.inflight.get(nodeId);
    if (count === undefined) {
      return;
    }
    if (count <= 1) {
      this.inflight.delete(nodeId);
    } else {
      this.inflight.set(nodeId, count - 1);
    }
  }

  inflightCount(nodeId: string): number {
    return this.inflight.get(nodeId) ?? 0;
  }

  dispose(): void {
    for (const timer of this.timers.values()) {
      clearTimeout(timer);
    }
    this.timers.clear();
  }
}
