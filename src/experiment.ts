import { mulberry32 } from './prng.ts';
import { Simulator } from './simulator.ts';
import { Network } from './network.ts';
import { SubClusterNode, DEFAULT_OPTIONS } from './subClusterNode.ts';
import { FlatGossipNode } from './flatGossipNode.ts';
import { DEAD } from './types.ts';

export interface ExperimentConfig {
  nodeCount: number;
  cellSize: number;
  warmupMs: number;
  windowEndMs: number;
  crashAtMs: number;
  endMs: number;
  flatPeriodMs: number;
  flatFanout: number;
  seed: number;
}

export const DEFAULT_CONFIG: ExperimentConfig = {
  nodeCount: 1000,
  cellSize: 25,
  warmupMs: 10_000,
  windowEndMs: 40_000,
  crashAtMs: 45_000,
  endMs: 50_000,
  flatPeriodMs: 100,
  flatFanout: 3,
  seed: 42,
};

export interface ExperimentResult {
  nodeCount: number;
  cellCount: number;
  flatPerNodePerSec: number;
  subPerNodePerSec: number;
  reductionPct: number;
  crashNodeId: number;
  cellPeersNotified: number;
  cellPeerTotal: number;
  maxCellDetectionMs: number;
  crossCellLearnedMs: number | null;
}

function perNodeRate(totalPackets: number, nodeCount: number, windowMs: number): number {
  return totalPackets / (nodeCount * (windowMs / 1000));
}

export function runExperiment(config: ExperimentConfig = DEFAULT_CONFIG): ExperimentResult {
  const {
    nodeCount,
    cellSize,
    warmupMs,
    windowEndMs,
    crashAtMs,
    endMs,
    flatPeriodMs,
    flatFanout,
    seed,
  } = config;
  const windowMs = windowEndMs - warmupMs;

  // ---- Baseline: flat full-network gossip ----
  const flatSim = new Simulator();
  const flatNet = new Network(flatSim, nodeCount, mulberry32(seed));
  const flatNodes: FlatGossipNode[] = [];
  for (let i = 0; i < nodeCount; i++) {
    const node = new FlatGossipNode(
      i,
      nodeCount,
      flatNet,
      flatSim,
      mulberry32(seed * 100003 + i),
      flatPeriodMs,
      flatFanout,
    );
    flatNodes.push(node);
    node.start();
  }
  flatSim.runUntil(warmupMs);
  const flatBase = flatNet.totalPackets();
  flatSim.runUntil(windowEndMs);
  const flatRate = perNodeRate(flatNet.totalPackets() - flatBase, nodeCount, windowMs);

  // ---- Sub-cluster gossip ----
  const sim = new Simulator();
  const net = new Network(sim, nodeCount, mulberry32(seed + 1));
  const opts = { ...DEFAULT_OPTIONS, cellSize };
  const nodes: SubClusterNode[] = [];
  const detectedAt = new Map<number, number>(); // observer -> first detection time of victim
  const victim = 7; // arbitrary node inside cell 0
  for (let i = 0; i < nodeCount; i++) {
    const node = new SubClusterNode(i, nodeCount, net, sim, mulberry32(seed * 200003 + i), opts);
    node.onDeadDetected = (deadId, atMs) => {
      if (deadId !== victim) return;
      if (!detectedAt.has(i)) detectedAt.set(i, atMs);
    };
    nodes.push(node);
    node.start();
  }
  sim.runUntil(warmupMs);
  const subBase = net.totalPackets();
  sim.runUntil(windowEndMs);
  const subRate = perNodeRate(net.totalPackets() - subBase, nodeCount, windowMs);

  // Crash one node; transport delivers link resets to its ring neighbors.
  sim.schedule(crashAtMs - sim.now, () => {
    const crashed = nodes[victim];
    crashed.crash();
    for (const neighbor of crashed.ringNeighbors()) {
      net.send(victim, neighbor, { kind: 'linkReset', deadId: victim });
    }
  });
  sim.runUntil(endMs);

  const victimCellStart = Math.floor(victim / cellSize) * cellSize;
  let maxCellDetectionMs = 0;
  let cellPeersNotified = 0;
  let crossCellLearnedMs: number | null = null;
  for (const [observer, atMs] of detectedAt) {
    const latency = atMs - crashAtMs;
    const sameCell = observer >= victimCellStart && observer < victimCellStart + cellSize;
    if (sameCell) {
      cellPeersNotified++;
      if (latency > maxCellDetectionMs) maxCellDetectionMs = latency;
    } else if (crossCellLearnedMs === null || latency < crossCellLearnedMs) {
      crossCellLearnedMs = latency;
    }
  }

  return {
    nodeCount,
    cellCount: Math.ceil(nodeCount / cellSize),
    flatPerNodePerSec: flatRate,
    subPerNodePerSec: subRate,
    reductionPct: (1 - subRate / flatRate) * 100,
    crashNodeId: victim,
    cellPeersNotified,
    cellPeerTotal: cellSize - 1,
    maxCellDetectionMs,
    crossCellLearnedMs,
  };
}

export { DEAD };
