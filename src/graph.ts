import type { WeightedGraph } from "./types.ts";

// All-pairs shortest paths (Floyd-Warshall) with path reconstruction.
export class RoadGraph {
  readonly nodes: string[];
  private readonly indexOf: Map<string, number>;
  private readonly dist: number[][];
  private readonly nextHop: number[][];

  constructor(graph: WeightedGraph) {
    this.nodes = [...graph.nodes];
    this.indexOf = new Map(this.nodes.map((name, i) => [name, i]));
    const n = this.nodes.length;
    this.dist = Array.from({ length: n }, (_, i) =>
      Array.from({ length: n }, (_, j) => (i === j ? 0 : Infinity)),
    );
    this.nextHop = Array.from({ length: n }, () => new Array<number>(n).fill(-1));
    for (const edge of graph.edges) {
      const u = this.nodeIndex(edge.from);
      const v = this.nodeIndex(edge.to);
      if (edge.dist < this.dist[u][v]) {
        this.dist[u][v] = edge.dist;
        this.dist[v][u] = edge.dist;
        this.nextHop[u][v] = v;
        this.nextHop[v][u] = u;
      }
    }
    for (let k = 0; k < n; k++) {
      for (let i = 0; i < n; i++) {
        if (!isFinite(this.dist[i][k])) continue;
        for (let j = 0; j < n; j++) {
          const cand = this.dist[i][k] + this.dist[k][j];
          if (cand < this.dist[i][j]) {
            this.dist[i][j] = cand;
            this.nextHop[i][j] = this.nextHop[i][k];
          }
        }
      }
    }
  }

  private nodeIndex(name: string): number {
    const idx = this.indexOf.get(name);
    if (idx === undefined) throw new Error(`unknown graph node: ${name}`);
    return idx;
  }

  distance(a: string, b: string): number {
    const d = this.dist[this.nodeIndex(a)][this.nodeIndex(b)];
    if (!isFinite(d)) throw new Error(`no path between ${a} and ${b}`);
    return d;
  }

  // Node sequence of a shortest path from a to b, inclusive.
  path(a: string, b: string): string[] {
    let u = this.nodeIndex(a);
    const v = this.nodeIndex(b);
    if (this.nextHop[u][v] === -1 && u !== v) {
      throw new Error(`no path between ${a} and ${b}`);
    }
    const out: string[] = [this.nodes[u]];
    while (u !== v) {
      u = this.nextHop[u][v];
      out.push(this.nodes[u]);
    }
    return out;
  }
}
