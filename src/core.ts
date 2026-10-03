import { Graph } from "./graph.ts";

export interface BatchStats {
  /** 全量分解处理的节点数（= |V|），用于与增量更新的局部开销对比。 */
  nodesProcessed: number;
  /** 剥离过程中扫描的邻接项总数（≈ 2|E|）。 */
  neighborScans: number;
}

/**
 * Batagelj–Zaversnik 批量核度分解，时间复杂度 O(|V| + |E|)。
 *
 * 反复剥离“当前度数最小”的节点：节点被剥离时的残余度数即其 Core-Number
 * （等价于动态移除度数 < K 的节点直到收敛，对所有 K 分层）。
 */
export function coreNumbers(graph: Graph, stats?: BatchStats): Uint32Array {
  const n = graph.nodeCount;
  const deg = new Uint32Array(n);
  let maxDeg = 0;
  for (let v = 0; v < n; v += 1) {
    deg[v] = graph.degree(v);
    if (deg[v] > maxDeg) maxDeg = deg[v];
  }

  // 计数排序：按当前度数把顶点装入桶中，vert/pos 支持 O(1) 度数下调
  const bin = new Uint32Array(maxDeg + 1);
  for (let v = 0; v < n; v += 1) bin[deg[v]] += 1;
  let sum = 0;
  for (let d = 0; d <= maxDeg; d += 1) {
    const count = bin[d];
    bin[d] = sum;
    sum += count;
  }
  const vert = new Uint32Array(n);
  const pos = new Uint32Array(n);
  for (let v = 0; v < n; v += 1) {
    pos[v] = bin[deg[v]];
    vert[pos[v]] = v;
    bin[deg[v]] += 1;
  }
  for (let d = maxDeg; d >= 1; d -= 1) bin[d] = bin[d - 1];
  bin[0] = 0;

  const core = new Uint32Array(n);
  let neighborScans = 0;
  for (let i = 0; i < n; i += 1) {
    const v = vert[i];
    core[v] = deg[v];
    for (const u of graph.neighbors(v)) {
      neighborScans += 1;
      if (deg[u] > deg[v]) {
        // u 尚未被剥离：度数减一，并在桶数组中前移一位
        const du = deg[u];
        const pu = pos[u];
        const pw = bin[du];
        const w = vert[pw];
        vert[pu] = w;
        pos[w] = pu;
        vert[pw] = u;
        pos[u] = pw;
        bin[du] += 1;
        deg[u] = du - 1;
      }
    }
  }
  if (stats) {
    stats.nodesProcessed = n;
    stats.neighborScans = neighborScans;
  }
  return core;
}

/** 按核度分层输出：core-number -> 该层节点列表（层与节点均升序）。 */
export function coreLayers(core: ArrayLike<number>): Map<number, number[]> {
  const layers = new Map<number, number[]>();
  for (let v = 0; v < core.length; v += 1) {
    const k = core[v];
    const list = layers.get(k);
    if (list) list.push(v);
    else layers.set(k, [v]);
  }
  return new Map([...layers.entries()].sort((a, b) => a[0] - b[0]));
}

export function maxCore(core: ArrayLike<number>): number {
  let m = 0;
  for (let i = 0; i < core.length; i += 1) if (core[i] > m) m = core[i];
  return m;
}

/** k-core 节点集：核度 >= k 的全部节点（升序）。 */
export function kCoreNodes(core: ArrayLike<number>, k: number): number[] {
  const out: number[] = [];
  for (let v = 0; v < core.length; v += 1) if (core[v] >= k) out.push(v);
  return out;
}

export interface Subgraph {
  nodes: number[];
  edges: [number, number][];
}

/** k-core 分层子图：核度 >= k 的节点及其诱导子图的边。 */
export function kCoreSubgraph(graph: Graph, core: ArrayLike<number>, k: number): Subgraph {
  const nodes = kCoreNodes(core, k);
  const inSub = new Set(nodes);
  const edges: [number, number][] = [];
  for (const u of nodes) {
    for (const v of graph.neighbors(u)) {
      if (u < v && inSub.has(v)) edges.push([u, v]);
    }
  }
  return { nodes, edges };
}
