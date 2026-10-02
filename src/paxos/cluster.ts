import { Network } from "./network.ts";
import { PaxosNode } from "./node.ts";

export class Cluster {
  readonly nodes = new Map<string, PaxosNode>();
  readonly network = new Network();
  readonly clock = { now: 0 };

  constructor(ids: string[], leaseDuration = 100) {
    for (const id of ids) {
      const peers = ids.filter((x) => x !== id);
      const node = new PaxosNode(id, peers, {
        leaseDuration,
        now: () => this.clock.now,
      });
      node.outbox = (msg) => this.network.send(msg);
      this.nodes.set(id, node);
    }
  }

  node(id: string): PaxosNode {
    const node = this.nodes.get(id);
    if (!node) throw new Error(`unknown node ${id}`);
    return node;
  }

  tick(): void {
    this.network.flush(this.nodes);
  }

  advanceTime(ms: number): void {
    this.clock.now += ms;
  }
}
