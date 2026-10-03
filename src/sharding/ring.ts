import { fnv1a32 } from './hash.ts';
import { zoneKey, type NodeInfo } from './topology.ts';

export interface RebuildResult {
  moved: number;
  version: number;
}

export class ShardRing {
  readonly shardCount: number;
  readonly replicaCount: number;
  private holders: string[][] = [];
  private version = 0;

  constructor(shardCount = 256, replicaCount = 2) {
    if (!Number.isInteger(shardCount) || shardCount <= 0) {
      throw new RangeError('shardCount must be a positive integer');
    }
    if (!Number.isInteger(replicaCount) || replicaCount <= 0) {
      throw new RangeError('replicaCount must be a positive integer');
    }
    this.shardCount = shardCount;
    this.replicaCount = replicaCount;
  }

  shardOf(key: string): number {
    return fnv1a32(key) % this.shardCount;
  }

  getVersion(): number {
    return this.version;
  }

  holdersOf(shardId: number): readonly string[] {
    return this.holders[shardId] ?? [];
  }

  rebuild(nodes: NodeInfo[]): RebuildResult {
    let moved = 0;
    const next: string[][] = new Array(this.shardCount);
    for (let shard = 0; shard < this.shardCount; shard++) {
      const ranked = nodes
        .map((node) => ({
          node,
          score: fnv1a32(`shard:${shard}:node:${node.id}`),
        }))
        .sort((a, b) => b.score - a.score || (a.node.id < b.node.id ? -1 : 1));
      const picked: string[] = [];
      const usedRegions = new Set<string>();
      const usedZones = new Set<string>();
      for (const { node } of ranked) {
        if (picked.length >= this.replicaCount) break;
        if (usedRegions.has(node.region)) continue;
        picked.push(node.id);
        usedRegions.add(node.region);
        usedZones.add(zoneKey(node));
      }
      for (const { node } of ranked) {
        if (picked.length >= this.replicaCount) break;
        if (usedZones.has(zoneKey(node))) continue;
        picked.push(node.id);
        usedZones.add(zoneKey(node));
      }
      for (const { node } of ranked) {
        if (picked.length >= this.replicaCount) break;
        if (!picked.includes(node.id)) picked.push(node.id);
      }
      next[shard] = picked;
      const previous = this.holders[shard] ?? [];
      if (previous[0] !== picked[0]) moved++;
    }
    this.holders = next;
    this.version++;
    return { moved, version: this.version };
  }
}
