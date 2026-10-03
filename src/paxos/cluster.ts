import { Network } from './network.ts';
import { PaxosNode } from './node.ts';

/** A test/demo harness: N nodes, one network, one virtual clock. */
export class Cluster {
  readonly network = new Network();
  readonly nodes: PaxosNode[] = [];
  now = 0;

  constructor(size: number, opts: { leaseMs?: number } = {}) {
    for (let id = 0; id < size; id++) {
      const node = new PaxosNode(id, size, () => this.now, opts);
      this.nodes.push(node);
      this.network.register(node);
    }
  }

  pump(): number {
    return this.network.pumpUntilIdle();
  }

  advanceTime(ms: number): void {
    this.now += ms;
  }

  /** Applied logs must be identical on every node. */
  appliedLogs(): unknown[][] {
    return this.nodes.map((n) => n.applied.map((a) => a.value));
  }
}
