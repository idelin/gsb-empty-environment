import { EventEmitter } from "node:events";
import { NodeState } from "./types.ts";
import type { LeaveIntent, RouteEntry, RpcRequest, RpcResponse } from "./types.ts";
import { NoReadyNodeError, NodeDrainingError, UnknownNodeError } from "./errors.ts";

export interface ClusterNodeOptions {
  /** 平静期时长（毫秒）：Leaving 状态持续多久后物理销毁路由条目 */
  quiescentMs: number;
  /** 模拟单次 RPC 处理耗时（毫秒） */
  rpcLatencyMs?: number;
}

/**
 * 集群节点：维护本地路由表，处理 LeaveIntent 广播，
 * 在平静期内阻断新流量、排空在途请求，超时后物理移除路由条目。
 *
 * 事件（用于观测/测试）：
 *  - "state-change"   (nodeId, from, to)
 *  - "route-removed"  (nodeId)            平静期超时，物理销毁
 *  - "leave-broadcast"(intent)            本节点发起离开广播
 *  - "rpc-complete"   (nodeId, requestId) 在途请求完成
 */
export class ClusterNode extends EventEmitter {
  readonly id: string;
  private readonly quiescentMs: number;
  private readonly rpcLatencyMs: number;
  private readonly routeTable = new Map<string, RouteEntry>();
  private peers: ClusterNode[] = [];
  private epoch = 0;

  constructor(id: string, options: ClusterNodeOptions) {
    super();
    this.id = id;
    this.quiescentMs = options.quiescentMs;
    this.rpcLatencyMs = options.rpcLatencyMs ?? 5;
    this.routeTable.set(id, { nodeId: id, state: NodeState.Ready, inflight: 0 });
  }

  /** 组网：互相注册路由条目并建立广播通道 */
  connect(peers: ClusterNode[]): void {
    for (const peer of peers) {
      if (peer.id === this.id) continue;
      if (!this.peers.includes(peer)) this.peers.push(peer);
      if (!this.routeTable.has(peer.id)) {
        this.routeTable.set(peer.id, { nodeId: peer.id, state: NodeState.Ready, inflight: 0 });
      }
      if (!peer.peers.includes(this)) peer.peers.push(this);
      if (!peer.routeTable.has(this.id)) {
        peer.routeTable.set(this.id, { nodeId: this.id, state: NodeState.Ready, inflight: 0 });
      }
    }
  }

  get selfState(): NodeState {
    return this.routeTable.get(this.id)!.state;
  }

  getRouteEntry(nodeId: string): RouteEntry | undefined {
    return this.routeTable.get(nodeId);
  }

  /** 就绪路由池：仅 Ready 状态的节点可承接新流量 */
  readyPool(): string[] {
    return [...this.routeTable.values()]
      .filter((entry) => entry.state === NodeState.Ready)
      .map((entry) => entry.nodeId);
  }

  // ------------------------------------------------------------------
  // 优雅离开协议
  // ------------------------------------------------------------------

  /** 主动下线：本地置为 Leaving 并广播离开意向 */
  initiateLeave(): void {
    const self = this.routeTable.get(this.id)!;
    if (self.state === NodeState.Leaving) return;
    this.transition(self, NodeState.Leaving);
    const intent: LeaveIntent = {
      kind: "LeaveIntent",
      nodeId: this.id,
      epoch: ++this.epoch,
      sentAt: Date.now(),
    };
    this.emit("leave-broadcast", intent);
    for (const peer of this.peers) peer.onLeaveIntent(intent);
  }

  /** 收到对等节点的离开意向：置 Leaving、阻断新流量、启动平静期定时器 */
  onLeaveIntent(intent: LeaveIntent): void {
    const entry = this.routeTable.get(intent.nodeId);
    if (!entry || entry.state === NodeState.Leaving) return;
    this.transition(entry, NodeState.Leaving);
    entry.quiescentTimer = setTimeout(() => {
      this.routeTable.delete(intent.nodeId);
      this.emit("route-removed", intent.nodeId);
    }, this.quiescentMs);
  }

  private transition(entry: RouteEntry, to: NodeState): void {
    const from = entry.state;
    entry.state = to;
    this.emit("state-change", entry.nodeId, from, to);
  }

  // ------------------------------------------------------------------
  // 路由与 RPC
  // ------------------------------------------------------------------

  /** 从就绪池中挑选下一跳（轮询之外的简单策略：首个 Ready 节点） */
  route(): string {
    const pool = this.readyPool().filter((id) => id !== this.id);
    if (pool.length === 0) throw new NoReadyNodeError();
    return pool[0];
  }

  /** 向指定节点发起 RPC；Leaving 节点拒绝新流量（无突发超时，立即快速失败） */
  async sendRpc(targetId: string, request: RpcRequest): Promise<RpcResponse> {
    const entry = this.routeTable.get(targetId);
    if (!entry) throw new UnknownNodeError(targetId);
    if (entry.state !== NodeState.Ready) throw new NodeDrainingError(targetId);
    const target = this.peers.find((peer) => peer.id === targetId);
    if (!target) throw new UnknownNodeError(targetId);

    entry.inflight++;
    const remote = target.routeTable.get(target.id)!;
    remote.inflight++;
    try {
      return await target.handleRpc(request);
    } finally {
      entry.inflight--;
      remote.inflight--;
      this.emit("rpc-complete", targetId, request.id);
    }
  }

  /** 服务端处理：Leaving 状态下不再接受新请求，但在途请求正常完成 */
  private handleRpc(request: RpcRequest): Promise<RpcResponse> {
    if (this.selfState !== NodeState.Ready) {
      return Promise.reject(new NodeDrainingError(this.id));
    }
    return new Promise((resolve) => {
      setTimeout(() => {
        resolve({ id: request.id, from: this.id, ok: true });
      }, this.rpcLatencyMs);
    });
  }
}
