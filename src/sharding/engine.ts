import { Router } from "./router.ts";
import { ShardMap } from "./shardmap.ts";
import type { ShardTable } from "./shardmap.ts";
import { Topology } from "./topology.ts";
import type { ClientLoc, TopoNode } from "./topology.ts";
import type { RouteDecision } from "./router.ts";

export interface EngineStats {
  members: number;
  version: number;
  totalAssignments: number;
  perNode: ReadonlyMap<string, number>;
}

/**
 * Hierarchical sharding engine: owns membership, rebuilds immutable
 * routing tables on change, and serves locality-aware routes.
 */
export class ShardEngine {
  private readonly topology = new Topology();
  private readonly shardMap: ShardMap;
  private readonly router: Router;
  private readonly members = new Map<string, TopoNode>();
  private table: ShardTable | null = null;

  constructor(vshardCount: number, replicaFactor: number) {
    this.shardMap = new ShardMap(vshardCount, replicaFactor);
    this.router = new Router(this.topology, (key) => this.shardMap.shardOf(key));
  }

  join(node: TopoNode): void {
    this.members.set(node.id, node);
    this.topology.addNode(node);
    this.rebalance();
  }

  leave(nodeId: string): void {
    this.members.delete(nodeId);
    this.topology.removeNode(nodeId);
    this.rebalance();
  }

  rebalance(): void {
    this.table = this.shardMap.build([...this.members.values()]);
    this.router.update(this.table, this.members.keys());
  }

  route(key: string, client: ClientLoc): RouteDecision {
    return this.router.route(key, client);
  }

  shardOf(key: string): number {
    return this.shardMap.shardOf(key);
  }

  getTable(): ShardTable | null {
    return this.table;
  }

  get memberCount(): number {
    return this.members.size;
  }

  get version(): number {
    return this.table?.version ?? 0;
  }

  stats(): EngineStats {
    const perNode = new Map<string, number>();
    let total = 0;
    if (this.table) {
      for (const replicas of this.table.assignments.values()) {
        for (const id of replicas) {
          perNode.set(id, (perNode.get(id) ?? 0) + 1);
          total++;
        }
      }
    }
    return {
      members: this.members.size,
      version: this.table?.version ?? 0,
      totalAssignments: total,
      perNode,
    };
  }

  /** Returns a list of invariant violations; empty means healthy. */
  invariants(): string[] {
    const violations: string[] = [];
    if (!this.table) return violations;
    const expected = Math.min(this.table.replicaFactor, this.members.size);
    const zoneCount = new Set(
      [...this.members.values()].map((n) => `${n.region}/${n.zone}`),
    ).size;
    for (const [shard, replicas] of this.table.assignments) {
      if (replicas.length !== expected) {
        violations.push(`shard ${shard}: ${replicas.length} replicas, expected ${expected}`);
      }
      if (new Set(replicas).size !== replicas.length) {
        violations.push(`shard ${shard}: duplicate replicas`);
      }
      for (const id of replicas) {
        if (!this.members.has(id)) {
          violations.push(`shard ${shard}: replica ${id} is not a member`);
        }
      }
      const zones = new Set(
        replicas.map((id) => {
          const member = this.members.get(id);
          return member ? `${member.region}/${member.zone}` : "?";
        }),
      );
      const expectedZones = Math.min(expected, zoneCount);
      if (zones.size !== expectedZones) {
        violations.push(
          `shard ${shard}: spans ${zones.size} zones, expected ${expectedZones}`,
        );
      }
    }
    return violations;
  }
}
