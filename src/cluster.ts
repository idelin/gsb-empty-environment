import { ClusterNode } from './node.ts';
import { LossyTransport } from './transport.ts';
import type { NodeId, NodeState } from './types.ts';

export class Cluster {
  readonly transport: LossyTransport;
  readonly nodes = new Map<NodeId, ClusterNode>();
  readonly order: NodeId[] = [];

  constructor(size: number, opts: { dropRate: number; seed: number }) {
    this.transport = new LossyTransport(opts.dropRate, opts.seed);
    const membership: NodeState[] = [];
    for (let i = 0; i < size; i++) {
      membership.push({ nodeId: `n${i}`, status: 'alive', version: 1, updatedBy: `n${i}` });
    }
    for (let i = 0; i < size; i++) {
      const id = `n${i}`;
      this.order.push(id);
      this.nodes.set(id, new ClusterNode(id, this.transport, opts.seed * 7919 + i * 131 + 7, membership));
    }
    for (const node of this.nodes.values()) node.bind(this.nodes, this.order);
  }

  get(id: NodeId): ClusterNode {
    const node = this.nodes.get(id);
    if (!node) throw new Error(`unknown node ${id}`);
    return node;
  }

  gossipRounds(n: number): void {
    for (let r = 0; r < n; r++) {
      for (const node of this.nodes.values()) node.gossipRound();
    }
  }

  /** Take a node offline; a detector node declares it dead and gossips the event. */
  killNode(id: NodeId, detectorId: NodeId): void {
    this.get(id).shutdown();
    this.get(detectorId).declareDead(id);
  }

  antiEntropyRound(): void {
    for (const node of this.nodes.values()) node.antiEntropyTick();
  }

  aliveNodes(): ClusterNode[] {
    return [...this.nodes.values()].filter((n) => n.alive);
  }

  converged(): boolean {
    const roots = new Set(this.aliveNodes().map((n) => n.rootHash()));
    return roots.size === 1;
  }

  runAntiEntropyUntilConverged(maxRounds = 50): number {
    let rounds = 0;
    while (!this.converged() && rounds < maxRounds) {
      this.antiEntropyRound();
      rounds++;
    }
    return rounds;
  }

  startAntiEntropy(intervalMs: number): void {
    for (const node of this.aliveNodes()) node.startAntiEntropy(intervalMs);
  }

  stopAll(): void {
    for (const node of this.nodes.values()) node.stopAntiEntropy();
  }
}
