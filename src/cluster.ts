import { ClusterNode } from "./node.ts";
import type { Dot } from "./types.ts";

export class Cluster<V = unknown> {
  readonly nodes: ClusterNode<V>[];

  constructor(nodeIds: string[]) {
    this.nodes = nodeIds.map((id) => new ClusterNode<V>(id));
  }

  node(id: string): ClusterNode<V> {
    const found = this.nodes.find((n) => n.id === id);
    if (!found) throw new Error(`unknown node: ${id}`);
    return found;
  }

  replicatedSet(originId: string, key: string, value: V): void {
    this.node(originId).set(key, value);
    for (const peer of this.nodes) {
      if (peer.id !== originId) peer.store.set(key, value);
    }
  }

  replicatedDelete(originId: string, key: string): Dot | null {
    const dot = this.node(originId).delete(key);
    if (dot) {
      for (const peer of this.nodes) {
        if (peer.id !== originId) peer.store.delete(key, dot);
      }
    }
    return dot;
  }

  gossipRound(excludeIds: string[] = []): void {
    for (const source of this.nodes) {
      if (excludeIds.includes(source.id)) continue;
      const vector = source.knowledgeVector();
      for (const target of this.nodes) {
        if (target.id !== source.id && !excludeIds.includes(target.id)) {
          target.receiveGossip(source.id, vector);
        }
      }
    }
  }

  gossipUntilConverged(maxRounds = 10): number {
    let rounds = 0;
    for (let i = 0; i < maxRounds; i++) {
      this.gossipRound();
      rounds += 1;
      if (this.converged()) break;
    }
    return rounds;
  }

  converged(): boolean {
    const ids = this.nodes.map((n) => n.id);
    return this.nodes.every((node) => {
      const frontier = node.frontier();
      return ids.every((id) => (frontier.get(id) ?? 0) >= this.node(id).clock);
    });
  }
}
