import { Clock } from './clock.ts';
import { Network } from './network.ts';
import { RaftNode, type NodeId } from './raft.ts';

export interface MemberConfig {
  id: NodeId;
  electionTimeout: number;
}

/**
 * Wires nodes, network and clock into one single-process cluster.
 * Each clock tick: nodes run their timers, then due messages are delivered.
 */
export class Cluster {
  readonly clock = new Clock();
  readonly network: Network;
  readonly nodes = new Map<NodeId, RaftNode>();
  readonly heartbeatInterval: number;

  constructor(members: MemberConfig[], heartbeatInterval = 4) {
    this.heartbeatInterval = heartbeatInterval;
    this.network = new Network(this.clock);
    const ids = members.map((m) => m.id);
    for (const m of members) {
      this.nodes.set(
        m.id,
        new RaftNode({
          id: m.id,
          members: ids,
          electionTimeout: m.electionTimeout,
          heartbeatInterval,
          clock: this.clock,
          network: this.network,
        }),
      );
    }
    this.clock.onTick(() => {
      for (const node of this.nodes.values()) node.onTick();
      this.network.deliverDue();
    });
  }

  node(id: NodeId): RaftNode {
    const node = this.nodes.get(id);
    if (!node) throw new Error(`unknown node ${id}`);
    return node;
  }

  run(ticks: number): void {
    this.clock.run(ticks);
  }

  leader(): RaftNode | undefined {
    for (const node of this.nodes.values()) {
      if (node.role === 'leader') return node;
    }
    return undefined;
  }

  isolate(id: NodeId): void {
    this.network.isolate(id);
  }

  heal(id: NodeId): void {
    this.network.heal(id);
  }
}
