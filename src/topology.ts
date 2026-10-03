import { WeightedGraph, type NodeId } from './graph.ts';

export interface HybridNetwork {
  graph: WeightedGraph;
  gateway: NodeId;
  leaves: NodeId[];
  meshNodes: NodeId[];
}

/** 星型拓扑：1 个中心 + leafCount 个叶子。 */
export function buildStarNetwork(leafCount: number, center: NodeId = 'C'): WeightedGraph {
  const g = new WeightedGraph();
  for (let i = 1; i <= leafCount; i++) g.addEdge(center, `L${i}`, 1);
  return g;
}

/** 网状拓扑：n 个节点全连接。 */
export function buildMeshNetwork(n: number, prefix = 'M'): WeightedGraph {
  const g = new WeightedGraph();
  for (let i = 1; i <= n; i++) g.addNode(`${prefix}${i}`);
  for (let i = 1; i <= n; i++) {
    for (let j = i + 1; j <= n; j++) g.addEdge(`${prefix}${i}`, `${prefix}${j}`, 1);
  }
  return g;
}

/**
 * 星型 + 网状混合网络：
 * - 中心网关 GW 以权重 1 连接全部叶子（星型部分）；
 * - GW 与核心节点 A/B/C/D 构成网状骨干，GW 链路权重 1，骨干间链路权重 1.5，
 *   因此正常流量优先汇聚到 GW；
 * - 每个叶子另有一条高权重（3）的备用链路接入骨干，平时闲置，故障时启用。
 */
export function buildHybridNetwork(): HybridNetwork {
  const g = new WeightedGraph();
  const gateway = 'GW';
  const meshNodes = ['A', 'B', 'C', 'D'];
  const leaves = ['S1', 'S2', 'S3', 'S4', 'S5', 'S6'];

  for (const m of meshNodes) g.addEdge(gateway, m, 1);
  for (let i = 0; i < meshNodes.length; i++) {
    for (let j = i + 1; j < meshNodes.length; j++) {
      g.addEdge(meshNodes[i], meshNodes[j], 1.5);
    }
  }
  for (let i = 0; i < leaves.length; i++) {
    g.addEdge(gateway, leaves[i], 1);
    g.addEdge(leaves[i], meshNodes[i % meshNodes.length], 3); // 备用链路
  }
  return { graph: g, gateway, leaves, meshNodes };
}
