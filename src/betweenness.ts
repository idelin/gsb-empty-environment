import { WeightedGraph } from "./graph.ts";

export interface BetweennessResult {
  node: Map<string, number>;
  edge: Map<string, number>;
}

export function edgeKey(a: string, b: string): string {
  return a < b ? `${a}|${b}` : `${b}|${a}`;
}

class MinHeap {
  private items: Array<[number, string]> = [];

  get size(): number {
    return this.items.length;
  }

  push(priority: number, value: string): void {
    this.items.push([priority, value]);
    let i = this.items.length - 1;
    while (i > 0) {
      const parent = (i - 1) >> 1;
      if (this.items[parent][0] <= this.items[i][0]) break;
      [this.items[parent], this.items[i]] = [this.items[i], this.items[parent]];
      i = parent;
    }
  }

  pop(): [number, string] {
    const top = this.items[0];
    const last = this.items.pop()!;
    if (this.items.length > 0) {
      this.items[0] = last;
      let i = 0;
      for (;;) {
        const left = 2 * i + 1;
        const right = left + 1;
        let smallest = i;
        if (left < this.items.length && this.items[left][0] < this.items[smallest][0]) smallest = left;
        if (right < this.items.length && this.items[right][0] < this.items[smallest][0]) smallest = right;
        if (smallest === i) break;
        [this.items[smallest], this.items[i]] = [this.items[i], this.items[smallest]];
        i = smallest;
      }
    }
    return top;
  }
}

export function brandes(graph: WeightedGraph, sources?: Iterable<string>): BetweennessResult {
  const nodeBc = new Map<string, number>();
  const edgeBc = new Map<string, number>();
  const nodes = graph.nodes();
  for (const n of nodes) nodeBc.set(n, 0);

  for (const s of sources ?? nodes) {
    const stack: string[] = [];
    const pred = new Map<string, string[]>();
    const sigma = new Map<string, number>();
    const dist = new Map<string, number>();
    for (const n of nodes) {
      pred.set(n, []);
      sigma.set(n, 0);
      dist.set(n, Infinity);
    }
    sigma.set(s, 1);
    dist.set(s, 0);
    const heap = new MinHeap();
    heap.push(0, s);

    while (heap.size > 0) {
      const [d, v] = heap.pop();
      if (d > dist.get(v)!) continue;
      stack.push(v);
      for (const [w, weight] of graph.neighbors(v)) {
        const nd = d + weight;
        const dw = dist.get(w)!;
        if (nd < dw) {
          dist.set(w, nd);
          heap.push(nd, w);
          sigma.set(w, sigma.get(v)!);
          pred.set(w, [v]);
        } else if (nd === dw) {
          sigma.set(w, sigma.get(w)! + sigma.get(v)!);
          pred.get(w)!.push(v);
        }
      }
    }

    const delta = new Map<string, number>();
    for (const n of nodes) delta.set(n, 0);
    while (stack.length > 0) {
      const w = stack.pop()!;
      for (const v of pred.get(w)!) {
        const key = edgeKey(v, w);
        const credit = (sigma.get(v)! / sigma.get(w)!) * (1 + delta.get(w)!);
        edgeBc.set(key, (edgeBc.get(key) ?? 0) + credit);
        delta.set(v, delta.get(v)! + credit);
      }
      if (w !== s) nodeBc.set(w, nodeBc.get(w)! + delta.get(w)!);
    }
  }

  for (const [n, v] of nodeBc) nodeBc.set(n, v / 2);
  for (const [e, v] of edgeBc) edgeBc.set(e, v / 2);
  return { node: nodeBc, edge: edgeBc };
}

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export function approximateBetweenness(
  graph: WeightedGraph,
  sampleCount: number,
  seed = 42,
): BetweennessResult {
  const nodes = graph.nodes();
  const k = Math.min(sampleCount, nodes.length);
  if (k === nodes.length) return brandes(graph);

  const rng = mulberry32(seed);
  const chosen = new Set<string>();
  while (chosen.size < k) chosen.add(nodes[Math.floor(rng() * nodes.length)]);

  const result = brandes(graph, chosen);
  const scale = nodes.length / k;
  for (const [n, v] of result.node) result.node.set(n, v * scale);
  for (const [e, v] of result.edge) result.edge.set(e, v * scale);
  return result;
}

export function shortestPath(graph: WeightedGraph, source: string, target: string): string[] | null {
  const dist = new Map<string, number>();
  const prev = new Map<string, string>();
  for (const n of graph.nodes()) dist.set(n, Infinity);
  dist.set(source, 0);
  const heap = new MinHeap();
  heap.push(0, source);

  while (heap.size > 0) {
    const [d, v] = heap.pop();
    if (d > dist.get(v)!) continue;
    if (v === target) break;
    for (const [w, weight] of graph.neighbors(v)) {
      const nd = d + weight;
      if (nd < dist.get(w)!) {
        dist.set(w, nd);
        prev.set(w, v);
        heap.push(nd, w);
      }
    }
  }

  if (!prev.has(target) && source !== target) return null;
  const path = [target];
  let cur = target;
  while (cur !== source) {
    cur = prev.get(cur)!;
    path.unshift(cur);
  }
  return path;
}
