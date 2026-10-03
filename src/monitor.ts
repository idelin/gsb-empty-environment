import { WeightedGraph, type NodeId } from './graph.ts';
import {
  betweennessCentrality,
  shortestPath,
  type CentralityResult,
} from './betweenness.ts';

export interface MonitorOptions {
  /** 跟踪的瓶颈节点数量，默认 5。 */
  topK?: number;
  /** 近似模式的抽样源节点数；不设置则精确计算。 */
  sampleSize?: number;
  /** 抽样随机种子，默认 42。 */
  seed?: number;
}

export interface BottleneckEntry {
  rank: number;
  node: NodeId;
  score: number;
}

export interface EdgeBottleneckEntry {
  rank: number;
  edge: [NodeId, NodeId];
  score: number;
}

export interface DiversionSuggestion {
  node: NodeId;
  /** 故障后改道路径经过该节点的次数。 */
  absorbedPaths: number;
  /** 占全部改道路径的比例。 */
  share: number;
}

export interface FailureReport {
  failedNode: NodeId;
  mode: 'remove' | 'degrade';
  before: BottleneckEntry[];
  after: BottleneckEntry[];
  /** 原最短路径经过故障节点的节点对数量。 */
  reroutedPairs: number;
  /** 改道后仍不可达的节点对数量。 */
  disconnectedPairs: number;
  primarySubstitute: NodeId | null;
  diversions: DiversionSuggestion[];
  /** 故障后新进入 Top-K 的节点。 */
  newBottlenecks: NodeId[];
  summary: string[];
}

/**
 * 瓶颈监控器：缓存介数结果，边权动态变动时标记失效并增量重算，
 * 始终可输出当前介数最高的 Top-K 瓶颈节点/边。
 */
export class BottleneckMonitor {
  readonly graph: WeightedGraph;
  private topK: number;
  private sampleSize?: number;
  private seed: number;
  private cache: CentralityResult | null = null;

  constructor(graph: WeightedGraph, options: MonitorOptions = {}) {
    this.graph = graph;
    this.topK = options.topK ?? 5;
    this.sampleSize = options.sampleSize;
    this.seed = options.seed ?? 42;
  }

  /** 当前介数结果（缓存；图变更后自动重算）。 */
  analyze(): CentralityResult {
    if (!this.cache) {
      this.cache = betweennessCentrality(this.graph, {
        sampleSize: this.sampleSize,
        seed: this.seed,
      });
    }
    return this.cache;
  }

  private invalidate(): void {
    this.cache = null;
  }

  topKNodes(k = this.topK): BottleneckEntry[] {
    const { nodeBC } = this.analyze();
    return [...nodeBC.entries()]
      .sort((a, b) => b[1] - a[1] || (a[0] < b[0] ? -1 : 1))
      .slice(0, k)
      .map(([node, score], i) => ({ rank: i + 1, node, score }));
  }

  topKEdges(k = this.topK): EdgeBottleneckEntry[] {
    const { edgeBC } = this.analyze();
    return [...edgeBC.entries()]
      .sort((a, b) => b[1] - a[1] || (a[0] < b[0] ? -1 : 1))
      .slice(0, k)
      .map(([key, score], i) => {
        const [u, v] = key.split('|');
        return { rank: i + 1, edge: [u, v] as [NodeId, NodeId], score };
      });
  }

  /** 边权动态变动：更新后重算并返回最新 Top-K。 */
  updateEdgeWeight(u: NodeId, v: NodeId, weight: number): BottleneckEntry[] {
    this.graph.updateEdgeWeight(u, v, weight);
    this.invalidate();
    return this.topKNodes();
  }

  addEdge(u: NodeId, v: NodeId, weight = 1): BottleneckEntry[] {
    this.graph.addEdge(u, v, weight);
    this.invalidate();
    return this.topKNodes();
  }

  removeEdge(u: NodeId, v: NodeId): BottleneckEntry[] {
    this.graph.removeEdge(u, v);
    this.invalidate();
    return this.topKNodes();
  }

  /**
   * 模拟节点故障（在图的副本上执行，不改动原图）：
   * - mode 'remove'：节点完全下线；
   * - mode 'degrade'：局部故障，关联边权放大 degradeFactor 倍。
   * 返回瓶颈转移报告与分流建议。
   */
  simulateNodeFailure(
    node: NodeId,
    options: { mode?: 'remove' | 'degrade'; degradeFactor?: number } = {},
  ): FailureReport {
    const mode = options.mode ?? 'remove';
    const degradeFactor = options.degradeFactor ?? 10;
    if (!this.graph.hasNode(node)) throw new Error(`node not found: ${node}`);

    const before = this.topKNodes();

    // 1. 找出故障前最短路径经过该节点的所有节点对。
    const nodes = this.graph.nodes().filter((n) => n !== node);
    const dependentPairs: Array<[NodeId, NodeId]> = [];
    for (let i = 0; i < nodes.length; i++) {
      for (let j = i + 1; j < nodes.length; j++) {
        const { path } = shortestPath(this.graph, nodes[i], nodes[j]);
        if (path && path.includes(node)) dependentPairs.push([nodes[i], nodes[j]]);
      }
    }

    // 2. 在副本上施加故障。
    const failed = this.graph.clone();
    if (mode === 'remove') {
      failed.removeNode(node);
    } else {
      for (const [nbr, w] of this.graph.neighbors(node)) {
        failed.updateEdgeWeight(node, nbr, w * degradeFactor);
      }
    }

    // 3. 故障后的介数与 Top-K。
    const afterMonitor = new BottleneckMonitor(failed, {
      topK: this.topK,
      sampleSize: this.sampleSize,
      seed: this.seed,
    });
    const after = afterMonitor.topKNodes();

    // 4. 对受影响的节点对重新选路，统计各节点承接的改道流量。
    const absorbed = new Map<NodeId, number>();
    let disconnectedPairs = 0;
    for (const [a, b] of dependentPairs) {
      const { path } = shortestPath(failed, a, b);
      if (!path) {
        disconnectedPairs++;
        continue;
      }
      for (const mid of path.slice(1, -1)) {
        if (mid !== node) absorbed.set(mid, (absorbed.get(mid) ?? 0) + 1);
      }
    }
    const totalAbsorbed = [...absorbed.values()].reduce((s, x) => s + x, 0);
    const diversions: DiversionSuggestion[] = [...absorbed.entries()]
      .sort((a, b) => b[1] - a[1] || (a[0] < b[0] ? -1 : 1))
      .map(([n, count]) => ({
        node: n,
        absorbedPaths: count,
        share: totalAbsorbed > 0 ? count / totalAbsorbed : 0,
      }));

    const beforeSet = new Set(before.map((b) => b.node));
    const newBottlenecks = after.map((a) => a.node).filter((n) => !beforeSet.has(n));
    const primarySubstitute = diversions.length > 0 ? diversions[0].node : null;

    const summary: string[] = [];
    summary.push(
      `故障节点 ${node}（${mode === 'remove' ? '完全下线' : `局部故障，边权 x${degradeFactor}`}）`,
    );
    summary.push(`受影响需改道的节点对：${dependentPairs.length}，不可达节点对：${disconnectedPairs}`);
    if (primarySubstitute) {
      const top = diversions[0];
      summary.push(
        `首要分流替代节点：${primarySubstitute}（承接 ${top.absorbedPaths} 条改道路径，占比 ${(top.share * 100).toFixed(1)}%）`,
      );
    } else {
      summary.push('无可用分流替代节点');
    }
    summary.push(
      `瓶颈 Top-${this.topK} 变化：[${before.map((b) => b.node).join(', ')}] -> [${after.map((a) => a.node).join(', ')}]`,
    );
    if (newBottlenecks.length > 0) summary.push(`新晋瓶颈节点：${newBottlenecks.join(', ')}`);

    return {
      failedNode: node,
      mode,
      before,
      after,
      reroutedPairs: dependentPairs.length,
      disconnectedPairs,
      primarySubstitute,
      diversions,
      newBottlenecks,
      summary,
    };
  }
}
