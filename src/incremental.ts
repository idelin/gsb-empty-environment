import { Graph } from "./graph.ts";
import { coreNumbers } from "./core.ts";

export interface UpdateStats {
  kind: "insert" | "remove";
  u: number;
  v: number;
  /** 受影响核度层 K = min(core[u], core[v])（操作前的核度）。 */
  K: number;
  /** 局部遍历访问的候选节点数（局部更新开销的核心度量）。 */
  visited: number;
  /** 核度实际发生变化的节点数。 */
  updated: number;
  /** 邻接表扫描次数。 */
  neighborScans: number;
  /** 核度发生变化的节点（升序）。 */
  changed: number[];
}

function emptyStats(kind: "insert" | "remove", u: number, v: number): UpdateStats {
  return { kind, u, v, K: 0, visited: 0, updated: 0, neighborScans: 0, changed: [] };
}

/**
 * 增量核度维护器（基于 Sariyuce et al. 的 traversal / propagate 思想）。
 *
 * 单条边增删只会使部分节点的核度变化 ±1，且只有核度等于
 * K = min(core[u], core[v]) 的节点可能受影响。因此每次更新只在
 * 核度为 K 的局部连通邻域内重新传播，无需全图重算。
 */
export class CoreMaintenance {
  readonly graph = new Graph();
  private core: number[] = [];
  readonly totals = {
    inserts: 0,
    removes: 0,
    visited: 0,
    updated: 0,
    neighborScans: 0,
  };

  /** 从边列表构建，并做一次全量分解作为初始核度。 */
  static fromEdges(edges: Iterable<[number, number]>): CoreMaintenance {
    const cm = new CoreMaintenance();
    for (const [u, v] of edges) cm.graph.addEdge(u, v);
    cm.recomputeAll();
    return cm;
  }

  /** 全量重算（基线 / 校验用），返回最新核度数组。 */
  recomputeAll(): Uint32Array {
    const batch = coreNumbers(this.graph);
    this.core = Array.from(batch);
    return batch;
  }

  coreOf(v: number): number {
    return this.core[v] ?? 0;
  }

  coreNumbers(): Uint32Array {
    return Uint32Array.from(this.core);
  }

  get maxCore(): number {
    let m = 0;
    for (const c of this.core) if (c > m) m = c;
    return m;
  }

  private ensureCapacity(n: number): void {
    while (this.core.length < n) this.core.push(0);
  }

  private accumulate(stats: UpdateStats): void {
    this.totals.visited += stats.visited;
    this.totals.updated += stats.updated;
    this.totals.neighborScans += stats.neighborScans;
  }

  /**
   * 插入边 (u, v)：在核度 == K 的候选邻域内遍历，
   * 用“乐观最大核度” mcd(w) = #(core>K 邻居) + #(未被驱逐的 core==K 邻居)
   * 判定节点能否晋升到 K+1；mcd(w) <= K 的节点被驱逐并级联传染，
   * 幸存节点的核度统一 +1。
   */
  insertEdge(u: number, v: number): UpdateStats {
    const stats = emptyStats("insert", u, v);
    const existed = this.graph.hasEdge(u, v);
    this.graph.addEdge(u, v);
    this.ensureCapacity(this.graph.nodeCount);
    if (existed) return stats; // 重边：无影响
    this.totals.inserts += 1;

    const core = this.core;
    const K = Math.min(core[u], core[v]);
    stats.K = K;

    const visited = new Set<number>();
    const evicted = new Set<number>();
    const queue: number[] = [];
    if (core[u] === K) queue.push(u);
    if (core[v] === K) queue.push(v);

    const mcd = (w: number): number => {
      let count = 0;
      for (const x of this.graph.neighbors(w)) {
        stats.neighborScans += 1;
        if (core[x] > K || (core[x] === K && !evicted.has(x))) count += 1;
      }
      return count;
    };

    const evict = (seed: number): void => {
      const stack = [seed];
      while (stack.length > 0) {
        const w = stack.pop()!;
        if (evicted.has(w)) continue;
        evicted.add(w);
        visited.delete(w);
        for (const x of this.graph.neighbors(w)) {
          stats.neighborScans += 1;
          if (core[x] === K && visited.has(x) && !evicted.has(x) && mcd(x) <= K) {
            stack.push(x);
          }
        }
      }
    };

    while (queue.length > 0) {
      const w = queue.pop()!;
      if (visited.has(w) || evicted.has(w)) continue;
      visited.add(w);
      stats.visited += 1;
      if (mcd(w) > K) {
        for (const x of this.graph.neighbors(w)) {
          stats.neighborScans += 1;
          if (core[x] === K && !visited.has(x) && !evicted.has(x)) queue.push(x);
        }
      } else {
        evict(w);
      }
    }

    for (const w of visited) {
      core[w] = K + 1;
      stats.updated += 1;
      stats.changed.push(w);
    }
    stats.changed.sort((a, b) => a - b);
    this.accumulate(stats);
    return stats;
  }

  /**
   * 删除边 (u, v)：先收集与端点连通、核度 == K 的候选区域，
   * 再在区域内做局部剥离——支撑度（core>K 邻居数 + 区域内邻居数）
   * 不足 K 的节点核度降为 K-1 并级联。
   */
  removeEdge(u: number, v: number): UpdateStats {
    const stats = emptyStats("remove", u, v);
    if (!this.graph.removeEdge(u, v)) return stats; // 边不存在：无影响
    this.totals.removes += 1;
    this.ensureCapacity(this.graph.nodeCount);

    const core = this.core;
    const K = Math.min(core[u], core[v]);
    stats.K = K;

    // 1) 候选区域：与核度 == K 的端点连通、且核度 == K 的节点
    const region = new Set<number>();
    const queue: number[] = [];
    if (core[u] === K) queue.push(u);
    if (core[v] === K) queue.push(v);
    while (queue.length > 0) {
      const w = queue.pop()!;
      if (region.has(w)) continue;
      region.add(w);
      for (const x of this.graph.neighbors(w)) {
        stats.neighborScans += 1;
        if (core[x] === K && !region.has(x)) queue.push(x);
      }
    }
    stats.visited = region.size;

    // 2) 区域内支撑度
    const support = new Map<number, number>();
    const dropQueue: number[] = [];
    for (const w of region) {
      let s = 0;
      for (const x of this.graph.neighbors(w)) {
        stats.neighborScans += 1;
        if (core[x] > K || region.has(x)) s += 1;
      }
      support.set(w, s);
      if (s < K) dropQueue.push(w);
    }

    // 3) 局部剥离并级联
    const dropped = new Set<number>();
    while (dropQueue.length > 0) {
      const w = dropQueue.pop()!;
      if (dropped.has(w)) continue;
      if (support.get(w)! >= K) continue;
      dropped.add(w);
      for (const x of this.graph.neighbors(w)) {
        stats.neighborScans += 1;
        if (region.has(x) && !dropped.has(x)) {
          const s = support.get(x)! - 1;
          support.set(x, s);
          if (s < K) dropQueue.push(x);
        }
      }
    }

    for (const w of dropped) {
      core[w] = K - 1;
      stats.updated += 1;
      stats.changed.push(w);
    }
    stats.changed.sort((a, b) => a - b);
    this.accumulate(stats);
    return stats;
  }
}
