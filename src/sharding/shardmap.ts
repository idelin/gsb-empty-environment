import { rendezvousScore, vshardOfKey } from './hash.ts';
import type { Topology } from './topology.ts';

/**
 * Immutable virtual-shard assignment snapshot.
 *
 * Keys map to one of `vshardCount` virtual shards (vnodes); each virtual shard
 * is assigned to `replicationFactor` distinct physical nodes via rendezvous
 * hashing, which keeps shard movement minimal when the topology changes.
 * assignments[v][0] is the primary replica of virtual shard v.
 */
export class ShardMap {
  readonly version: number;
  readonly vshardCount: number;
  readonly replicationFactor: number;
  private readonly assignments: ReadonlyArray<ReadonlyArray<string>>;

  private constructor(
    version: number,
    vshardCount: number,
    replicationFactor: number,
    assignments: ReadonlyArray<ReadonlyArray<string>>,
  ) {
    this.version = version;
    this.vshardCount = vshardCount;
    this.replicationFactor = replicationFactor;
    this.assignments = assignments;
  }

  static build(
    topology: Topology,
    vshardCount: number,
    replicationFactor: number,
    version: number,
  ): ShardMap {
    if (!Number.isInteger(vshardCount) || vshardCount <= 0) {
      throw new RangeError('vshardCount must be a positive integer');
    }
    if (!Number.isInteger(replicationFactor) || replicationFactor <= 0) {
      throw new RangeError('replicationFactor must be a positive integer');
    }
    const nodeIds = topology
      .list()
      .map((n) => n.id)
      .sort();
    // Replication factor is clamped to the number of available nodes.
    const rf = Math.min(replicationFactor, nodeIds.length);
    const assignments: string[][] = [];
    for (let v = 0; v < vshardCount; v++) {
      const scored = nodeIds.map((id) => ({ id, score: rendezvousScore(v, id) }));
      scored.sort((a, b) => b.score - a.score || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
      assignments.push(scored.slice(0, rf).map((s) => s.id));
    }
    return new ShardMap(version, vshardCount, rf, assignments);
  }

  replicasOfVshard(vshard: number): readonly string[] {
    if (!Number.isInteger(vshard) || vshard < 0 || vshard >= this.vshardCount) {
      throw new RangeError(`invalid vshard: ${vshard}`);
    }
    return this.assignments[vshard];
  }

  vshardOf(key: string): number {
    return vshardOfKey(key, this.vshardCount);
  }

  replicasOf(key: string): readonly string[] {
    return this.replicasOfVshard(this.vshardOf(key));
  }

  primaryOf(key: string): string {
    const replicas = this.replicasOf(key);
    if (replicas.length === 0) throw new Error('no nodes available for routing');
    return replicas[0];
  }

  /** Number of virtual shards each node holds a replica of. */
  replicaCounts(): Map<string, number> {
    const counts = new Map<string, number>();
    for (const replicas of this.assignments) {
      for (const id of replicas) counts.set(id, (counts.get(id) ?? 0) + 1);
    }
    return counts;
  }

  /** Number of virtual shards whose replica sets differ from `other`. */
  diffCount(other: ShardMap): number {
    if (this.vshardCount !== other.vshardCount) {
      throw new Error('cannot diff shard maps with different vshard counts');
    }
    let diff = 0;
    for (let v = 0; v < this.vshardCount; v++) {
      const a = this.assignments[v];
      const b = other.assignments[v];
      if (a.length !== b.length || a.some((id, i) => id !== b[i])) diff++;
    }
    return diff;
  }
}
