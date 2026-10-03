import type { NodeId, Route, Topology } from './types.ts';
import type { ChangedEdge } from './lsdb.ts';

export interface SpfResult {
  dist: Map<NodeId, number>;
  prev: Map<NodeId, NodeId | null>;
  mode: 'full' | 'incremental';
  /** 本次实际重算目的地的数量 */
  recomputed: number;
}

/** 简单二叉最小堆（按距离出队） */
class MinHeap {
  private keys: NodeId[] = [];
  private prio: number[] = [];

  size(): number {
    return this.keys.length;
  }

  push(key: NodeId, prio: number): void {
    this.keys.push(key);
    this.prio.push(prio);
    let i = this.keys.length - 1;
    while (i > 0) {
      const parent = (i - 1) >> 1;
      if (this.prio[parent] <= this.prio[i]) break;
      this.swap(i, parent);
      i = parent;
    }
  }

  pop(): [NodeId, number] {
    const top: [NodeId, number] = [this.keys[0], this.prio[0]];
    const lastKey = this.keys.pop()!;
    const lastPrio = this.prio.pop()!;
    if (this.keys.length > 0) {
      this.keys[0] = lastKey;
      this.prio[0] = lastPrio;
      let i = 0;
      for (;;) {
        const left = 2 * i + 1;
        const right = left + 1;
        let smallest = i;
        if (left < this.prio.length && this.prio[left] < this.prio[smallest]) smallest = left;
        if (right < this.prio.length && this.prio[right] < this.prio[smallest]) smallest = right;
        if (smallest === i) break;
        this.swap(i, smallest);
        i = smallest;
      }
    }
    return top;
  }

  private swap(i: number, j: number): void {
    [this.keys[i], this.keys[j]] = [this.keys[j], this.keys[i]];
    [this.prio[i], this.prio[j]] = [this.prio[j], this.prio[i]];
  }
}

/** 全量 Dijkstra SPF */
export function fullSpf(topo: Topology, source: NodeId): SpfResult {
  const dist = new Map<NodeId, number>();
  const prev = new Map<NodeId, NodeId | null>();
  for (const id of topo.keys()) {
    dist.set(id, Infinity);
    prev.set(id, null);
  }
  dist.set(source, 0);
  const heap = new MinHeap();
  heap.push(source, 0);
  const settled = new Set<NodeId>();
  while (heap.size() > 0) {
    const [u, d] = heap.pop();
    if (settled.has(u)) continue;
    settled.add(u);
    for (const [v, w] of topo.get(u) ?? []) {
      const nd = d + w;
      if (nd < (dist.get(v) ?? Infinity)) {
        dist.set(v, nd);
        prev.set(v, u);
        heap.push(v, nd);
      }
    }
  }
  return { dist, prev, mode: 'full', recomputed: topo.size };
}

/**
 * 增量 Dijkstra SPF：
 * 仅对“旧最短路径树上经过变化边”的目的地子集重算，
 * 未受影响节点的距离作为已知边界直接复用。
 *
 * 正确性前提：变化仅为劣化（链路 Down / 权值上升）。此时未受影响
 * 节点的旧最短路径未使用变化边，且劣化不会产生更短路径，其距离不变。
 * 改善型变化（链路 Up / 权值下降）可能让任意节点获得新短路径，
 * 因此回退为全量计算。
 */
export function incrementalSpf(
  topo: Topology,
  source: NodeId,
  oldDist: Map<NodeId, number>,
  oldPrev: Map<NodeId, NodeId | null>,
  changes: ChangedEdge[],
): SpfResult {
  if (changes.some((c) => c.kind === 'up' || c.kind === 'cost-down')) {
    return fullSpf(topo, source);
  }
  const hitsChange = (a: NodeId, b: NodeId): boolean =>
    changes.some((c) => (c.u === a && c.v === b) || (c.u === b && c.v === a));

  // 受影响集合：旧 SPT 中路径经过变化边的目的地
  const affected = new Set<NodeId>();
  for (const id of topo.keys()) {
    if (id === source) continue;
    let cur: NodeId | null = id;
    const seen = new Set<NodeId>();
    while (cur !== null && cur !== source && !seen.has(cur)) {
      seen.add(cur);
      const parent = oldPrev.get(cur) ?? null;
      if (parent !== null && hitsChange(cur, parent)) {
        affected.add(id);
        break;
      }
      cur = parent;
    }
  }
  if (affected.size === 0) {
    return { dist: new Map(oldDist), prev: new Map(oldPrev), mode: 'incremental', recomputed: 0 };
  }
  // 受影响面过大时增量不划算，退化为全量
  if (affected.size * 2 >= topo.size) {
    return fullSpf(topo, source);
  }

  const dist = new Map(oldDist);
  const prev = new Map(oldPrev);
  const settled = new Set<NodeId>();
  for (const id of topo.keys()) {
    if (!affected.has(id)) settled.add(id);
  }
  const heap = new MinHeap();
  for (const id of affected) {
    dist.set(id, Infinity);
    prev.set(id, null);
    // 以未受影响节点的已知最优距离作为边界种子
    for (const [v, w] of topo.get(id) ?? []) {
      if (affected.has(v)) continue;
      const nd = (dist.get(v) ?? Infinity) + w;
      if (nd < dist.get(id)!) {
        dist.set(id, nd);
        prev.set(id, v);
      }
    }
    heap.push(id, dist.get(id)!);
  }
  while (heap.size() > 0) {
    const [u, d] = heap.pop();
    if (settled.has(u) || d > (dist.get(u) ?? Infinity)) continue;
    settled.add(u);
    for (const [v, w] of topo.get(u) ?? []) {
      if (!affected.has(v)) continue;
      const nd = d + w;
      if (nd < (dist.get(v) ?? Infinity)) {
        dist.set(v, nd);
        prev.set(v, u);
        heap.push(v, nd);
      }
    }
  }
  return { dist, prev, mode: 'incremental', recomputed: affected.size };
}

/** 由 dist/prev 生成转发表（首跳提取） */
export function buildRoutes(
  source: NodeId,
  dist: Map<NodeId, number>,
  prev: Map<NodeId, NodeId | null>,
): Map<NodeId, Route> {
  const routes = new Map<NodeId, Route>();
  for (const [dest, d] of dist) {
    if (dest === source || !Number.isFinite(d)) continue;
    let cur = dest;
    let parent = prev.get(cur) ?? null;
    while (parent !== null && parent !== source) {
      cur = parent;
      parent = prev.get(cur) ?? null;
    }
    routes.set(dest, { destination: dest, nextHop: cur, cost: d });
  }
  return routes;
}
