import { VirtualClock, type TimerHandle } from './clock.ts';

export type PartitionId = string;
export type NodeId = string;

export interface VersionedValue {
  readonly value: unknown;
  readonly version: number;
}

export interface ReadSnapshot {
  /** 键对应的条目；键不存在则为 undefined。 */
  readonly entry: VersionedValue | undefined;
  /** 响应产生时该副本在此分区上的已应用水位。 */
  readonly appliedVersion: number;
}

interface VersionWaiter {
  version: number;
  resolve: () => void;
}

/**
 * 副本节点：持有若干分区的本地副本，带固定处理延迟。
 * 复制事件按分区版本有序应用（每个副本的复制滞后固定，保证 FIFO）。
 */
export class ReplicaNode {
  readonly id: NodeId;
  private readonly clock: VirtualClock;
  readonly latencyMs: number;
  private readonly data = new Map<PartitionId, Map<string, VersionedValue>>();
  private readonly applied = new Map<PartitionId, number>();
  private readonly waiters = new Map<PartitionId, VersionWaiter[]>();

  constructor(id: NodeId, clock: VirtualClock, latencyMs: number) {
    this.id = id;
    this.clock = clock;
    this.latencyMs = latencyMs;
  }

  /** 该副本在某分区上已应用的版本水位。 */
  appliedVersion(partition: PartitionId): number {
    return this.applied.get(partition) ?? 0;
  }

  /** 复制应用（由集群在滞后到期时调用），幂等且只前进。 */
  apply(partition: PartitionId, key: string, entry: VersionedValue): void {
    if (entry.version <= this.appliedVersion(partition)) return;
    let store = this.data.get(partition);
    if (!store) {
      store = new Map();
      this.data.set(partition, store);
    }
    store.set(key, entry);
    this.applied.set(partition, entry.version);
    const queue = this.waiters.get(partition);
    if (queue) {
      const ready = queue.filter((w) => w.version <= entry.version);
      if (ready.length === queue.length) {
        this.waiters.delete(partition);
      } else {
        this.waiters.set(
          partition,
          queue.filter((w) => w.version > entry.version),
        );
      }
      for (const w of ready) w.resolve();
    }
  }

  /** 注册“追平到 version 时通知”的订阅；已追平则立即解决。 */
  whenVersionReached(
    partition: PartitionId,
    version: number,
  ): { promise: Promise<void>; cancel: () => void } {
    if (this.appliedVersion(partition) >= version) {
      return { promise: Promise.resolve(), cancel: () => {} };
    }
    let resolveFn!: () => void;
    const promise = new Promise<void>((resolve) => {
      resolveFn = resolve;
    });
    const waiter: VersionWaiter = { version, resolve: resolveFn };
    const queue = this.waiters.get(partition);
    if (queue) queue.push(waiter);
    else this.waiters.set(partition, [waiter]);
    const cancel = () => {
      const q = this.waiters.get(partition);
      if (!q) return;
      const idx = q.indexOf(waiter);
      if (idx >= 0) q.splice(idx, 1);
      if (q.length === 0) this.waiters.delete(partition);
    };
    return { promise, cancel };
  }

  /** 模拟节点处理延迟后返回读取快照。 */
  async read(partition: PartitionId, key: string): Promise<ReadSnapshot> {
    await this.clock.sleep(this.latencyMs);
    return {
      entry: this.data.get(partition)?.get(key),
      appliedVersion: this.appliedVersion(partition),
    };
  }
}

export interface ReplicaSpec {
  readonly id: NodeId;
  /** 节点处理一次读取的延迟。 */
  readonly latencyMs: number;
  /** 主库写入传播到该副本的复制滞后。 */
  readonly replicationLagMs: number;
}

interface PartitionTopology {
  readonly primaryLatencyMs: number;
  readonly replicas: ReplicaNode[];
  readonly replicationLags: Map<NodeId, number>;
  version: number;
  readonly data: Map<string, VersionedValue>;
  readonly replicationTimers: TimerHandle[];
}

/**
 * 集群：分区 + 主库 + 若干滞后各不相同的读副本。
 * 写入先在主库生效（耗时 primaryLatencyMs），再按各副本滞后调度复制事件。
 */
export class Cluster {
  private readonly partitions = new Map<PartitionId, PartitionTopology>();
  private readonly clock: VirtualClock;

  constructor(clock: VirtualClock) {
    this.clock = clock;
  }

  addPartition(
    id: PartitionId,
    opts: { primaryLatencyMs?: number; replicas?: ReplicaSpec[] } = {},
  ): void {
    const replicas = (opts.replicas ?? []).map(
      (spec) => new ReplicaNode(spec.id, this.clock, spec.latencyMs),
    );
    const replicationLags = new Map<NodeId, number>();
    for (const spec of opts.replicas ?? []) replicationLags.set(spec.id, spec.replicationLagMs);
    this.partitions.set(id, {
      primaryLatencyMs: opts.primaryLatencyMs ?? 0,
      replicas,
      replicationLags,
      version: 0,
      data: new Map(),
      replicationTimers: [],
    });
  }

  /** 某分区的读候选节点（仅读副本；主库只服务写入）。 */
  readNodes(partition: PartitionId): ReplicaNode[] {
    return this.topology(partition).replicas;
  }

  /** 在主库上写入并调度向各副本的复制，返回新版本号。 */
  async write(
    partition: PartitionId,
    key: string,
    value: unknown,
  ): Promise<{ version: number }> {
    const topo = this.topology(partition);
    await this.clock.sleep(topo.primaryLatencyMs);
    const version = ++topo.version;
    const entry: VersionedValue = { value, version };
    topo.data.set(key, entry);
    for (const replica of topo.replicas) {
      const lag = topo.replicationLags.get(replica.id) ?? 0;
      const handle = this.clock.schedule(lag, () => replica.apply(partition, key, entry));
      topo.replicationTimers.push(handle);
    }
    return { version };
  }

  private topology(partition: PartitionId): PartitionTopology {
    const topo = this.partitions.get(partition);
    if (!topo) throw new Error(`unknown partition "${partition}"`);
    return topo;
  }
}
