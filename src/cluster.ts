import { PaxosNode } from './node.ts';
import { Network } from './network.ts';
import { ManualClock } from './clock.ts';
import { InvariantMonitor } from './invariants.ts';
import type { NodeId } from './types.ts';

export interface Cluster {
  nodes: PaxosNode[];
  net: Network;
  clock: ManualClock;
  monitor: InvariantMonitor;
  quorum: number;
  byId(id: NodeId): PaxosNode;
}

export function makeCluster(size = 5): Cluster {
  const clock = new ManualClock();
  const net = new Network();
  const monitor = new InvariantMonitor();
  const quorum = Math.floor(size / 2) + 1;
  const nodes: PaxosNode[] = [];
  for (let i = 0; i < size; i++) {
    nodes.push(new PaxosNode(`N${i + 1}`, net, clock, quorum));
  }
  for (const node of nodes) {
    node.monitor = monitor;
    net.register(node);
  }
  monitor.bind(nodes);
  return {
    nodes,
    net,
    clock,
    monitor,
    quorum,
    byId: (id) => {
      const node = nodes.find((n) => n.id === id);
      if (!node) throw new Error(`unknown node ${id}`);
      return node;
    },
  };
}
