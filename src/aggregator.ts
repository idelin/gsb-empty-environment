import { MerkleTree, foldProof, hashLeaf } from './merkleTree.ts';
import type { MerkleProof } from './merkleTree.ts';
import { ShardTree, encodeAccountLeaf } from './shardTree.ts';
import type { ShardInclusionProof, AccountState } from './shardTree.ts';

export const SHARD_COUNT = 4;

/**
 * End-to-end cross-shard aggregate proof. Fully self-contained and
 * JSON-serializable so any external verifier can check it offline.
 */
export interface CrossShardProof {
  version: 1;
  shardId: number;
  accountId: string;
  accountState: { balance: string; nonce: number }; // balance as decimal string for JSON safety
  shardLeafHash: string; // hex
  shardProof: MerkleProof; // account -> shard root
  shardRoot: string; // hex
  metaProof: MerkleProof; // shard root -> meta root
  metaRoot: string; // hex
}

/**
 * Master aggregator: collects the 4 shard local roots, builds the top-level
 * Meta-Tree, and issues combined cross-shard aggregate proofs.
 */
export class Aggregator {
  readonly shards: ShardTree[];

  constructor(shardCount: number = SHARD_COUNT) {
    this.shards = Array.from({ length: shardCount }, (_, i) => new ShardTree(i));
  }

  shard(shardId: number): ShardTree {
    const shard = this.shards[shardId];
    if (!shard) throw new RangeError(`shard ${shardId} out of range`);
    return shard;
  }

  /** Meta-Tree whose leaves are the current local roots of every shard. */
  private buildMetaTree(): MerkleTree {
    const meta = new MerkleTree();
    for (const shard of this.shards) {
      meta.append(shard.root());
    }
    return meta;
  }

  metaRoot(): Buffer {
    return this.buildMetaTree().root();
  }

  /** Position proof of a shard's local root inside the Meta-Tree. */
  getShardPositionProof(shardId: number): { proof: MerkleProof; metaRoot: Buffer } {
    const meta = this.buildMetaTree();
    return { proof: meta.getProof(shardId), metaRoot: meta.root() };
  }

  /**
   * Build the combined proof: shard-internal inclusion proof for the account
   * plus the shard's position proof in the Meta-Tree.
   */
  buildCrossShardProof(shardId: number, accountId: string): CrossShardProof {
    const inclusion: ShardInclusionProof = this.shard(shardId).getInclusionProof(accountId);
    const { proof: metaProof, metaRoot } = this.getShardPositionProof(shardId);
    return {
      version: 1,
      shardId,
      accountId,
      accountState: {
        balance: inclusion.state.balance.toString(),
        nonce: inclusion.state.nonce,
      },
      shardLeafHash: inclusion.leafHash,
      shardProof: inclusion.proof,
      shardRoot: inclusion.shardRoot,
      metaProof,
      metaRoot: metaRoot.toString('hex'),
    };
  }
}

/**
 * Standalone external verification of a combined cross-shard proof.
 * Requires nothing but the proof itself (and optionally a trusted meta root
 * to pin against). Returns true only if every link in the chain verifies.
 */
export function verifyCrossShardProof(
  proof: CrossShardProof,
  trustedMetaRoot?: string,
): boolean {
  try {
    // 1. Recompute the account leaf from the claimed state.
    const state: AccountState = {
      balance: BigInt(proof.accountState.balance),
      nonce: proof.accountState.nonce,
    };
    const leaf = encodeAccountLeaf(proof.shardId, proof.accountId, state);
    const leafHash = hashLeaf(leaf);
    if (leafHash.toString('hex') !== proof.shardLeafHash) return false;
    if (leafHash.toString('hex') !== proof.shardProof.leafHash) return false;

    // 2. Fold the shard-internal proof up to the shard local root.
    const shardRoot = foldProof(leafHash, proof.shardProof.path);
    if (shardRoot.toString('hex') !== proof.shardRoot) return false;

    // 3. Fold the Meta-Tree position proof of the shard root up to the meta root.
    const metaLeafHash = hashLeaf(shardRoot);
    if (metaLeafHash.toString('hex') !== proof.metaProof.leafHash) return false;
    const metaRoot = foldProof(metaLeafHash, proof.metaProof.path);
    if (metaRoot.toString('hex') !== proof.metaRoot) return false;

    // 4. Optional pinning against an externally known trusted meta root.
    if (trustedMetaRoot !== undefined && proof.metaRoot !== trustedMetaRoot) return false;

    return true;
  } catch {
    return false;
  }
}
