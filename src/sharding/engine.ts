import { EventEmitter } from 'node:events';
import { AsyncMutex } from './mutex.ts';
import { Router } from './router.ts';
import { ShardMap } from './shardmap.ts';
import type { NodeInfo, Topology, TopologyChange } from './topology.ts';

export interface ShardMove {
  vshard: number;
  from: readonly string[];
  to: readonly string[];
}

export interface MigrationPlan {
  fromVersion: number;
  toVersion: number;
  moves: ShardMove[];
}

export interface ShardingEngineOptions {
  vshardCount?: number;
  replicationFactor?: number;
}

/**
 * Hierarchical sharding engine with locality-aware routing.
 *
 * All topology mutations are serialized through an async mutex and produce a
 * new immutable ShardMap snapshot (copy-on-write), so concurrent readers
 * always see a consistent assignment. Routers capture the snapshot per call.
 */
export class ShardingEngine extends EventEmitter {
  readonly topology: Topology;
  readonly router: Router;
  private readonly vshardCount: number;
  private readonly replicationFactor: number;
  private readonly mutex = new AsyncMutex();
  private map: ShardMap;
  private disposed = false;
  // True while the engine itself is mutating the topology inside the mutex;
  // those changes are rebuilt inline and must not be scheduled twice.
  private mutating = false;
  private readonly onTopologyChange = (change: TopologyChange): void => {
    if (this.mutating || this.disposed) return;
    // Fire-and-forget serialized rebalance; errors surface as 'error' events.
    void this.rebalance().catch((err: unknown) => {
      if (!this.disposed && this.listenerCount('error') > 0) this.emit('error', err, change);
    });
  };

  constructor(topology: Topology, options: ShardingEngineOptions = {}) {
    super();
    this.topology = topology;
    this.vshardCount = options.vshardCount ?? 1024;
    this.replicationFactor = options.replicationFactor ?? 3;
    this.map = ShardMap.build(topology, this.vshardCount, this.replicationFactor, 0);
    this.router = new Router(topology, () => this.map);
    this.topology.on('change', this.onTopologyChange);
  }

  getShardMap(): ShardMap {
    return this.map;
  }

  get version(): number {
    return this.map.version;
  }

  /** Number of topology updates currently running or queued. */
  get pendingUpdates(): number {
    return this.mutex.pending;
  }

  addNode(info: NodeInfo): Promise<MigrationPlan> {
    return this.mutex.runExclusive(() => {
      this.assertLive();
      this.mutating = true;
      try {
        this.topology.addNode(info);
      } finally {
        this.mutating = false;
      }
      return this.rebuildLocked();
    });
  }

  removeNode(id: string): Promise<MigrationPlan> {
    return this.mutex.runExclusive(() => {
      this.assertLive();
      this.mutating = true;
      try {
        this.topology.removeNode(id);
      } finally {
        this.mutating = false;
      }
      return this.rebuildLocked();
    });
  }

  /** Rebuild the shard map against the current topology (serialized). */
  rebalance(): Promise<MigrationPlan> {
    return this.mutex.runExclusive(() => {
      this.assertLive();
      return this.rebuildLocked();
    });
  }

  private rebuildLocked(): MigrationPlan {
    const previous = this.map;
    const next = ShardMap.build(this.topology, this.vshardCount, this.replicationFactor, previous.version + 1);
    const moves: ShardMove[] = [];
    for (let v = 0; v < next.vshardCount; v++) {
      const from = previous.replicasOfVshard(v);
      const to = next.replicasOfVshard(v);
      if (from.length !== to.length || from.some((id, i) => id !== to[i])) {
        moves.push({ vshard: v, from, to });
      }
    }
    this.map = next;
    const plan: MigrationPlan = {
      fromVersion: previous.version,
      toVersion: next.version,
      moves,
    };
    this.emit('rebalanced', plan);
    return plan;
  }

  private assertLive(): void {
    if (this.disposed) throw new Error('sharding engine has been disposed');
  }

  dispose(): void {
    this.disposed = true;
    this.topology.off('change', this.onTopologyChange);
    this.removeAllListeners();
    this.router.resetStats();
  }
}
