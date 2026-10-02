import type { KeyRange } from './keyrange.ts';
import { contains, midpointOf, overlaps, rangeKey } from './keyrange.ts';
import { LeaseManager, type Lease } from './lease.ts';
import type { QueryResult, StorageNode } from './node.ts';

export interface Partition {
  readonly range: KeyRange;
  lease: Lease;
  /** Approximate data size, used to trigger automatic splits. */
  dataSize: number;
}

export interface SplitEvent {
  parent: { range: KeyRange; leaseId: number; epoch: number };
  children: Array<{ range: KeyRange; leaseId: number; nodeId: string; epoch: number }>;
}

/**
 * Fine-grained range-partition lease routing gateway.
 *
 * Maintains an ordered route table of non-overlapping [start, end) ranges,
 * each guarded by an exclusive lease held by exactly one storage node.
 * On split, the parent lease is revoked and each child range is granted an
 * independent lease that inherits epoch = parent.epoch + 1.
 */
export class LeaseRouterGateway {
  readonly leaseManager = new LeaseManager();
  private nodes = new Map<string, StorageNode>();
  private partitions: Partition[] = [];
  readonly splitLog: SplitEvent[] = [];

  private readonly splitThreshold: number;

  constructor(splitThreshold: number = 1000) {
    this.splitThreshold = splitThreshold;
  }

  addNode(node: StorageNode): void {
    this.nodes.set(node.nodeId, node);
  }

  /** Create the very first partition covering `range`, leased to `nodeId`. */
  bootstrap(range: KeyRange, nodeId: string): Lease {
    if (this.partitions.length !== 0) throw new Error('gateway already bootstrapped');
    this.requireNode(nodeId);
    const lease = this.leaseManager.grant(range, nodeId, 1);
    this.partitions.push({ range, lease, dataSize: 0 });
    return lease;
  }

  /**
   * Find the partition owning `key`. The last partition is open-tailed:
   * any key at or beyond its start belongs to it, modelling the
   * unbounded tail of the keyspace (so 'Zoo' lives in [M-Z)).
   */
  locate(key: string): Partition {
    for (let i = 0; i < this.partitions.length; i += 1) {
      const partition = this.partitions[i];
      const isTail = i === this.partitions.length - 1;
      if (contains(partition.range, key) || (isTail && key >= partition.range.start)) {
        return partition;
      }
    }
    throw new Error(`no partition covers key '${key}'`);
  }

  /** Resolve a key to its current lease holder node. */
  route(key: string): { node: StorageNode; lease: Lease } {
    const partition = this.locate(key);
    this.leaseManager.assertUsable(partition.lease);
    return { node: this.requireNode(partition.lease.nodeId), lease: partition.lease };
  }

  /** Route a query to the lease holder of `key`. */
  async query(key: string): Promise<QueryResult> {
    const { node } = this.route(key);
    return node.handleQuery(key);
  }

  /**
   * Record a write of `bytes` against `key`; if the owning partition grows
   * beyond the split threshold it is split automatically at its midpoint.
   */
  recordWrite(key: string, bytes: number = 1): SplitEvent | null {
    const partition = this.locate(key);
    partition.dataSize += bytes;
    if (partition.dataSize > this.splitThreshold) {
      return this.splitAt(midpointOf(partition.range));
    }
    return null;
  }

  /**
   * Split the partition containing `splitKey` into
   * [start, splitKey) and [splitKey, end). The parent lease is revoked and
   * each child receives an independent lease (epoch inherited + 1). Child
   * leases go to `leftNodeId` / `rightNodeId` (default: parent holder).
   */
  splitAt(splitKey: string, leftNodeId?: string, rightNodeId?: string): SplitEvent {
    const index = this.partitions.findIndex((p) => contains(p.range, splitKey));
    if (index < 0) throw new Error(`no partition covers split key '${splitKey}'`);
    const parent = this.partitions[index];
    const { range, lease: parentLease } = parent;
    if (splitKey <= range.start || splitKey >= range.end) {
      throw new Error(`split key '${splitKey}' must be strictly inside ${rangeKey(range)}`);
    }

    const leftId = leftNodeId ?? parentLease.nodeId;
    const rightId = rightNodeId ?? parentLease.nodeId;
    this.requireNode(leftId);
    this.requireNode(rightId);

    this.leaseManager.revoke(parentLease);

    const childEpoch = parentLease.epoch + 1;
    const leftRange: KeyRange = { start: range.start, end: splitKey };
    const rightRange: KeyRange = { start: splitKey, end: range.end };
    const leftLease = this.leaseManager.grant(leftRange, leftId, childEpoch, parentLease.id);
    const rightLease = this.leaseManager.grant(rightRange, rightId, childEpoch, parentLease.id);

    const half = Math.floor(parent.dataSize / 2);
    const children: Partition[] = [
      { range: leftRange, lease: leftLease, dataSize: half },
      { range: rightRange, lease: rightLease, dataSize: parent.dataSize - half },
    ];
    this.partitions.splice(index, 1, ...children);
    this.assertTableSane();

    const event: SplitEvent = {
      parent: { range, leaseId: parentLease.id, epoch: parentLease.epoch },
      children: [
        { range: leftRange, leaseId: leftLease.id, nodeId: leftId, epoch: childEpoch },
        { range: rightRange, leaseId: rightLease.id, nodeId: rightId, epoch: childEpoch },
      ],
    };
    this.splitLog.push(event);
    return event;
  }

  table(): Array<{ range: string; nodeId: string; leaseId: number; epoch: number; dataSize: number }> {
    return this.partitions.map((p) => ({
      range: rangeKey(p.range),
      nodeId: p.lease.nodeId,
      leaseId: p.lease.id,
      epoch: p.lease.epoch,
      dataSize: p.dataSize,
    }));
  }

  private requireNode(nodeId: string): StorageNode {
    const node = this.nodes.get(nodeId);
    if (!node) throw new Error(`unknown node '${nodeId}'`);
    return node;
  }

  private assertTableSane(): void {
    for (let i = 0; i < this.partitions.length; i += 1) {
      const current = this.partitions[i];
      if (current.range.start >= current.range.end) {
        throw new Error(`degenerate range ${rangeKey(current.range)}`);
      }
      for (let j = i + 1; j < this.partitions.length; j += 1) {
        if (overlaps(current.range, this.partitions[j].range)) {
          throw new Error(`overlapping ranges ${rangeKey(current.range)} / ${rangeKey(this.partitions[j].range)}`);
        }
      }
    }
  }
}
