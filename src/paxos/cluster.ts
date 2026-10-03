/**
 * 集群测试/演示工具：把若干 PaxosNode 接入同一模拟网络，
 * 提供选举、提案、分区与一致性断言等便捷方法。
 */
import { PaxosNode } from "./node.ts";
import { SimulatedNetwork } from "./network.ts";
import type { LogEntry, Value } from "./types.ts";

export class Cluster {
  readonly network: SimulatedNetwork;
  readonly nodes: PaxosNode[];
  readonly leaseMs: number;

  constructor(size: number, leaseMs = 1000) {
    this.leaseMs = leaseMs;
    const ids = Array.from({ length: size }, (_, i) => i);
    this.network = new SimulatedNetwork(ids);
    this.nodes = ids.map(
      (id) =>
        new PaxosNode({
          id,
          peers: ids,
          network: this.network,
          now: () => this.network.time,
          leaseMs,
        }),
    );
  }

  /** 让指定节点竞选并投递全部消息，返回是否成为领导者。 */
  elect(nodeId: number): boolean {
    this.nodes[nodeId].campaign();
    this.network.flush();
    return this.nodes[nodeId].isLeader;
  }

  leader(): PaxosNode | undefined {
    return this.nodes.find((n) => n.isLeader);
  }

  /** 推进虚拟时间（租约过期等）。 */
  advanceTime(ms: number): void {
    this.network.flush(ms);
  }

  /** 提案并投递消息，返回分配的槽位。 */
  propose(nodeId: number, value: Value): number {
    const idx = this.nodes[nodeId].propose(value);
    this.network.flush();
    return idx;
  }

  /** 全集群已提交日志应完全一致（无空洞、同值）。 */
  committedLogs(): LogEntry[][] {
    return this.nodes.map((n) => n.committedEntries());
  }

  /** 断言所有节点已提交前缀一致：返回公共已提交值序列。 */
  convergedCommittedValues(): Value[] {
    const logs = this.committedLogs();
    const min = Math.min(...logs.map((l) => l.length));
    const out: Value[] = [];
    for (let i = 0; i < min; i++) {
      const v = logs[0][i].value;
      for (const l of logs) {
        if (l[i].value !== v) {
          throw new Error(`diverged committed value at index ${i + 1}`);
        }
      }
      out.push(v);
    }
    return out;
  }
}
