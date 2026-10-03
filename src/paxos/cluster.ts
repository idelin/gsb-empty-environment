import { ManualClock } from './clock.ts';
import { Network } from './network.ts';
import { PaxosNode } from './node.ts';

/**
 * Test/simulation harness wiring a set of PaxosNodes to a deterministic
 * Network and a shared ManualClock.
 */
export class Cluster {
  readonly network = new Network();
  readonly clock = new ManualClock();
  readonly nodes = new Map<string, PaxosNode>();
  private readonly leaseMs: number;

  constructor(ids: string[], options: { leaseMs?: number } = {}) {
    if (ids.length === 0) throw new Error('cluster needs at least one node');
    if (new Set(ids).size !== ids.length) throw new Error('duplicate node id');
    this.leaseMs = options.leaseMs ?? 1000;
    for (const id of ids) {
      this.nodes.set(
        id,
        new PaxosNode(id, ids, this.network, { clock: this.clock, leaseMs: this.leaseMs }),
      );
    }
  }

  node(id: string): PaxosNode {
    const node = this.nodes.get(id);
    if (node === undefined) throw new Error(`unknown node: ${id}`);
    return node;
  }

  leader(): PaxosNode | undefined {
    for (const node of this.nodes.values()) if (node.isLeader()) return node;
    return undefined;
  }

  /** Run a full election for the given node and deliver all messages. */
  elect(id: string): PaxosNode {
    this.node(id).campaign();
    this.network.flush();
    return this.node(id);
  }

  /** Propose through the current leader and deliver all messages. */
  propose(value: string): void {
    const leader = this.leader();
    if (leader === undefined) throw new Error('no leader elected');
    leader.propose(value);
    this.network.flush();
  }

  advanceClock(ms: number): void {
    this.clock.advance(ms);
  }

  dispose(): void {
    for (const node of this.nodes.values()) node.dispose();
    this.network.dispose();
  }
}
