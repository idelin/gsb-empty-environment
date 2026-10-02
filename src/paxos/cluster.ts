/**
 * 集群封装：N 个 PaxosNode + 共享手动时钟（确定性测试租约）。
 */
import { InMemoryNetwork } from "./network.ts";
import { PaxosNode } from "./node.ts";
import type { LogValue } from "./types.ts";

export class Cluster {
  readonly net = new InMemoryNetwork();
  readonly nodes = new Map<string, PaxosNode>();
  readonly ids: string[];
  private clock = 0;
  private readonly leaseMs: number;

  constructor(n: number, leaseMs = 1000) {
    this.leaseMs = leaseMs;
    this.ids = Array.from({ length: n }, (_, i) => `N${i}`);
    for (const id of this.ids) {
      this.nodes.set(
        id,
        new PaxosNode(id, this.ids, this.net, {
          leaseMs,
          now: () => this.clock,
          resolvePeer: (pid) => this.nodes.get(pid),
        }),
      );
    }
  }

  node(id: string): PaxosNode {
    const n = this.nodes.get(id);
    if (!n) throw new Error(`unknown node ${id}`);
    return n;
  }

  /** 推进逻辑时钟（使租约过期）。 */
  tick(ms: number): void {
    this.clock += ms;
  }

  get now(): number {
    return this.clock;
  }

  /** 让某节点竞选并成为 Leader（需多数派可达）。 */
  elect(id: string): PaxosNode {
    const node = this.node(id);
    node.campaign();
    if (!node.isLeader) throw new Error(`${id} failed to win election`);
    return node;
  }

  /** 网络分区：两组节点互不连通。 */
  partition(groupA: string[], groupB: string[]): void {
    this.net.partition(groupA, groupB);
  }

  heal(): void {
    this.net.healAll();
  }

  /** 集群不变量：所有节点已提交前缀一致（已提交条目绝不丢失/冲突）。 */
  assertCommittedConsistent(): void {
    const committed = [...this.nodes.values()].map((n) => n.committedEntries());
    const maxLen = Math.max(...committed.map((c) => c.length));
    for (let i = 0; i < maxLen; i++) {
      const vals = new Set(committed.filter((c) => i < c.length).map((c) => String(c[i])));
      if (vals.size > 1) {
        throw new Error(`committed log diverged at index ${i}: ${[...vals].join(",")}`);
      }
    }
  }

  committedValues(): LogValue[][] {
    return [...this.nodes.values()].map((n) => n.committedEntries());
  }
}
