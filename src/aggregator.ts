import { MerkleTree, leafIndexFromProof } from './merkle.ts';
import type { ProofStep } from './merkle.ts';
import { ShardTree, encodeAccount } from './shard.ts';
import type { AccountState } from './shard.ts';
import { MetaTree } from './meta-tree.ts';

/**
 * End-to-end cross-shard proof: shard-internal account inclusion proof
 * plus the shard's position proof inside the Meta-Tree.
 * Fully self-contained and JSON-serializable.
 */
export interface CrossShardProof {
  shardId: number;
  account: AccountState;
  shardLeafIndex: number;
  shardProof: ProofStep[];
  shardRoot: string;
  metaProof: ProofStep[];
  metaRoot: string;
}

/**
 * Main aggregator: owns the shard trees and the Meta-Tree, and merges
 * local proofs with meta proofs into a single cross-shard proof.
 */
export class Aggregator {
  readonly shards: ShardTree[];
  private readonly metaTree: MetaTree;

  constructor(shardCount = 4) {
    this.shards = Array.from({ length: shardCount }, (_, id) => new ShardTree(id));
    this.metaTree = new MetaTree(shardCount);
  }

  getShard(shardId: number): ShardTree {
    const shard = this.shards[shardId];
    if (!shard) throw new RangeError(`unknown shard id: ${shardId}`);
    return shard;
  }

  /** Publishes one shard's current local root into the Meta-Tree. */
  syncShard(shardId: number): void {
    this.metaTree.updateShardRoot(shardId, this.getShard(shardId).getLocalRoot());
  }

  syncAllShards(): void {
    for (const shard of this.shards) this.syncShard(shard.shardId);
  }

  getMetaRoot(): string {
    return this.metaTree.getMetaRoot();
  }

  /**
   * Merges the shard-internal account proof with the shard's Meta-Tree
   * position proof into one self-contained cross-shard proof.
   */
  buildCrossShardProof(shardId: number, accountId: string): CrossShardProof {
    this.syncShard(shardId);
    const shardProof = this.getShard(shardId).getAccountProof(accountId);
    return {
      shardId,
      account: shardProof.account,
      shardLeafIndex: shardProof.leafIndex,
      shardProof: shardProof.proof,
      shardRoot: shardProof.shardRoot,
      metaProof: this.metaTree.getShardProof(shardId),
      metaRoot: this.metaTree.getMetaRoot(),
    };
  }
}

/**
 * Independent external verifier. Needs nothing but the proof itself plus
 * (optionally) a trusted meta root to pin against. Returns true only if:
 *  1. the account state hashes into `shardRoot` via `shardProof`,
 *  2. the shard proof path is consistent with `shardLeafIndex`,
 *  3. `shardRoot` hashes into `metaRoot` via `metaProof`,
 *  4. the meta proof path is consistent with `shardId`,
 *  5. `metaRoot` matches `trustedMetaRoot` when one is supplied.
 */
export function verifyCrossShardProof(proof: CrossShardProof, trustedMetaRoot?: string): boolean {
  let shardLeaf: Buffer;
  try {
    shardLeaf = encodeAccount(proof.account);
  } catch {
    return false;
  }
  const shardRoot = Buffer.from(proof.shardRoot, 'hex');
  if (!MerkleTree.verifyProof(shardLeaf, proof.shardProof, shardRoot)) return false;
  if (leafIndexFromProof(proof.shardProof) !== proof.shardLeafIndex) return false;

  const metaRoot = Buffer.from(proof.metaRoot, 'hex');
  if (!MerkleTree.verifyProof(shardRoot, proof.metaProof, metaRoot)) return false;
  if (leafIndexFromProof(proof.metaProof) !== proof.shardId) return false;

  if (trustedMetaRoot !== undefined && trustedMetaRoot !== proof.metaRoot) return false;
  return true;
}
