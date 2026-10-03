import { WeightedGraph, type NodeId } from './graph.ts';

export interface CentralityOptions {
  /** 指定参与计算的源节点；缺省时按 sampleSize 抽样或全量精确计算。 */
  sources?: NodeId[];
  /** 抽样源节点数（近似模式）；不设置则精确计算。 */
  sampleSize?: number;
  /** 抽样随机种子，保证结果可复现。 */
  seed?: number;
}

export interface CentralityResult {
  nodeBC: Map<NodeId, number>;
  edgeBC: Map<string, number>;
  sampledSources: NodeId[];
  exact: boolean;
}

export function edgeKey(u: NodeId, v: NodeId): string {
  return u < v ? `${u}|${v}` : `${v}|${u}`;
}

/** 确定性伪随机数（mulberry32），用于可复现的源节点抽样。 */
function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

class MinHeap {
  private keys: number[] = [];
  private vals: NodeId[] = [];

  get size(): number {
    return this.keys.length;
  }

  push(key: number, val: NodeId): void {
    this.keys.push(key);
    this.vals.push(val);
    let i = this.keys.length - 1;
    while (i > 0) {
      const p = (i - 1) >> 1;
      if (this.keys[p] <= this.keys[i]) break;
      this.swap(i, p);
      i = p;
    }
  }

  pop(): NodeId | undefined {
    if (this.keys.length === 0) return undefined;
    const top = this.vals[0];
    const lastKey = this.keys.pop()!;
    const lastVal = this.vals.pop()!;
    if (this.keys.length > 0) {
      this.keys[0] = lastKey;
      this.vals[0] = lastVal;
      let i = 0;
      for (;;) {
        const l = 2 * i + 1;
        const r = l + 1;
        let m = i;
        if (l < this.keys.length && this.keys[l] < this.keys[m]) m = l;
        if (r < this.keys.length && this.keys[r] < this.keys[m]) m = r;
        if (m === i) break;
        this.swap(i, m);
        i = m;
      }
    }
    return top;
  }

  private swap(a: number, b: number): void {
    [this.keys[a], this.keys[b]] = [this.keys[b], this.keys[a]];
    [this.vals[a], this.vals[b]] = [this.vals[b], this.vals[a]];
  }
}

interface SingleSourceResult {
  nodeCredit: Map<NodeId, number>;
  edgeCredit: Map<string, number>;
}

/**
 * 加权 Brandes 单源介数累积（Dijkstra 变体），
 * 同时产出节点介数与边介数的贡献量。
 */
function accumulateFrom(graph: WeightedGraph, source: NodeId): SingleSourceResult {
  const dist = new Map<NodeId, number>();
  const sigma = new Map<NodeId, number>();
  const pred = new Map<NodeId, NodeId[]>();
  const stack: NodeId[] = [];

  for (const n of graph.nodes()) {
    dist.set(n, Infinity);
    sigma.set(n, 0);
    pred.set(n, []);
  }
  dist.set(source, 0);
  sigma.set(source, 1);

  const heap = new MinHeap();
  heap.push(0, source);
  const done = new Set<NodeId>();
  while (heap.size > 0) {
    const v = heap.pop()!;
    const dv = dist.get(v)!;
    if (done.has(v)) continue; // 已出堆（惰性删除）
    done.add(v);
    stack.push(v);
    for (const [w, weight] of graph.neighbors(v)) {
      const cand = dv + weight;
      const dw = dist.get(w)!;
      if (cand < dw - 1e-12) {
        dist.set(w, cand);
        sigma.set(w, sigma.get(v)!);
        pred.set(w, [v]);
        heap.push(cand, w);
      } else if (Math.abs(cand - dw) <= 1e-12) {
        sigma.set(w, sigma.get(w)! + sigma.get(v)!);
        pred.get(w)!.push(v);
      }
    }
  }

  const nodeCredit = new Map<NodeId, number>();
  const edgeCredit = new Map<string, number>();
  const delta = new Map<NodeId, number>();
  for (const n of graph.nodes()) delta.set(n, 0);

  while (stack.length > 0) {
    const w = stack.pop()!;
    for (const v of pred.get(w)!) {
      const c = (sigma.get(v)! / sigma.get(w)!) * (1 + delta.get(w)!);
      const key = edgeKey(v, w);
      edgeCredit.set(key, (edgeCredit.get(key) ?? 0) + c);
      delta.set(v, delta.get(v)! + c);
    }
    if (w !== source) nodeCredit.set(w, (nodeCredit.get(w) ?? 0) + delta.get(w)!);
  }
  return { nodeCredit, edgeCredit };
}

/**
 * 介数中心性（节点 + 边）。
 * - 精确模式：遍历全部源节点（Brandes 算法）。
 * - 近似模式：按随机种子抽样部分源节点，结果按 n/样本数 放大无偏估计。
 */
export function betweennessCentrality(
  graph: WeightedGraph,
  options: CentralityOptions = {},
): CentralityResult {
  const all = graph.nodes();
  let sources: NodeId[];
  let exact: boolean;
  if (options.sources) {
    sources = options.sources.filter((s) => graph.hasNode(s));
    exact = sources.length === all.length;
  } else if (options.sampleSize !== undefined && options.sampleSize < all.length) {
    const rand = mulberry32(options.seed ?? 42);
    const pool = [...all];
    sources = [];
    const k = Math.max(1, Math.min(options.sampleSize, pool.length));
    for (let i = 0; i < k; i++) {
      const j = i + Math.floor(rand() * (pool.length - i));
      [pool[i], pool[j]] = [pool[j], pool[i]];
      sources.push(pool[i]);
    }
    exact = false;
  } else {
    sources = all;
    exact = true;
  }

  const nodeBC = new Map<NodeId, number>();
  const edgeBC = new Map<string, number>();
  for (const n of all) nodeBC.set(n, 0);

  for (const s of sources) {
    const { nodeCredit, edgeCredit } = accumulateFrom(graph, s);
    for (const [n, c] of nodeCredit) nodeBC.set(n, (nodeBC.get(n) ?? 0) + c);
    for (const [e, c] of edgeCredit) edgeBC.set(e, (edgeBC.get(e) ?? 0) + c);
  }

  // 无向图每对节点被两个方向各计一次，折半；抽样时按 n/样本数 放大。
  const scale = all.length / Math.max(1, sources.length) / 2;
  for (const [n, c] of nodeBC) nodeBC.set(n, c * scale);
  for (const [e, c] of edgeBC) edgeBC.set(e, c * scale);

  return { nodeBC, edgeBC, sampledSources: sources, exact };
}

export interface ShortestPathResult {
  path: NodeId[] | null;
  distance: number;
}

/** Dijkstra 单源最短路径（取其中一条），供分流分析使用。 */
export function shortestPath(graph: WeightedGraph, source: NodeId, target: NodeId): ShortestPathResult {
  if (!graph.hasNode(source) || !graph.hasNode(target)) return { path: null, distance: Infinity };
  const dist = new Map<NodeId, number>();
  const prev = new Map<NodeId, NodeId>();
  const done = new Set<NodeId>();
  for (const n of graph.nodes()) dist.set(n, Infinity);
  dist.set(source, 0);

  const heap = new MinHeap();
  heap.push(0, source);
  while (heap.size > 0) {
    const v = heap.pop()!;
    if (done.has(v)) continue;
    done.add(v);
    if (v === target) break;
    for (const [w, weight] of graph.neighbors(v)) {
      const cand = dist.get(v)! + weight;
      if (cand < dist.get(w)! - 1e-12) {
        dist.set(w, cand);
        prev.set(w, v);
        heap.push(cand, w);
      }
    }
  }

  const d = dist.get(target)!;
  if (!Number.isFinite(d)) return { path: null, distance: Infinity };
  const path: NodeId[] = [];
  let cur: NodeId | undefined = target;
  while (cur !== undefined) {
    path.unshift(cur);
    if (cur === source) break;
    cur = prev.get(cur);
  }
  return { path: path[0] === source ? path : null, distance: d };
}
