import { fnv1a32, shardHash } from './hash.ts';

export interface NodeInfo {
  id: string;
  region: string;
  zone: string;
  /** Relative capacity weight (>= 1). Heavier nodes host more virtual shards. */
  weight: number;
}

export interface ClientLocality {
  region?: string;
  zone?: string;
}

export const HopLocality = {
  /** Serving node sits in the client's zone. */
  Local: 'local',
  /** Same region, different zone. */
  Regional: 'regional',
  /** Different region — most expensive hop. */
  Remote: 'remote',
} as const;
export type HopLocalityValue = (typeof HopLocality)[keyof typeof HopLocality];

export interface RouteDecision {
  key: string;
  shardId: number;
  /** Replicas ordered by locality preference for the caller. */
  replicas: NodeInfo[];
  locality: HopLocalityValue;
  crossNodeHops: number;
}

export interface RouterMetrics {
  totalRoutes: number;
  localRoutes: number;
  regionalRoutes: number;
  remoteRoutes: number;
  crossNodeHops: number;
}

export interface RouterOptions {
  /** Number of virtual shards (power of two recommended). Default 1024. */
  shardCount?: number;
  /** Replicas per shard. Default 3. */
  replicaCount?: number;
}

/** Minimal async mutex: serializes topology mutations across await points. */
class AsyncMutex {
  private tail: Promise<void> = Promise.resolve();

  async run<T>(fn: () => T | Promise<T>): Promise<T> {
    const prev = this.tail;
    let release!: () => void;
    this.tail = new Promise<void>((resolve) => {
      release = resolve;
    });
    await prev;
    try {
      return await fn();
    } finally {
      release();
    }
  }
}

/**
 * Hierarchical sharding + locality-aware routing engine.
 *
 * - Fixed set of virtual shards; each shard maps to `replicaCount` nodes via
 *   weighted rendezvous (HRW) hashing, so adding/removing a node only moves
 *   ~1/N of shards (minimal reshuffling, no global rehash).
 * - Replica placement is hierarchy-aware: it prefers spreading replicas across
 *   distinct zones and regions (anti-affinity) to survive zone/region faults.
 * - Routing is locality-aware: replicas are reordered per caller so the
 *   closest copy (same zone > same region > remote) is tried first, which
 *   minimizes cross-node/cross-zone traffic.
 */
export class LocalityAwareRouter {
  private readonly shardCount: number;
  private readonly replicaCount: number;
  private readonly nodes = new Map<string, NodeInfo>();
  /** shardId -> ordered replica node ids (placement order, stable). */
  private placement: string[][] = [];
  private readonly mutex = new AsyncMutex();
  private readonly metrics: RouterMetrics = {
    totalRoutes: 0,
    localRoutes: 0,
    regionalRoutes: 0,
    remoteRoutes: 0,
    crossNodeHops: 0,
  };

  constructor(options: RouterOptions = {}) {
    this.shardCount = options.shardCount ?? 1024;
    this.replicaCount = options.replicaCount ?? 3;
    if (!Number.isInteger(this.shardCount) || this.shardCount <= 0) {
      throw new RangeError('shardCount must be a positive integer');
    }
    if (!Number.isInteger(this.replicaCount) || this.replicaCount <= 0) {
      throw new RangeError('replicaCount must be a positive integer');
    }
    this.rebuildPlacement();
  }

  get nodeCount(): number {
    return this.nodes.size;
  }

  get shardCountValue(): number {
    return this.shardCount;
  }

  getMetrics(): RouterMetrics {
    return { ...this.metrics };
  }

  resetMetrics(): void {
    this.metrics.totalRoutes = 0;
    this.metrics.localRoutes = 0;
    this.metrics.regionalRoutes = 0;
    this.metrics.remoteRoutes = 0;
    this.metrics.crossNodeHops = 0;
  }

  /** Register (or update) a node. Serialized against concurrent mutations. */
  async addNode(node: NodeInfo): Promise<void> {
    if (!node.id) throw new RangeError('node id must be non-empty');
    if (!Number.isFinite(node.weight) || node.weight < 1) {
      throw new RangeError(`node ${node.id}: weight must be >= 1`);
    }
    await this.mutex.run(() => {
      this.nodes.set(node.id, { ...node });
      this.rebuildPlacement();
    });
  }

  /** Remove a node and purge every reference to it (leak-free teardown). */
  async removeNode(nodeId: string): Promise<boolean> {
    return this.mutex.run(() => {
      const existed = this.nodes.delete(nodeId);
      if (existed) this.rebuildPlacement();
      return existed;
    });
  }

  /** Snapshot of current placement (defensive copy). */
  getPlacement(): string[][] {
    return this.placement.map((replicas) => [...replicas]);
  }

  /** Shard id for a key. */
  shardOf(key: string): number {
    return fnv1a32(key) % this.shardCount;
  }

  /**
   * Route a key to its replica set, ordered by locality for the caller.
   * Throws when the topology is empty.
   */
  route(key: string, client: ClientLocality = {}): RouteDecision {
    if (this.nodes.size === 0) {
      throw new Error('route: topology is empty, no nodes registered');
    }
    const shardId = this.shardOf(key);
    const replicaIds = this.placement[shardId];
    const replicas = replicaIds.map((id) => {
      const node = this.nodes.get(id);
      if (!node) throw new Error(`route: placement references unknown node ${id}`);
      return node;
    });

    const scored = replicas.map((node, index) => ({
      node,
      index,
      score: this.localityScore(node, client),
    }));
    scored.sort((a, b) => b.score - a.score || a.index - b.index);
    const ordered = scored.map((s) => s.node);

    const best = scored[0].score;
    const locality: HopLocalityValue =
      best === 2 ? HopLocality.Local : best === 1 ? HopLocality.Regional : HopLocality.Remote;
    const crossNodeHops = locality === HopLocality.Local ? 0 : 1;

    this.metrics.totalRoutes++;
    this.metrics.crossNodeHops += crossNodeHops;
    if (locality === HopLocality.Local) this.metrics.localRoutes++;
    else if (locality === HopLocality.Regional) this.metrics.regionalRoutes++;
    else this.metrics.remoteRoutes++;

    return { key, shardId, replicas: ordered, locality, crossNodeHops };
  }

  /** Deterministic placement check helper: replicas of a shard. */
  replicasOf(shardId: number): string[] {
    return [...this.placement[shardId]];
  }

  private localityScore(node: NodeInfo, client: ClientLocality): number {
    if (client.zone !== undefined && node.zone === client.zone && node.region === client.region) return 2;
    if (client.region !== undefined && node.region === client.region) return 1;
    return 0;
  }

  /**
   * Weighted rendezvous (HRW) hashing per shard with hierarchy-aware
   * anti-affinity: pick the highest-scoring nodes, preferring distinct zones
   * and regions for fault isolation. Deterministic; adding/removing a node
   * only re-scores shards that node could win, so movement stays ~1/N.
   */
  private rebuildPlacement(): void {
    const nodes = [...this.nodes.values()];
    const placement: string[][] = new Array(this.shardCount);
    for (let shard = 0; shard < this.shardCount; shard++) {
      placement[shard] = this.placeShard(shard, nodes);
    }
    this.placement = placement;
  }

  private placeShard(shard: number, nodes: NodeInfo[]): string[] {
    if (nodes.length === 0) return [];
    const target = Math.min(this.replicaCount, nodes.length);
    const scored = nodes.map((node) => ({
      node,
      score: shardHash(shard, node.id) * node.weight,
    }));
    scored.sort((a, b) => b.score - a.score || (a.node.id < b.node.id ? -1 : 1));

    const chosen: NodeInfo[] = [];
    const usedZones = new Set<string>();
    const usedRegions = new Set<string>();

    // Pass 1: distinct zones. Pass 2: distinct regions. Pass 3: anything left.
    for (const pass of [0, 1, 2]) {
      for (const { node } of scored) {
        if (chosen.length >= target) break;
        if (chosen.includes(node)) continue;
        if (pass === 0 && usedZones.has(node.zone)) continue;
        if (pass === 1 && usedRegions.has(node.region)) continue;
        chosen.push(node);
        usedZones.add(node.zone);
        usedRegions.add(node.region);
      }
      if (chosen.length >= target) break;
    }
    return chosen.map((n) => n.id);
  }
}
