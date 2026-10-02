import { MemberTable, INTERVAL_COUNT } from "./membership.ts";
import type { MemberRecord, MemberState } from "./membership.ts";

export interface ProbeStats {
  digestExchanges: number;   // 4 字节摘要交换次数
  digestMismatches: number;  // 摘要失配次数
  intervalPulls: number;     // 区间对账拉取次数
  recordsTransferred: number;
}

// 集群节点：周期探活时与邻居交换 4 字节 Digest，失配则触发区间状态对账。
export class ClusterNode {
  readonly id: string;
  readonly table = new MemberTable();
  readonly stats: ProbeStats = {
    digestExchanges: 0,
    digestMismatches: 0,
    intervalPulls: 0,
    recordsTransferred: 0,
  };
  private links = new Set<ClusterNode>(); // 当前可达邻居（网络分区即割断链路）
  private incarnation = 0;

  constructor(id: string) {
    this.id = id;
    this.join(); // 节点自身首先是成员
  }

  private nextIncarnation(): number {
    return ++this.incarnation;
  }

  join(): void {
    this.table.upsert({ id: this.id, state: "ALIVE", incarnation: this.nextIncarnation() });
  }

  // ---- 本地成员变更（产生视图分歧的来源） ----
  addMember(memberId: string): void {
    this.table.upsert({ id: memberId, state: "ALIVE", incarnation: this.nextIncarnation() });
  }

  markState(memberId: string, state: MemberState): void {
    const cur = this.table.get(memberId);
    this.table.upsert({
      id: memberId,
      state,
      incarnation: Math.max(cur?.incarnation ?? 0, this.nextIncarnation()),
    });
  }

  // ---- 拓扑（链路）管理 ----
  connect(peer: ClusterNode): void {
    this.links.add(peer);
    peer.links.add(this);
  }

  disconnect(peer: ClusterNode): void {
    this.links.delete(peer);
    peer.links.delete(this);
  }

  get peers(): ClusterNode[] {
    return [...this.links];
  }

  // ---- 探活协议 ----
  // 第 1 步：交换 4 字节全局 Digest。相等 => 视图一致，本轮结束。
  // 第 2 步：失配 => 交换分区间 Digest，定位分歧区间。
  // 第 3 步：仅拉取分歧区间内的成员记录，双向合并（LWW）。
  probeOnce(): void {
    for (const peer of this.peers) {
      this.stats.digestExchanges++;
      peer.stats.digestExchanges++;
      if (this.table.digest === peer.table.digest) continue; // 快速路径：4 字节比对通过

      this.stats.digestMismatches++;
      peer.stats.digestMismatches++;
      const mine = this.table.intervalDigests();
      const theirs = peer.table.intervalDigests();
      for (let i = 0; i < INTERVAL_COUNT; i++) {
        if (mine[i] === theirs[i]) continue;
        // 区间状态对账：双向拉取该区间记录并合并。
        this.pullInterval(peer, i);
        peer.pullInterval(this, i);
      }
    }
  }

  private pullInterval(peer: ClusterNode, interval: number): void {
    this.stats.intervalPulls++;
    const records = peer.table.recordsInInterval(interval);
    this.stats.recordsTransferred += records.length;
    for (const r of records) this.table.upsert(r);
  }
}

// 运行一轮探活：所有节点各执行一次 probeOnce，返回是否全网收敛。
export function runProbeRound(nodes: ClusterNode[]): boolean {
  for (const n of nodes) n.probeOnce();
  return isConverged(nodes);
}

export function isConverged(nodes: ClusterNode[]): boolean {
  if (nodes.length === 0) return true;
  const ref = nodes[0].table.digest;
  return nodes.every((n) => n.table.digest === ref);
}
