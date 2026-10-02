import type { ShardTable } from "./shardmap.ts";
import { Topology } from "./topology.ts";
import type { ClientLoc } from "./topology.ts";

export class NoReplicaError extends Error {
  constructor(shard: number) {
    super(`no alive replica for virtual shard ${shard}`);
    this.name = "NoReplicaError";
  }
}

export interface RouteDecision {
  readonly key: string;
  readonly vshard: number;
  readonly nodeId: string;
  readonly distance: number;
  readonly version: number;
  readonly replicas: readonly string[];
}

/**
 * Locality-aware router. Routing tables are immutable snapshots swapped
 * atomically, so concurrent readers always observe one consistent version.
 * The router keeps no per-request state.
 */
export class Router {
  private readonly topology: Topology;
  private readonly shardOf: (key: string) => number;
  private table: ShardTable | null = null;
  private alive = new Set<string>();

  constructor(topology: Topology, shardOf: (key: string) => number) {
    this.topology = topology;
    this.shardOf = shardOf;
  }

  update(table: ShardTable, alive: Iterable<string>): void {
    this.table = table;
    this.alive = new Set(alive);
  }

  get version(): number {
    return this.table?.version ?? 0;
  }

  route(key: string, client: ClientLoc): RouteDecision {
    const table = this.table;
    if (!table) throw new NoReplicaError(-1);
    const vshard = this.shardOf(key);
    const assigned = table.assignments.get(vshard) ?? [];
    const replicas = assigned.filter((id) => this.alive.has(id));
    if (replicas.length === 0) throw new NoReplicaError(vshard);
    let best = replicas[0];
    let bestDistance = Number.POSITIVE_INFINITY;
    for (const id of replicas) {
      const distance = this.topology.distance(client, id);
      if (distance < bestDistance || (distance === bestDistance && id < best)) {
        best = id;
        bestDistance = distance;
      }
    }
    return {
      key,
      vshard,
      nodeId: best,
      distance: bestDistance,
      version: table.version,
      replicas: Object.freeze([...replicas]),
    };
  }
}
