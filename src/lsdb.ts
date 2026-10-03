import type { LSA, NodeId, Topology } from './types.ts';

/** 自上次 SPF 计算以来发生变化的边 */
export interface ChangedEdge {
  u: NodeId;
  v: NodeId;
  kind: 'down' | 'up' | 'cost-up' | 'cost-down';
}

/**
 * 链路状态数据库（LSDB）。
 * 按 origin 保存最新 LSA，依据序列号拒绝过期通告；
 * 维护上次 SPF 使用的拓扑快照，用于增量计算的边差异分析。
 */
export class LSDB {
  private entries = new Map<NodeId, LSA>();
  private computedTopo: Topology | null = null;

  /** 插入 LSA；序列号更新才接受，返回是否更新了 LSDB */
  insert(lsa: LSA): boolean {
    const current = this.entries.get(lsa.origin);
    if (current && current.seq >= lsa.seq) return false;
    this.entries.set(lsa.origin, {
      origin: lsa.origin,
      seq: lsa.seq,
      age: lsa.age,
      links: lsa.links.map((l) => ({ ...l })),
    });
    return true;
  }

  get(origin: NodeId): LSA | undefined {
    return this.entries.get(origin);
  }

  origins(): NodeId[] {
    return [...this.entries.keys()];
  }

  /**
   * 由 LSDB 构建拓扑图。
   * 采用 OSPF 双向校验（two-way check）：仅当链路两端都通告了
   * 该链路（Up 且代价一致）时，边才参与 SPF 计算。
   */
  toTopology(): Topology {
    const topo: Topology = new Map();
    const ensure = (id: NodeId): Map<NodeId, number> => {
      let adj = topo.get(id);
      if (!adj) {
        adj = new Map();
        topo.set(id, adj);
      }
      return adj;
    };
    for (const lsa of this.entries.values()) {
      ensure(lsa.origin);
      for (const link of lsa.links) {
        if (!link.up) continue;
        ensure(link.neighbor);
        const peer = this.entries.get(link.neighbor);
        const mutual = peer?.links.some(
          (l) => l.neighbor === lsa.origin && l.up && l.cost === link.cost,
        );
        if (!mutual) continue;
        topo.get(lsa.origin)!.set(link.neighbor, link.cost);
      }
    }
    return topo;
  }

  /**
   * 计算当前拓扑与上次 SPF 快照之间的边差异，并更新快照。
   * 每次 SPF 运行前调用一次。
   */
  diffSinceComputed(): ChangedEdge[] {
    const now = this.toTopology();
    const prev = this.computedTopo;
    this.computedTopo = now;
    if (!prev) {
      // 首次计算：无快照可比，调用方应走全量 SPF
      return [{ u: '*', v: '*', kind: 'up' }];
    }
    const costOf = (topo: Topology, a: NodeId, b: NodeId): number | undefined =>
      topo.get(a)?.get(b) ?? topo.get(b)?.get(a);
    const edgeKey = (a: NodeId, b: NodeId): string => (a < b ? `${a}|${b}` : `${b}|${a}`);
    const keys = new Set<string>();
    for (const topo of [now, prev]) {
      for (const [u, adj] of topo) {
        for (const v of adj.keys()) keys.add(edgeKey(u, v));
      }
    }
    const changes: ChangedEdge[] = [];
    for (const key of keys) {
      const [a, b] = key.split('|') as [NodeId, NodeId];
      const cNow = costOf(now, a, b);
      const cPrev = costOf(prev, a, b);
      if (cNow === cPrev) continue;
      let kind: ChangedEdge['kind'];
      if (cPrev === undefined) kind = 'up';
      else if (cNow === undefined) kind = 'down';
      else kind = cNow > cPrev ? 'cost-up' : 'cost-down';
      changes.push({ u: a, v: b, kind });
    }
    return changes;
  }
}
