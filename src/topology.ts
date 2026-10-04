import { Lsa, NodeId } from "./types";

export type Topo = Record<NodeId, Record<NodeId, number>>;

/** LSDB 的规范化序列化（按来源排序），用于跨节点一致性比对 */
export function canonicalLsdb(entries: [NodeId, Lsa][]): string {
  const sorted = [...entries].sort(([a], [b]) => (a < b ? -1 : 1));
  return JSON.stringify(
    sorted.map(([origin, lsa]) => [
      origin,
      lsa.version.generation,
      lsa.version.sequence,
      lsa.withdrawn,
      Object.keys(lsa.links)
        .sort()
        .map((n) => [n, lsa.links[n]]),
    ]),
  );
}

/** 由 LSDB 推导拓扑：忽略已撤销的 LSA，取各来源通告的并集 */
export function deriveTopology(entries: [NodeId, Lsa][]): Topo {
  const topo: Topo = {};
  const sorted = [...entries].sort(([a], [b]) => (a < b ? -1 : 1));
  for (const [origin, lsa] of sorted) {
    if (lsa.withdrawn) continue;
    topo[origin] = topo[origin] ?? {};
    for (const neighbor of Object.keys(lsa.links).sort()) {
      topo[origin][neighbor] = lsa.links[neighbor];
      topo[neighbor] = topo[neighbor] ?? {};
      if (!(origin in topo[neighbor])) topo[neighbor][origin] = lsa.links[neighbor];
    }
  }
  return topo;
}

export function canonicalTopo(topo: Topo): string {
  return JSON.stringify(
    Object.keys(topo)
      .sort()
      .map((node) => [
        node,
        Object.keys(topo[node])
          .sort()
          .map((n) => [n, topo[node][n]]),
      ]),
  );
}
