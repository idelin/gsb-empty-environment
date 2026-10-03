import { Graph } from "./graph.ts";

/** 一次增量更新的开销统计 */
export interface UpdateStats {
  kind: "insert" | "remove" | "noop";
  /** 传播发生的层：K = min(core(u), core(v))（以更新前核度计） */
  k: number;
  /** 受影响局部区域大小（实际访问的节点数） */
  visited: number;
  /** 核度发生变化的节点数 */
  changed: number;
  /** 区域内的边松弛次数（局部更新的实际工作量） */
  edgeRelaxations: number;
}

/** 一次全量分解的开销统计 */
export interface DecomposeStats {
  vertices: number;
  edgeRelaxations: number;
}

/**
 * K-核分解与核度（Core-Number）增量维护。
 *
 * - 全量分解：Batagelj–Zaversnik 剥离算法，O(V + E)。
 * - 增量维护：利用单条边增删的理论性质——
 *   只有核度等于 K = min(core(u), core(v)) 的节点可能变化，且至多变化 1。
 *   因此只需在 u/v 所在的「核度 = K 连通分量」内做局部剥离传播，
 *   区域外节点的核度保持不变，开销远小于全量重算。
 */
export class KCoreDecomposition {
  private readonly graph: Graph;
  private readonly core = new Map<number, number>();
  private fullStats: DecomposeStats = { vertices: 0, edgeRelaxations: 0 };

  constructor(graph: Graph) {
    this.graph = graph;
    this.decompose();
  }

  /** Batagelj–Zaversnik：按残余度数升序剥离，得到每个节点的 Core-Number。 */
  private decompose(): void {
    const nodes = this.graph.nodes();
    const n = nodes.length;
    const deg = new Map<number, number>();
    let maxDeg = 0;
    for (const v of nodes) {
      const d = this.graph.degree(v);
      deg.set(v, d);
      if (d > maxDeg) maxDeg = d;
    }

    // 桶排序：按当前残余度数组织节点
    const bin = new Array<number>(maxDeg + 1).fill(0);
    for (const v of nodes) bin[deg.get(v)!]!++;
    let start = 0;
    for (let d = 0; d <= maxDeg; d++) {
      const num = bin[d]!;
      bin[d] = start;
      start += num;
    }
    const vert = new Array<number>(n);
    const pos = new Map<number, number>();
    for (const v of nodes) {
      const d = deg.get(v)!;
      pos.set(v, bin[d]!);
      vert[bin[d]!] = v;
      bin[d]!++;
    }
    for (let d = maxDeg; d >= 1; d--) bin[d] = bin[d - 1]!;
    bin[0] = 0;

    this.core.clear();
    let relaxations = 0;
    for (let i = 0; i < n; i++) {
      const v = vert[i]!;
      const dv = deg.get(v)!;
      this.core.set(v, dv);
      for (const u of this.graph.neighbors(v)) {
        relaxations++;
        const du = deg.get(u)!;
        if (du > dv) {
          // 将 u 从 bin[du] 移到 bin[du - 1]
          const pu = pos.get(u)!;
          const pw = bin[du]!;
          const w = vert[pw]!;
          if (u !== w) {
            vert[pu] = w;
            vert[pw] = u;
            pos.set(w, pu);
            pos.set(u, pw);
          }
          bin[du]!++;
          deg.set(u, du - 1);
        }
      }
    }
    this.fullStats = { vertices: n, edgeRelaxations: relaxations };
  }

  /** 全量重算（用于对照验证或大规模变更后重建）。 */
  recompute(): void {
    this.decompose();
  }

  get lastFullDecomposeStats(): DecomposeStats {
    return this.fullStats;
  }

  coreNumbers(): ReadonlyMap<number, number> {
    return this.core;
  }

  coreNumber(v: number): number {
    return this.core.get(v) ?? 0;
  }

  maxCore(): number {
    let max = 0;
    for (const c of this.core.values()) if (c > max) max = c;
    return max;
  }

  /** 按核度分层：返回 k -> 核度恰好等于 k 的节点列表（升序）。 */
  layers(): Map<number, number[]> {
    const out = new Map<number, number[]>();
    for (const [v, c] of this.core) {
      const list = out.get(c);
      if (list === undefined) out.set(c, [v]);
      else list.push(v);
    }
    const sorted = new Map<number, number[]>();
    for (const k of [...out.keys()].sort((a, b) => a - b)) {
      sorted.set(k, out.get(k)!.sort((a, b) => a - b));
    }
    return sorted;
  }

  /** k-core 分层子图：核度 >= k 的诱导子图。 */
  kCore(k: number): { nodes: number[]; edges: Array<[number, number]> } {
    const nodes = this.graph.nodes().filter((v) => this.coreNumber(v) >= k);
    const inSub = new Set(nodes);
    const edges: Array<[number, number]> = [];
    for (const [u, v] of this.graph.edges()) {
      if (inSub.has(u) && inSub.has(v)) edges.push([u, v]);
    }
    nodes.sort((a, b) => a - b);
    return { nodes, edges };
  }

  /** 增量加边：仅在受影响的局部邻域内传播核度上调。 */
  addEdge(u: number, v: number): UpdateStats {
    for (const x of [u, v]) if (!this.core.has(x)) this.core.set(x, 0);
    const k = Math.min(this.coreNumber(u), this.coreNumber(v));
    if (!this.graph.addEdge(u, v)) {
      return { kind: "noop", k, visited: 0, changed: 0, edgeRelaxations: 0 };
    }
    // 加边只会使核度为 K 的节点上升 1：存活阈值 K + 1
    return this.propagate(k, [u, v], k + 1, "insert");
  }

  /** 增量删边：仅在受影响的局部邻域内传播核度下调。 */
  removeEdge(u: number, v: number): UpdateStats {
    if (!this.graph.hasEdge(u, v)) {
      return { kind: "noop", k: 0, visited: 0, changed: 0, edgeRelaxations: 0 };
    }
    const k = Math.min(this.coreNumber(u), this.coreNumber(v));
    this.graph.removeEdge(u, v);
    // 删边只会使核度为 K 的节点下降 1：存活阈值 K
    return this.propagate(k, [u, v], k, "remove");
  }

  /**
   * 局部传播：
   * 1. 圈定区域 R = 种子所在的「核度 == K」连通分量（区域外核度不变）；
   * 2. 区域内以 threshold 做局部剥离——核度 > K 的外部邻居视为固定支撑；
   * 3. 存活者核度设为 threshold，被剥离者设为 threshold - 1。
   */
  private propagate(
    k: number,
    seeds: number[],
    threshold: number,
    kind: "insert" | "remove",
  ): UpdateStats {
    const region = new Set<number>();
    const stack: number[] = [];
    for (const s of seeds) {
      if (this.coreNumber(s) === k && !region.has(s)) {
        region.add(s);
        stack.push(s);
      }
    }
    while (stack.length > 0) {
      const w = stack.pop()!;
      for (const x of this.graph.neighbors(w)) {
        if (this.coreNumber(x) === k && !region.has(x)) {
          region.add(x);
          stack.push(x);
        }
      }
    }
    if (region.size === 0) {
      return { kind, k, visited: 0, changed: 0, edgeRelaxations: 0 };
    }

    // 残余支撑度 = 外部固定支撑（核度 > K 的邻居）+ 区域内未剥离邻居
    const residual = new Map<number, number>();
    for (const w of region) {
      let r = 0;
      for (const x of this.graph.neighbors(w)) {
        const cx = this.coreNumber(x);
        if (cx > k || (cx === k && region.has(x))) r++;
      }
      residual.set(w, r);
    }

    const peeled = new Set<number>();
    const queue: number[] = [];
    for (const w of region) {
      if (residual.get(w)! < threshold) queue.push(w);
    }
    let relaxations = 0;
    while (queue.length > 0) {
      const w = queue.pop()!;
      if (peeled.has(w)) continue;
      peeled.add(w);
      for (const x of this.graph.neighbors(w)) {
        if (region.has(x) && !peeled.has(x)) {
          relaxations++;
          const rx = residual.get(x)! - 1;
          residual.set(x, rx);
          if (rx < threshold) queue.push(x);
        }
      }
    }

    let changed = 0;
    for (const w of region) {
      const next = peeled.has(w) ? threshold - 1 : threshold;
      if (next !== k) {
        this.core.set(w, next);
        changed++;
      }
    }
    return {
      kind,
      k,
      visited: region.size,
      changed,
      edgeRelaxations: relaxations,
    };
  }
}
