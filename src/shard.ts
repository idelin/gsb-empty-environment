/**
 * Hierarchical sharding and locality-aware routing engine.
 *
 * Topology is three-level: region -> zone -> node. Keys map to a fixed
 * number of virtual shards via FNV-1a hashing; each shard is assigned to
 * replicaCount nodes by weighted rendezvous hashing with hierarchy
 * diversity (replicas prefer distinct zones, then distinct regions).
 * Routing ranks healthy replicas by locality distance to the client:
 * same zone < same region < remote, minimizing cross-node traffic.
 *
 * Concurrency: all topology mutations are serialized through an internal
 * async mutex and produce immutable, versioned snapshots. Readers always
 * see a consistent snapshot.
 */

export interface NodeInfo {
  id: string;
  region: string;
  zone: string;
  /** relative capacity; more virtual shards land on heavier nodes. */
  weight: number;
  healthy: boolean;
}

export interface Locality {
  region: string;
  zone: string;
}

export interface RouteDecision {
  key: string;
  shardId: number;
  /** ordered candidates: best locality first. */
  candidates: NodeInfo[];
  /** 0 = same zone, 1 = same region, 2 = remote. */
  localityDistance: number;
  /** topology snapshot version used for this decision. */
  version: number;
}

export interface RouterStats {
  totalRoutes: number;
  sameZone: number;
  sameRegion: number;
  remote: number;
}

export class TopologyError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'TopologyError';
  }
}

/** FNV-1a 32-bit hash: fast, deterministic, good avalanche for short keys. */
export function hashKey(key: string): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < key.length; i++) {
    h ^= key.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return h >>> 0;
}

function mixHash(a: number, b: number): number {
  let h = a >>> 0;
  h ^= b >>> 0;
  h = Math.imul(h, 0x01000193);
  h ^= h >>> 13;
  h = Math.imul(h, 0x5bd1e995);
  h ^= h >>> 15;
  return h >>> 0;
}

interface ShardAssignment {
  primary: string;
  replicas: string[]; // includes primary, best-first
}

interface Snapshot {
  version: number;
  nodes: ReadonlyMap<string, NodeInfo>;
  assignments: readonly ShardAssignment[];
}

class AsyncMutex {
  private tail: Promise<void> = Promise.resolve();

  async run<T>(fn: () => T | Promise<T>): Promise<T> {
    const prev = this.tail;
    let release!: () => void;
    this.tail = new Promise<void>((resolve) => (release = resolve));
    await prev;
    try {
      return await fn();
    } finally {
      release();
    }
  }
}

export interface RouterOptions {
  /** total virtual shards; must be a positive integer. */
  shardCount?: number;
  /** replicas per shard (including primary). */
  replicaCount?: number;
}

export class LocalityAwareRouter {
  private readonly shardCount: number;
  private readonly replicaCount: number;
  private readonly mutex = new AsyncMutex();
  private snapshot: Snapshot = { version: 0, nodes: new Map(), assignments: [] };
  private readonly stats: RouterStats = { totalRoutes: 0, sameZone: 0, sameRegion: 0, remote: 0 };
  private readonly inflight = new Map<string, number>();
  private disposed = false;

  constructor(options: RouterOptions = {}) {
    this.shardCount = options.shardCount ?? 1024;
    this.replicaCount = options.replicaCount ?? 3;
    if (!Number.isInteger(this.shardCount) || this.shardCount <= 0) {
      throw new TopologyError('shardCount must be a positive integer');
    }
    if (!Number.isInteger(this.replicaCount) || this.replicaCount <= 0) {
      throw new TopologyError('replicaCount must be a positive integer');
    }
  }

  get version(): number {
    return this.snapshot.version;
  }

  getStats(): RouterStats {
    return { ...this.stats };
  }

  nodeCount(): number {
    return this.snapshot.nodes.size;
  }

  /** Register or update a node; triggers reassignment of virtual shards. */
  async upsertNode(node: NodeInfo): Promise<void> {
    this.assertAlive();
    if (!node.id) throw new TopologyError('node id must be non-empty');
    if (!Number.isFinite(node.weight) || node.weight <= 0) {
      throw new TopologyError(`node ${node.id}: weight must be a positive finite number`);
    }
    await this.mutex.run(() => {
      const nodes = new Map(this.snapshot.nodes);
      nodes.set(node.id, { ...node });
      this.publish(nodes);
    });
  }

  async removeNode(nodeId: string): Promise<boolean> {
    this.assertAlive();
    return this.mutex.run(() => {
      if (!this.snapshot.nodes.has(nodeId)) return false;
      const nodes = new Map(this.snapshot.nodes);
      nodes.delete(nodeId);
      this.inflight.delete(nodeId);
      this.publish(nodes);
      return true;
    });
  }

  async setHealthy(nodeId: string, healthy: boolean): Promise<void> {
    this.assertAlive();
    await this.mutex.run(() => {
      const node = this.snapshot.nodes.get(nodeId);
      if (!node) throw new TopologyError(`unknown node: ${nodeId}`);
      const nodes = new Map(this.snapshot.nodes);
      nodes.set(nodeId, { ...node, healthy });
      this.publish(nodes);
    });
  }

  /**
   * Route a key to the best-located healthy replica.
   * Synchronous over an immutable snapshot: always consistent.
   */
  route(key: string, locality: Locality): RouteDecision {
    this.assertAlive();
    const snap = this.snapshot;
    if (snap.nodes.size === 0) throw new TopologyError('cannot route: topology is empty');

    const shardId = hashKey(key) % this.shardCount;
    const assignment = snap.assignments[shardId];
    const healthy = assignment.replicas
      .map((id) => snap.nodes.get(id))
      .filter((n): n is NodeInfo => n !== undefined && n.healthy);
    if (healthy.length === 0) {
      throw new TopologyError(`shard ${shardId}: no healthy replica available`);
    }

    const distance = (n: NodeInfo): number =>
      n.zone === locality.zone && n.region === locality.region
        ? 0
        : n.region === locality.region
          ? 1
          : 2;

    const ranked = [...healthy].sort((a, b) => {
      const d = distance(a) - distance(b);
      if (d !== 0) return d;
      const load = (this.inflight.get(a.id) ?? 0) - (this.inflight.get(b.id) ?? 0);
      if (load !== 0) return load;
      return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
    });

    const best = distance(ranked[0]);
    this.stats.totalRoutes++;
    if (best === 0) this.stats.sameZone++;
    else if (best === 1) this.stats.sameRegion++;
    else this.stats.remote++;

    return {
      key,
      shardId,
      candidates: ranked,
      localityDistance: best,
      version: snap.version,
    };
  }

  /** Track in-flight load for least-loaded tie-breaking. Returns a release fn. */
  acquire(nodeId: string): () => void {
    this.assertAlive();
    const next = (this.inflight.get(nodeId) ?? 0) + 1;
    this.inflight.set(nodeId, next);
    let released = false;
    return () => {
      if (released) return;
      released = true;
      const cur = this.inflight.get(nodeId) ?? 0;
      if (cur <= 1) this.inflight.delete(nodeId);
      else this.inflight.set(nodeId, cur - 1);
    };
  }

  /** Inspect which nodes own a shard (diagnostics / tests). */
  shardOwners(shardId: number): string[] {
    const a = this.snapshot.assignments[shardId];
    return a ? [...a.replicas] : [];
  }

  /** Count shards whose primary is the given node. */
  primaryCount(nodeId: string): number {
    let n = 0;
    for (const a of this.snapshot.assignments) if (a.primary === nodeId) n++;
    return n;
  }

  /** Release all internal state; router becomes unusable afterwards. */
  dispose(): void {
    this.snapshot = { version: this.snapshot.version, nodes: new Map(), assignments: [] };
    this.inflight.clear();
    this.disposed = true;
  }

  private assertAlive(): void {
    if (this.disposed) throw new TopologyError('router has been disposed');
  }

  private publish(nodes: Map<string, NodeInfo>): void {
    this.snapshot = {
      version: this.snapshot.version + 1,
      nodes,
      assignments: this.assignShards(nodes),
    };
  }

  /**
   * Weighted rendezvous (highest-random-weight) assignment with hierarchy
   * diversity: replicas prefer distinct regions first, then distinct zones.
   * Region-first placement guarantees every client region holds a local
   * copy (when replicaCount >= regionCount), driving cross-region traffic
   * to zero; zone spread then limits blast radius of a zone failure.
   */
  private assignShards(nodes: Map<string, NodeInfo>): ShardAssignment[] {
    const list = [...nodes.values()];
    const out: ShardAssignment[] = new Array(this.shardCount);
    for (let shard = 0; shard < this.shardCount; shard++) {
      const scored = list.map((n) => ({
        node: n,
        score: mixHash(shard, hashKey(n.id)) * n.weight,
      }));
      scored.sort((a, b) => b.score - a.score || (a.node.id < b.node.id ? -1 : 1));

      const picked: NodeInfo[] = [];
      const usedZones = new Set<string>();
      const usedRegions = new Set<string>();
      const target = Math.min(this.replicaCount, list.length);
      for (const pass of [0, 1, 2]) {
        for (const { node } of scored) {
          if (picked.length >= target) break;
          if (picked.includes(node)) continue;
          const zoneKey = `${node.region}/${node.zone}`;
          if (pass === 0 && usedRegions.has(node.region)) continue;
          if (pass === 1 && usedZones.has(zoneKey)) continue;
          picked.push(node);
          usedZones.add(zoneKey);
          usedRegions.add(node.region);
        }
      }
      out[shard] = {
        primary: picked[0]?.id ?? '',
        replicas: picked.map((n) => n.id),
      };
    }
    return out;
  }
}
