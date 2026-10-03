/**
 * 集群模拟器：确定性逻辑时钟 + 可编程网络分区（对称 / 非对称）。
 *
 * - 时钟由测试/样例显式推进（advance），不依赖真实定时器，结果可复现。
 * - 连通性用有向边表示：默认全互联；partitionInto 划分对称分区；
 *   setLink 可单独切断单向链路以模拟非对称分区。
 */

import { ArbiterNode } from "./lease-arbiter.ts";
import type { NodeId, RequestLeaseMsg, RequestLeaseReply, Transport } from "./lease-arbiter.ts";

export interface ClusterOptions {
  leaseDurationMs: number;
}

export class Cluster implements Transport {
  readonly leaseDurationMs: number;
  private readonly nodes = new Map<NodeId, ArbiterNode>();
  private readonly links = new Map<string, boolean>();
  private clock = 0;

  constructor(nodeIds: NodeId[], options: ClusterOptions) {
    this.leaseDurationMs = options.leaseDurationMs;
    for (const id of nodeIds) {
      this.nodes.set(id, new ArbiterNode(id, this));
    }
    this.heal();
  }

  node(id: NodeId): ArbiterNode {
    const n = this.nodes.get(id);
    if (!n) throw new Error(`unknown node ${id}`);
    return n;
  }

  get members(): NodeId[] {
    return [...this.nodes.keys()];
  }

  /** 当前逻辑时间（ms） */
  now(): number {
    return this.clock;
  }

  quorumSize(): number {
    return Math.floor(this.nodes.size / 2) + 1;
  }

  memberIds(): NodeId[] {
    return this.members;
  }

  /** 推进逻辑时钟；各节点检查租约过期并自动剥离主权限 */
  advance(ms: number): void {
    this.clock += ms;
    for (const n of this.nodes.values()) n.onClockTick();
  }

  /**
   * 投递一次同步 RPC；请求走 from->to 链路，响应走 to->from 链路，
   * 任一方向断开则本次 RPC 失败（返回 null）。自身回路永远可达。
   */
  send(from: NodeId, to: NodeId, msg: RequestLeaseMsg): RequestLeaseReply | null {
    if (from !== to && (!this.isLinked(from, to) || !this.isLinked(to, from))) return null;
    return this.nodes.get(to)!.handleRequestLease(msg);
  }

  /** 恢复全互联 */
  heal(): void {
    for (const a of this.members) {
      for (const b of this.members) {
        if (a !== b) this.setLink(a, b, true);
      }
    }
  }

  /** 对称分区：仅同一分组内的节点互相可达，跨组链路全部切断 */
  partitionInto(groups: NodeId[][]): void {
    const groupOf = new Map<NodeId, number>();
    groups.forEach((g, i) => g.forEach((id) => groupOf.set(id, i)));
    for (const a of this.members) {
      for (const b of this.members) {
        if (a === b) continue;
        this.setLink(a, b, groupOf.get(a) === groupOf.get(b));
      }
    }
  }

  /** 设置单向链路（用于非对称分区） */
  setLink(from: NodeId, to: NodeId, open: boolean): void {
    this.links.set(`${from}->${to}`, open);
  }

  isLinked(from: NodeId, to: NodeId): boolean {
    return this.links.get(`${from}->${to}`) ?? false;
  }

  /** 当前持有有效租约的 Leader 列表（正常应至多 1 个） */
  activeLeaders(): NodeId[] {
    return this.members.filter((id) => this.node(id).isLeader);
  }
}
