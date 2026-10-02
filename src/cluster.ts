import { GossipNode } from './node.ts';
import { mulberry32 } from './prng.ts';
import type { MemberEvent, NodeStatus } from './types.ts';

export class Cluster {
  readonly nodes: GossipNode[] = [];
  private readonly rng: () => number;
  period = 0;

  constructor(size: number, seed = 42) {
    this.rng = mulberry32(seed);
    for (let i = 0; i < size; i++) {
      this.nodes.push(new GossipNode(`node-${i}`, size + 1));
    }
    const roster: MemberEvent[] = this.nodes.map((n) => ({
      type: 'join',
      nodeId: n.id,
      incarnation: 0,
    }));
    for (const node of this.nodes) {
      for (const event of roster) node.table.apply(event);
    }
  }

  join(newNode: GossipNode, seedIndex = 0): void {
    this.nodes[seedIndex].ingest({
      type: 'join',
      nodeId: newNode.id,
      incarnation: newNode.incarnation,
    });
  }

  runPeriod(): void {
    this.period++;
    const order = [...this.nodes];
    for (let i = order.length - 1; i > 0; i--) {
      const j = Math.floor(this.rng() * (i + 1));
      [order[i], order[j]] = [order[j], order[i]];
    }
    for (const node of order) {
      const peer = this.pickPeer(node);
      const ping = node.buildPing();
      const ack = peer.handlePing(ping);
      node.handleAck(ack);
    }
  }

  private pickPeer(node: GossipNode): GossipNode {
    let peer = node;
    while (peer === node) {
      peer = this.nodes[Math.floor(this.rng() * this.nodes.length)];
    }
    return peer;
  }

  countAware(nodeId: string, status: NodeStatus = 'alive'): number {
    return this.nodes.filter((n) => n.table.statusOf(nodeId) === status).length;
  }
}
