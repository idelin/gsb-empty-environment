import type { Lsdb } from './lsdb.ts';
import type { Adjacency, NodeId } from './types.ts';

export interface SpfResult {
  dist: Map<NodeId, number>;
  prev: Map<NodeId, NodeId | null>;
  affectedCount: number;
}

class MinHeap {
  private nodes: NodeId[] = [];
  private dists: number[] = [];

  get size(): number {
    return this.nodes.length;
  }

  private less(i: number, j: number): boolean {
    if (this.dists[i] !== this.dists[j]) return this.dists[i] < this.dists[j];
    return this.nodes[i] < this.nodes[j];
  }

  push(node: NodeId, dist: number): void {
    this.nodes.push(node);
    this.dists.push(dist);
    let i = this.nodes.length - 1;
    while (i > 0) {
      const parent = (i - 1) >> 1;
      if (this.less(i, parent)) {
        this.swap(i, parent);
        i = parent;
      } else break;
    }
  }

  pop(): { node: NodeId; dist: number } | undefined {
    if (this.nodes.length === 0) return undefined;
    const top = { node: this.nodes[0], dist: this.dists[0] };
    const lastNode = this.nodes.pop() as NodeId;
    const lastDist = this.dists.pop() as number;
    if (this.nodes.length > 0) {
      this.nodes[0] = lastNode;
      this.dists[0] = lastDist;
      let i = 0;
      for (;;) {
        const left = 2 * i + 1;
        const right = left + 1;
        let smallest = i;
        if (left < this.nodes.length && this.less(left, smallest)) smallest = left;
        if (right < this.nodes.length && this.less(right, smallest)) smallest = right;
        if (smallest === i) break;
        this.swap(i, smallest);
        i = smallest;
      }
    }
    return top;
  }

  private swap(i: number, j: number): void {
    [this.nodes[i], this.nodes[j]] = [this.nodes[j], this.nodes[i]];
    [this.dists[i], this.dists[j]] = [this.dists[j], this.dists[i]];
  }
}

/** Build adjacency from LSDB, honoring the OSPF two-way check. */
export function buildGraph(lsdb: Lsdb): Adjacency {
  const graph: Adjacency = new Map();
  for (const lsa of lsdb.all()) graph.set(lsa.originator, new Map());
  for (const lsa of lsdb.all()) {
    for (const link of lsa.links) {
      if (lsdb.hasLink(link.neighbor, lsa.originator)) {
        (graph.get(lsa.originator) as Map<NodeId, number>).set(link.neighbor, link.cost);
      }
    }
  }
  return graph;
}

/** Directed edges whose cost increased or that were removed. */
export function diffGraphs(oldGraph: Adjacency, newGraph: Adjacency): Array<[NodeId, NodeId]> {
  const worsened: Array<[NodeId, NodeId]> = [];
  const nodes = new Set<NodeId>([...oldGraph.keys(), ...newGraph.keys()]);
  for (const a of nodes) {
    const neighbors = new Set<NodeId>([
      ...(oldGraph.get(a)?.keys() ?? []),
      ...(newGraph.get(a)?.keys() ?? []),
    ]);
    for (const b of neighbors) {
      const oldCost = oldGraph.get(a)?.get(b) ?? Infinity;
      const newCost = newGraph.get(a)?.get(b) ?? Infinity;
      if (newCost > oldCost) worsened.push([a, b]);
    }
  }
  return worsened;
}

function relax(
  graph: Adjacency,
  dist: Map<NodeId, number>,
  prev: Map<NodeId, NodeId | null>,
  heap: MinHeap,
): void {
  const settled = new Set<NodeId>();
  while (heap.size > 0) {
    const { node: u, dist: d } = heap.pop() as { node: NodeId; dist: number };
    if (settled.has(u) || d !== dist.get(u)) continue;
    settled.add(u);
    if (d === Infinity) continue;
    for (const [v, cost] of graph.get(u) ?? []) {
      const nd = d + cost;
      const dv = dist.get(v) ?? Infinity;
      const pv = prev.get(v);
      if (nd < dv || (nd === dv && (pv === null || pv === undefined || u < pv))) {
        dist.set(v, nd);
        prev.set(v, u);
        heap.push(v, nd);
      }
    }
  }
}

/** Classic full Dijkstra from the source. */
export function fullSpf(graph: Adjacency, source: NodeId): SpfResult {
  const dist = new Map<NodeId, number>();
  const prev = new Map<NodeId, NodeId | null>();
  for (const n of graph.keys()) {
    dist.set(n, Infinity);
    prev.set(n, null);
  }
  dist.set(source, 0);
  const heap = new MinHeap();
  heap.push(source, 0);
  relax(graph, dist, prev, heap);
  return { dist, prev, affectedCount: graph.size };
}

/**
 * Incremental SPF (Narvaez-style dynamic SPT update).
 *
 * Nodes whose old shortest-path-tree branch used a worsened/removed edge
 * (the subtree below that edge) are invalidated; every other node keeps its
 * old distance as a valid upper bound. A warm-started Dijkstra seeded with
 * all unaffected nodes then recomputes only what actually changed — which
 * also absorbs link additions and cost decreases.
 */
export function incrementalSpf(
  graph: Adjacency,
  source: NodeId,
  oldDist: Map<NodeId, number>,
  oldPrev: Map<NodeId, NodeId | null>,
  worsened: Array<[NodeId, NodeId]>,
): SpfResult {
  const dist = new Map<NodeId, number>(oldDist);
  const prev = new Map<NodeId, NodeId | null>(oldPrev);

  const children = new Map<NodeId, NodeId[]>();
  for (const [node, parent] of oldPrev) {
    if (parent !== null) {
      const list = children.get(parent) ?? [];
      list.push(node);
      children.set(parent, list);
    }
  }

  const affected = new Set<NodeId>();
  for (const [a, b] of worsened) {
    if (oldPrev.get(b) === a && !affected.has(b)) {
      const stack = [b];
      while (stack.length > 0) {
        const n = stack.pop() as NodeId;
        if (affected.has(n)) continue;
        affected.add(n);
        for (const c of children.get(n) ?? []) stack.push(c);
      }
    }
  }

  for (const n of affected) {
    dist.set(n, Infinity);
    prev.set(n, null);
  }
  for (const n of graph.keys()) {
    if (!dist.has(n)) {
      dist.set(n, Infinity);
      prev.set(n, null);
    }
  }
  dist.set(source, 0);
  prev.set(source, null);

  const heap = new MinHeap();
  for (const [n, d] of dist) {
    if (d < Infinity) heap.push(n, d);
  }
  relax(graph, dist, prev, heap);
  return { dist, prev, affectedCount: affected.size };
}
