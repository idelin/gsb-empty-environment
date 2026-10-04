import type { Version } from "./version.ts";

export type Adjacency = Map<string, Map<string, { weight: number; version: Version }>>;

export type Route =
  | {
      reachable: true;
      distance: number;
      hops: number;
      /** 源到自身的下一跳为 null。 */
      nextHop: string | null;
      path: string[];
      /** 路径首条边（源 -> 下一跳）的通告版本；源到自身为 null。 */
      firstEdgeVersion: Version | null;
    }
  | { reachable: false };

/** 路径排序键：先距离，再跳数，最后完整节点 ID 序列字典序。 */
export interface PathKey {
  dist: number;
  hops: number;
  path: string[];
}

export function comparePathKey(a: PathKey, b: PathKey): number {
  if (a.dist !== b.dist) return a.dist < b.dist ? -1 : 1;
  if (a.hops !== b.hops) return a.hops < b.hops ? -1 : 1;
  const n = Math.min(a.path.length, b.path.length);
  for (let i = 0; i < n; i++) {
    if (a.path[i] !== b.path[i]) return a.path[i] < b.path[i] ? -1 : 1;
  }
  return a.path.length - b.path.length;
}

/** 小二叉堆，按 PathKey 排序。 */
class MinHeap {
  private keys: PathKey[] = [];
  private nodes: string[] = [];

  get size(): number {
    return this.keys.length;
  }

  push(key: PathKey, node: string): void {
    this.keys.push(key);
    this.nodes.push(node);
    let i = this.keys.length - 1;
    while (i > 0) {
      const p = (i - 1) >> 1;
      if (comparePathKey(this.keys[p], this.keys[i]) <= 0) break;
      this.swap(i, p);
      i = p;
    }
  }

  pop(): { key: PathKey; node: string } | null {
    if (this.keys.length === 0) return null;
    const top = { key: this.keys[0], node: this.nodes[0] };
    const lastKey = this.keys.pop()!;
    const lastNode = this.nodes.pop()!;
    if (this.keys.length > 0) {
      this.keys[0] = lastKey;
      this.nodes[0] = lastNode;
      let i = 0;
      for (;;) {
        let m = i;
        const l = 2 * i + 1;
        const r = l + 1;
        if (l < this.keys.length && comparePathKey(this.keys[l], this.keys[m]) < 0) m = l;
        if (r < this.keys.length && comparePathKey(this.keys[r], this.keys[m]) < 0) m = r;
        if (m === i) break;
        this.swap(i, m);
        i = m;
      }
    }
    return top;
  }

  private swap(i: number, j: number): void {
    [this.keys[i], this.keys[j]] = [this.keys[j], this.keys[i]];
    [this.nodes[i], this.nodes[j]] = [this.nodes[j], this.nodes[i]];
  }
}

/**
 * 从 source 出发计算到所有已知节点的最优路由。
 *
 * 权重非负，且排序键对“追加一条边”是单调的（距离不减、跳数严格 +1），
 * 因此 Dijkstra 的定點性质成立。零权环只会产生“同距离、更多跳”的
 * 候选键，永远不可能更优，松弛条件保证算法必然终止。
 */
export function computeRoutes(
  adj: Adjacency,
  nodes: Iterable<string>,
  source: string,
): Map<string, Route> {
  const best = new Map<string, PathKey>();
  const settled = new Set<string>();
  const heap = new MinHeap();

  const start: PathKey = { dist: 0, hops: 0, path: [source] };
  best.set(source, start);
  heap.push(start, source);

  while (heap.size > 0) {
    const { key, node } = heap.pop()!;
    if (settled.has(node)) continue;
    const cur = best.get(node);
    if (!cur || comparePathKey(key, cur) !== 0) continue; // 过期堆项
    settled.add(node);

    for (const [dst, edge] of adj.get(node) ?? []) {
      if (settled.has(dst)) continue;
      const cand: PathKey = {
        dist: key.dist + edge.weight,
        hops: key.hops + 1,
        path: [...key.path, dst],
      };
      const old = best.get(dst);
      if (!old || comparePathKey(cand, old) < 0) {
        best.set(dst, cand);
        heap.push(cand, dst);
      }
    }
  }

  const routes = new Map<string, Route>();
  const all = new Set<string>(nodes);
  all.add(source);
  for (const node of all) {
    const k = best.get(node);
    if (!k) {
      routes.set(node, { reachable: false }); // 不可达单独标记，不用大数冒充
      continue;
    }
    const nextHop = k.path.length > 1 ? k.path[1] : null;
    const firstEdgeVersion =
      nextHop === null ? null : { ...adj.get(source)!.get(nextHop)!.version };
    routes.set(node, {
      reachable: true,
      distance: k.dist,
      hops: k.hops,
      nextHop,
      path: k.path,
      firstEdgeVersion,
    });
  }
  return routes;
}
