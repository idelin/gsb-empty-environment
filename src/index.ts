export {
  MerkleTree,
  hashLeaf,
  hashNode,
  foldProof,
  verifyProof,
} from './merkleTree.ts';
export type { MerkleProof, ProofStep } from './merkleTree.ts';
export { ShardTree, encodeAccountLeaf } from './shardTree.ts';
export type { AccountState, ShardInclusionProof } from './shardTree.ts';
export { Aggregator, verifyCrossShardProof, SHARD_COUNT } from './aggregator.ts';
export type { CrossShardProof } from './aggregator.ts';
