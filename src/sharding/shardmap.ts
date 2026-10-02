import { rendezvousScore } from "./hash.ts";
import type { TopoNode } from "./topology.ts";
import { fnv1a } from "./hash.ts";

export interface ShardTable {
  readonly version: number;
  readonly vshardCount: number;
  readonly replicaFactor: number;
  readonly assignments: ReadonlyMap<number, readonly string[]>;
}

/**
 * Virtual-shard placement. Keys map to a fixed number of virtual shards;
 * each shard is placed on `replicaFactor` distinct nodes with rendezvous
 * (highest-random-weight) hashing, which minimizes movement when
 * membership changes. Placement prefers distinct zones so every shard
 * survives a zone failure and stays reachable within a client's region.
 */
export class ShardMap {
  readonly vshardCount: number;
  readonly replicaFactor: number;
  private version = 0;

  constructor(vshardCount: number, replicaFactor: number) {
    if (vshardCount <= 0) throw new Error("vshardCount must be positive");
    if (replicaFactor <= 0) throw new Error("replicaFactor must be positive");
    this.vshardCount = vshardCount;
    this.replicaFactor = replicaFactor;
  }

  shardOf(key: string): number {
    return fnv1a(key) % this.vshardCount;
  }

  build(nodes: TopoNode[]): ShardTable {
    this.version++;
    const assignments = new Map<number, readonly string[]>();
    const target = Math.min(this.replicaFactor, nodes.length);
    for (let shard = 0; shard < this.vshardCount; shard++) {
      assignments.set(shard, this.place(shard, nodes, target));
    }
    const table: ShardTable = {
      version: this.version,
      vshardCount: this.vshardCount,
      replicaFactor: this.replicaFactor,
      assignments,
    };
    return table;
  }

  private place(shard: number, nodes: TopoNode[], target: number): string[] {
    if (target === 0) return [];
    const scored = nodes
      .map((node) => ({ node, score: rendezvousScore(shard, node.id) }))
      .sort((a, b) => b.score - a.score || (a.node.id < b.node.id ? -1 : 1));
    const chosen: string[] = [];
    const usedZones = new Set<string>();
    // Pass 1: spread replicas across distinct zones.
    for (const { node } of scored) {
      if (chosen.length >= target) break;
      const zoneKey = `${node.region}/${node.zone}`;
      if (!usedZones.has(zoneKey)) {
        chosen.push(node.id);
        usedZones.add(zoneKey);
      }
    }
    // Pass 2: fill remaining slots by weight.
    for (const { node } of scored) {
      if (chosen.length >= target) break;
      if (!chosen.includes(node.id)) chosen.push(node.id);
    }
    return chosen;
  }
}
