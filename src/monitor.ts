import { WeightedGraph } from "./graph.ts";
import { approximateBetweenness, brandes, edgeKey, shortestPath } from "./betweenness.ts";

export interface BottleneckEntry {
  node: string;
  score: number;
}

export interface EdgeBottleneckEntry {
  edge: [string, string];
  score: number;
}

export interface ReroutedPath {
  from: string;
  to: string;
  oldPath: string[];
  newPath: string[];
}

export interface RerouteSuggestion {
  failedNode: string;
  affectedPairs: number;
  disconnectedPairs: number;
  alternatives: Array<{ node: string; redirectedFlows: number; share: number }>;
  primaryAlternative: string | null;
  sampleReroutes: ReroutedPath[];
}

export interface BottleneckShiftReport {
  failedNode: string;
  previousTopK: BottleneckEntry[];
  newTopK: BottleneckEntry[];
  biggestGainers: Array<{ node: string; delta: number }>;
  reroute: RerouteSuggestion;
}

export interface MonitorOptions {
  k?: number;
  approximate?: boolean;
  samples?: number;
  seed?: number;
}

export class BottleneckMonitor {
  private readonly graph: WeightedGraph;
  private readonly k: number;
  private readonly approximate: boolean;
  private readonly samples: number;
  private readonly seed: number;
  private nodeScores = new Map<string, number>();
  private edgeScores = new Map<string, number>();

  constructor(graph: WeightedGraph, options: MonitorOptions = {}) {
    this.graph = graph;
    this.k = options.k ?? 5;
    this.approximate = options.approximate ?? false;
    this.samples = options.samples ?? 16;
    this.seed = options.seed ?? 42;
    this.recompute();
  }

  recompute(): void {
    const result = this.approximate
      ? approximateBetweenness(this.graph, this.samples, this.seed)
      : brandes(this.graph);
    this.nodeScores = result.node;
    this.edgeScores = result.edge;
  }

  updateEdgeWeight(u: string, v: string, weight: number): BottleneckEntry[] {
    this.graph.setWeight(u, v, weight);
    this.recompute();
    return this.topK();
  }

  topK(): BottleneckEntry[] {
    return [...this.nodeScores.entries()]
      .map(([node, score]) => ({ node, score }))
      .sort((a, b) => b.score - a.score || a.node.localeCompare(b.node))
      .slice(0, this.k);
  }

  topKEdges(k = this.k): EdgeBottleneckEntry[] {
    return [...this.edgeScores.entries()]
      .map(([key, score]) => {
        const [a, b] = key.split("|");
        return { edge: [a, b] as [string, string], score };
      })
      .sort((a, b) => b.score - a.score || a.edge[0].localeCompare(b.edge[0]))
      .slice(0, k);
  }

  scoreOf(node: string): number {
    return this.nodeScores.get(node) ?? 0;
  }

  edgeScoreOf(u: string, v: string): number {
    return this.edgeScores.get(edgeKey(u, v)) ?? 0;
  }

  suggestReroutes(failedNode: string, maxSamples = 10): RerouteSuggestion {
    const others = this.graph.nodes().filter((n) => n !== failedNode);
    const affected: Array<{ s: string; t: string; oldPath: string[] }> = [];
    for (let i = 0; i < others.length; i++) {
      for (let j = i + 1; j < others.length; j++) {
        const path = shortestPath(this.graph, others[i], others[j]);
        if (path && path.includes(failedNode)) {
          affected.push({ s: others[i], t: others[j], oldPath: path });
        }
      }
    }

    const removed = this.graph.removeNode(failedNode);
    const counter = new Map<string, number>();
    const sampleReroutes: ReroutedPath[] = [];
    let disconnectedPairs = 0;
    for (const { s, t, oldPath } of affected) {
      const newPath = shortestPath(this.graph, s, t);
      if (!newPath) {
        disconnectedPairs++;
        continue;
      }
      for (const mid of newPath.slice(1, -1)) {
        counter.set(mid, (counter.get(mid) ?? 0) + 1);
      }
      if (sampleReroutes.length < maxSamples) {
        sampleReroutes.push({ from: s, to: t, oldPath, newPath });
      }
    }
    this.graph.restoreNode(failedNode, removed);

    const total = [...counter.values()].reduce((a, b) => a + b, 0);
    const alternatives = [...counter.entries()]
      .map(([node, redirectedFlows]) => ({
        node,
        redirectedFlows,
        share: total > 0 ? redirectedFlows / total : 0,
      }))
      .sort((a, b) => b.redirectedFlows - a.redirectedFlows || a.node.localeCompare(b.node));

    return {
      failedNode,
      affectedPairs: affected.length,
      disconnectedPairs,
      alternatives,
      primaryAlternative: alternatives[0]?.node ?? null,
      sampleReroutes,
    };
  }

  simulateNodeFailure(failedNode: string): BottleneckShiftReport {
    const previousTopK = this.topK();
    const previousScores = new Map(this.nodeScores);
    const reroute = this.suggestReroutes(failedNode);

    const removed = this.graph.removeNode(failedNode);
    this.recompute();
    const newTopK = this.topK();
    const biggestGainers = [...this.nodeScores.entries()]
      .map(([node, score]) => ({ node, delta: score - (previousScores.get(node) ?? 0) }))
      .filter((g) => g.delta > 0)
      .sort((a, b) => b.delta - a.delta || a.node.localeCompare(b.node))
      .slice(0, this.k);

    this.graph.restoreNode(failedNode, removed);
    this.recompute();

    return { failedNode, previousTopK, newTopK, biggestGainers, reroute };
  }
}
