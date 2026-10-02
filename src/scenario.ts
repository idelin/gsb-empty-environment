/**
 * 可复用的模拟场景构建器（样例与测试共用）。
 */

import { Simulator, Network } from './sim.ts';
import { mulberry32 } from './rng.ts';
import { CellAssignment } from './membership.ts';
import {
  HierarchicalNode,
  DEFAULT_HIERARCHICAL_CONFIG,
  type HierarchicalConfig,
} from './hierarchical.ts';
import {
  FlatBroadcastNode,
  DEFAULT_FLAT_CONFIG,
  type FlatConfig,
} from './flat.ts';

export interface HierarchicalScenario {
  sim: Simulator;
  net: Network;
  assignment: CellAssignment;
  cells: number[][];
  nodes: HierarchicalNode[];
}

export function buildHierarchical(
  nodeCount: number,
  seed: number,
  config: HierarchicalConfig = DEFAULT_HIERARCHICAL_CONFIG,
): HierarchicalScenario {
  const sim = new Simulator();
  const rand = mulberry32(seed);
  // 网络延迟 1~3ms（同机房量级）
  const net = new Network(sim, nodeCount, () => 1 + rand() * 2);
  const assignment = new CellAssignment(CellAssignment.recommendedCells(nodeCount));
  const ids = Array.from({ length: nodeCount }, (_, i) => i);
  const cells = assignment.partition(ids);
  const nodes: HierarchicalNode[] = [];
  for (const id of ids) {
    nodes.push(
      new HierarchicalNode({
        id,
        nodeCount,
        sim,
        net,
        config,
        assignment,
        cells,
        nodes,
        rand: mulberry32(seed * 1000003 + id),
      }),
    );
  }
  for (const node of nodes) node.start();
  return { sim, net, assignment, cells, nodes };
}

export interface FlatScenario {
  sim: Simulator;
  net: Network;
  nodes: FlatBroadcastNode[];
}

export function buildFlat(
  nodeCount: number,
  seed: number,
  config: FlatConfig = DEFAULT_FLAT_CONFIG,
): FlatScenario {
  const sim = new Simulator();
  const rand = mulberry32(seed);
  // 扁平基线只关心包量：全网广播下每个发出的包必被唯一对端接收，
  // 用 deliver=false 计数模式避免调度千万级投递事件。
  const net = new Network(sim, nodeCount, () => 1 + rand() * 2, 5, false);
  const nodes: FlatBroadcastNode[] = [];
  for (let id = 0; id < nodeCount; id++) {
    nodes.push(
      new FlatBroadcastNode({
        id,
        nodeCount,
        sim,
        net,
        config,
        nodes,
        rand: mulberry32(seed * 1000003 + id),
      }),
    );
  }
  for (const node of nodes) node.start();
  return { sim, net, nodes };
}
