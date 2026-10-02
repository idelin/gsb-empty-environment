import { MerkleTree } from './merkle.ts';
import type { ProofStep } from './merkle.ts';

/**
 * Top-level Meta-Tree maintained by the main aggregator.
 * Leaf i is the current local root of shard i.
 */
export class MetaTree {
  readonly shardCount: number;
  private readonly tree: MerkleTree;

  constructor(shardCount = 4) {
    if (shardCount < 1) throw new RangeError('shardCount must be >= 1');
    this.shardCount = shardCount;
    this.tree = new MerkleTree(shardCount);
  }

  /** Records the latest local root of one shard. */
  updateShardRoot(shardId: number, shardRootHex: string): void {
    if (shardId < 0 || shardId >= this.shardCount) {
      throw new RangeError(`shard id ${shardId} out of range (0..${this.shardCount - 1})`);
    }
    this.tree.setLeaf(shardId, Buffer.from(shardRootHex, 'hex'));
  }

  /** Current meta root, hex encoded. */
  getMetaRoot(): string {
    return this.tree.getRoot().toString('hex');
  }

  /** Inclusion proof for shard `shardId`'s slot in the Meta-Tree. */
  getShardProof(shardId: number): ProofStep[] {
    return this.tree.getProof(shardId);
  }
}
