import { ShardRing, type RebuildResult } from './ring.ts';
import {
  COST_SAME_REGION,
  COST_SAME_ZONE,
  localityCost,
  type Location,
  type NodeInfo,
} from './topology.ts';

export class RoutingError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'RoutingError';
  }
}

export type Locality = 'zone' | 'region' | 'remote';

export interface RouteDecision {
  key: string;
  shardId: number;
  nodeId: string;
  cost: number;
  locality: Locality;
}

export interface BatchPlan {
  batches: Map<string, string[]>;
  decisions: RouteDecision[];
  nodeHops: number;
  crossZoneKeys: number;
  crossRegionKeys: number;
}

export interface RouterStats {
  requests: number;
  zoneLocal: number;
  regionLocal: number;
  remote: number;
  inFlight: number;
  pendingMutations: number;
  pendingDrainWaiters: number;
  version: number;
  nodes: number;
  downNodes: number;
}

class AsyncMutex {
  private chain: Promise<void> = Promise.resolve();
  private pending = 0;

  get pendingCount(): number {
    return this.pending;
  }

  async acquire(): Promise<() => void> {
    this.pending++;
    const previous = this.chain;
    let release!: () => void;
    this.chain = new Promise<void>((resolve) => {
      release = resolve;
    });
    await previous;
    let released = false;
    return () => {
      if (released) return;
      released = true;
      this.pending--;
      release();
    };
  }
}

export class LocalityAwareRouter {
  private readonly nodes = new Map<string, NodeInfo>();
  private readonly down = new Set<string>();
  private readonly ring: ShardRing;
  private readonly mutex = new AsyncMutex();
  private drainWaiters: Array<() => void> = [];
  private inFlight = 0;
  private requests = 0;
  private zoneLocal = 0;
  private regionLocal = 0;
  private remote = 0;

  constructor(shardCount = 256, replicaCount = 2) {
    this.ring = new ShardRing(shardCount, replicaCount);
  }

  get version(): number {
    return this.ring.getVersion();
  }

  get inFlightCount(): number {
    return this.inFlight;
  }

  async addNode(node: NodeInfo): Promise<RebuildResult> {
    const release = await this.mutex.acquire();
    try {
      if (this.nodes.has(node.id)) throw new RoutingError(`duplicate node: ${node.id}`);
      this.nodes.set(node.id, node);
      return this.ring.rebuild([...this.nodes.values()]);
    } finally {
      release();
    }
  }

  async removeNode(nodeId: string): Promise<RebuildResult> {
    const release = await this.mutex.acquire();
    try {
      if (!this.nodes.delete(nodeId)) throw new RoutingError(`unknown node: ${nodeId}`);
      this.down.delete(nodeId);
      return this.ring.rebuild([...this.nodes.values()]);
    } finally {
      release();
    }
  }

  setNodeHealth(nodeId: string, healthy: boolean): void {
    if (!this.nodes.has(nodeId)) throw new RoutingError(`unknown node: ${nodeId}`);
    if (healthy) this.down.delete(nodeId);
    else this.down.add(nodeId);
  }

  route(key: string, client: Location): RouteDecision {
    const shardId = this.ring.shardOf(key);
    const candidates = this.ring
      .holdersOf(shardId)
      .filter((nodeId) => !this.down.has(nodeId));
    if (candidates.length === 0) {
      throw new RoutingError(`no available replica for shard ${shardId}`);
    }
    let bestNode = '';
    let bestCost = Number.POSITIVE_INFINITY;
    for (const nodeId of candidates) {
      const node = this.nodes.get(nodeId);
      if (!node) continue;
      const cost = localityCost(client, node);
      if (cost < bestCost || (cost === bestCost && nodeId < bestNode)) {
        bestNode = nodeId;
        bestCost = cost;
      }
    }
    if (bestNode === '') throw new RoutingError(`no available replica for shard ${shardId}`);
    const locality: Locality =
      bestCost <= COST_SAME_ZONE ? 'zone' : bestCost <= COST_SAME_REGION ? 'region' : 'remote';
    this.requests++;
    if (locality === 'zone') this.zoneLocal++;
    else if (locality === 'region') this.regionLocal++;
    else this.remote++;
    return { key, shardId, nodeId: bestNode, cost: bestCost, locality };
  }

  routeBatch(keys: readonly string[], client: Location): BatchPlan {
    const batches = new Map<string, string[]>();
    const decisions: RouteDecision[] = [];
    let crossZoneKeys = 0;
    let crossRegionKeys = 0;
    for (const key of keys) {
      const decision = this.route(key, client);
      decisions.push(decision);
      if (decision.locality !== 'zone') crossZoneKeys++;
      if (decision.locality === 'remote') crossRegionKeys++;
      const bucket = batches.get(decision.nodeId);
      if (bucket) bucket.push(key);
      else batches.set(decision.nodeId, [key]);
    }
    return { batches, decisions, nodeHops: batches.size, crossZoneKeys, crossRegionKeys };
  }

  async withRouted<T>(
    key: string,
    client: Location,
    task: (decision: RouteDecision) => T | Promise<T>,
  ): Promise<T> {
    const decision = this.route(key, client);
    this.inFlight++;
    try {
      return await task(decision);
    } finally {
      this.inFlight--;
      if (this.inFlight === 0 && this.drainWaiters.length > 0) {
        const waiters = this.drainWaiters;
        this.drainWaiters = [];
        for (const resolve of waiters) resolve();
      }
    }
  }

  async drain(): Promise<void> {
    while (this.inFlight > 0) {
      await new Promise<void>((resolve) => this.drainWaiters.push(resolve));
    }
  }

  stats(): RouterStats {
    return {
      requests: this.requests,
      zoneLocal: this.zoneLocal,
      regionLocal: this.regionLocal,
      remote: this.remote,
      inFlight: this.inFlight,
      pendingMutations: this.mutex.pendingCount,
      pendingDrainWaiters: this.drainWaiters.length,
      version: this.ring.getVersion(),
      nodes: this.nodes.size,
      downNodes: this.down.size,
    };
  }
}
