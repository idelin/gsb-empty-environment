import type { ShardMap } from './shardmap.ts';
import { Topology } from './topology.ts';

export interface Route {
  key: string;
  vshard: number;
  nodeId: string;
  cost: number;
  /** True when the chosen replica is in the client's zone (cost <= 1). */
  local: boolean;
}

export interface RouterStats {
  localReads: number;
  remoteReads: number;
}

/**
 * Locality-aware router. Reads are served by the replica closest to the
 * client in the hierarchical topology; writes always go to the primary.
 * Snapshots of the ShardMap are captured per call, so concurrent rebalances
 * never tear a routing decision.
 */
export class Router {
  private readonly topology: Topology;
  private readonly getMap: () => ShardMap;
  private localReads = 0;
  private remoteReads = 0;

  constructor(topology: Topology, getMap: () => ShardMap) {
    this.topology = topology;
    this.getMap = getMap;
  }

  routeWrite(key: string): Route {
    const map = this.getMap();
    const vshard = map.vshardOf(key);
    const replicas = map.replicasOfVshard(vshard);
    if (replicas.length === 0) throw new Error('no nodes available for routing');
    return { key, vshard, nodeId: replicas[0], cost: Topology.COST_SAME_NODE, local: true };
  }

  routeRead(key: string, clientNodeId?: string): Route {
    const map = this.getMap();
    const vshard = map.vshardOf(key);
    const replicas = map.replicasOfVshard(vshard);
    if (replicas.length === 0) throw new Error('no nodes available for routing');
    let client = undefined;
    if (clientNodeId !== undefined) {
      client = this.topology.get(clientNodeId);
      if (client === undefined) throw new Error(`unknown client node: ${clientNodeId}`);
    }
    let best = replicas[0];
    let bestCost = Infinity;
    for (const id of replicas) {
      const node = this.topology.get(id);
      if (node === undefined) continue; // stale assignment mid-rebalance
      const cost = client === undefined ? Topology.COST_SAME_NODE : this.topology.cost(client, node);
      if (cost < bestCost) {
        bestCost = cost;
        best = id;
      }
    }
    const local = bestCost <= Topology.COST_SAME_ZONE;
    if (local) this.localReads++;
    else this.remoteReads++;
    return {
      key,
      vshard,
      nodeId: best,
      cost: bestCost === Infinity ? Topology.COST_SAME_NODE : bestCost,
      local,
    };
  }

  /**
   * Group keys by their target node so a scheduler can batch traffic and
   * minimize cross-node (especially cross-region) round trips.
   */
  planBatch(keys: readonly string[], clientNodeId?: string): Map<string, string[]> {
    const plan = new Map<string, string[]>();
    for (const key of keys) {
      const route = this.routeRead(key, clientNodeId);
      const bucket = plan.get(route.nodeId);
      if (bucket === undefined) plan.set(route.nodeId, [key]);
      else bucket.push(key);
    }
    return plan;
  }

  get stats(): RouterStats {
    return { localReads: this.localReads, remoteReads: this.remoteReads };
  }

  resetStats(): void {
    this.localReads = 0;
    this.remoteReads = 0;
  }
}
