import { VirtualClock } from './clock.ts';
import { NoFreshReplicaError, ReadTimeoutError, StaleReadError } from './errors.ts';
import { Session } from './session.ts';
import { Cluster, ReplicaNode, type NodeId, type PartitionId } from './store.ts';

/**
 * 副本落后时的显式策略：
 * - wait：挂起等待，直到某副本追平会话水位；
 * - switch：立即在候选节点间切换，全部落后则抛 NoFreshReplicaError；
 * - timeout：等待追平但带时限，超时抛 ReadTimeoutError。
 */
export type LagPolicy =
  | { readonly kind: 'wait' }
  | { readonly kind: 'switch' }
  | { readonly kind: 'timeout'; readonly timeoutMs: number };

export interface ReadResult {
  readonly value: unknown;
  /** 响应版本水位（副本已应用水位），已记录进会话。 */
  readonly version: number;
  readonly node: NodeId;
}

export class SessionRouter {
  private readonly cluster: Cluster;
  private readonly clock: VirtualClock;

  constructor(cluster: Cluster, clock: VirtualClock) {
    this.cluster = cluster;
    this.clock = clock;
  }

  /** 写主库，返回版本并记录进会话（读己之写）。 */
  async write(
    session: Session,
    partition: PartitionId,
    key: string,
    value: unknown,
  ): Promise<number> {
    const { version } = await this.cluster.write(partition, key, value);
    session.record(partition, version);
    return version;
  }

  /**
   * 带会话保证的读：只从已追平会话水位的副本读取，
   * 响应版本记录回会话；绝不返回比水位更旧的版本。
   */
  async read(
    session: Session,
    partition: PartitionId,
    key: string,
    policy: LagPolicy,
  ): Promise<ReadResult> {
    const required = session.watermark(partition);
    const node = await this.selectNode(partition, required, policy);
    const snapshot = await node.read(partition, key);
    if (snapshot.appliedVersion < required) {
      throw new StaleReadError(partition, required, snapshot.appliedVersion);
    }
    session.record(partition, snapshot.appliedVersion);
    return {
      value: snapshot.entry?.value,
      version: snapshot.appliedVersion,
      node: node.id,
    };
  }

  private async selectNode(
    partition: PartitionId,
    required: number,
    policy: LagPolicy,
  ): Promise<ReplicaNode> {
    const candidates = this.cluster.readNodes(partition);
    if (candidates.length === 0) {
      throw new NoFreshReplicaError(partition, required);
    }
    switch (policy.kind) {
      case 'switch': {
        const node = this.freshest(candidates, partition, required);
        if (!node) throw new NoFreshReplicaError(partition, required);
        return node;
      }
      case 'wait':
        return this.awaitFresh(candidates, partition, required);
      case 'timeout': {
        const outcome = await Promise.race([
          this.awaitFresh(candidates, partition, required).then(
            (node) => ({ tag: 'fresh' as const, node }),
          ),
          this.clock.sleep(policy.timeoutMs).then(() => ({ tag: 'expired' as const })),
        ]);
        if (outcome.tag === 'expired') {
          throw new ReadTimeoutError(partition, required, policy.timeoutMs);
        }
        return outcome.node;
      }
    }
  }

  /** 已追平的候选中延迟最低者；无则 undefined。 */
  private freshest(
    candidates: ReplicaNode[],
    partition: PartitionId,
    required: number,
  ): ReplicaNode | undefined {
    let best: ReplicaNode | undefined;
    for (const node of candidates) {
      if (node.appliedVersion(partition) < required) continue;
      if (!best || node.latencyMs < best.latencyMs) best = node;
    }
    return best;
  }

  /** 等待任一候选追平水位（事件驱动，不轮询），随后取已追平者中延迟最低的。 */
  private async awaitFresh(
    candidates: ReplicaNode[],
    partition: PartitionId,
    required: number,
  ): Promise<ReplicaNode> {
    const immediate = this.freshest(candidates, partition, required);
    if (immediate) return immediate;
    const subs = candidates.map((n) => n.whenVersionReached(partition, required));
    try {
      await Promise.race(subs.map((s) => s.promise));
    } finally {
      for (const s of subs) s.cancel();
    }
    const node = this.freshest(candidates, partition, required);
    if (!node) throw new NoFreshReplicaError(partition, required);
    return node;
  }
}
