import { createHash } from 'node:crypto';
import type { NodeId, NodeState } from './types.ts';

/** Branching factor of internal nodes. */
export const BRANCHING_FACTOR = 4;
/** Fixed number of leaf buckets => both peers always build identically-shaped trees. */
export const BUCKET_COUNT = 16;
/** Tree depth: root -> BUCKET_COUNT/BRANCHING internal -> BUCKET_COUNT leaves. */
export const TREE_DEPTH = 2;

export const EMPTY_HASH = createHash('sha256').update('anti-entropy/empty').digest('hex');

function sha256(input: string): string {
  return createHash('sha256').update(input).digest('hex');
}

export function hashState(state: NodeState): string {
  return sha256(`${state.nodeId} ${state.status} ${state.version}`);
}

function bucketOf(key: NodeId): number {
  return parseInt(sha256(`bucket/${key}`).slice(0, 8), 16) % BUCKET_COUNT;
}

export interface MerkleNode {
  hash: string;
  depth: number;
  /** Non-null only on leaves: the state keys stored in this bucket (sorted). */
  keys: NodeId[] | null;
  children: MerkleNode[] | null;
}

export interface ChildInfo {
  hash: string;
  /** Non-null when the child is a leaf bucket. */
  keys: NodeId[] | null;
}

/**
 * Merkle tree over the membership table. Shape is independent of the data
 * (fixed bucket count), so two replicas can compare level by level.
 */
export class MerkleTree {
  readonly root: MerkleNode;

  constructor(states: Iterable<NodeState>) {
    const buckets: NodeState[][] = Array.from({ length: BUCKET_COUNT }, () => []);
    for (const state of states) buckets[bucketOf(state.nodeId)].push(state);

    const leaves: MerkleNode[] = buckets.map((bucket) => {
      bucket.sort((a, b) => (a.nodeId < b.nodeId ? -1 : 1));
      if (bucket.length === 0) {
        return { hash: EMPTY_HASH, depth: TREE_DEPTH, keys: [], children: null };
      }
      const hash = sha256(bucket.map((s) => `${s.nodeId}=${hashState(s)}`).join(','));
      return { hash, depth: TREE_DEPTH, keys: bucket.map((s) => s.nodeId), children: null };
    });

    let level = leaves;
    let depth = TREE_DEPTH;
    while (level.length > 1) {
      depth--;
      const next: MerkleNode[] = [];
      for (let i = 0; i < level.length; i += BRANCHING_FACTOR) {
        const children = level.slice(i, i + BRANCHING_FACTOR);
        next.push({
          hash: sha256(children.map((c) => c.hash).join('|')),
          depth,
          keys: null,
          children,
        });
      }
      level = next;
    }
    this.root = level[0];
  }

  get rootHash(): string {
    return this.root.hash;
  }

  /** Children of the node at `path` (indices from the root). Empty for leaves. */
  childrenOf(path: number[]): ChildInfo[] {
    let node: MerkleNode = this.root;
    for (const index of path) {
      if (!node.children) return [];
      node = node.children[index];
    }
    if (!node.children) return [];
    return node.children.map((c) => ({ hash: c.hash, keys: c.keys }));
  }
}

/** Read-only view of a Merkle tree; the remote side is typically a peer RPC stub. */
export interface TreeAccessor {
  rootHash(): string;
  childrenOf(path: number[]): ChildInfo[];
}

export interface DiffResult {
  /** Keys whose leaf buckets differ between the two replicas (sorted). */
  keys: NodeId[];
  /** How many tree levels were traversed (root level = 1). */
  levelsTraversed: number;
  /** Child-hash comparisons performed — small when the tree prunes well. */
  nodesCompared: number;
  /** Remote `childrenOf` calls (one network round trip per level per branch). */
  roundTrips: number;
}

/**
 * Depth-by-depth (BFS) reconciliation: compare root hashes, then descend only
 * into children whose hashes differ, until differing leaf buckets yield the
 * exact set of divergent keys.
 */
export function diffByDepth(local: TreeAccessor, remote: TreeAccessor): DiffResult {
  const keys = new Set<NodeId>();
  let levelsTraversed = 0;
  let nodesCompared = 0;
  let roundTrips = 0;

  if (local.rootHash() === remote.rootHash()) {
    return { keys: [], levelsTraversed, nodesCompared, roundTrips };
  }

  let frontier: number[][] = [[]];
  while (frontier.length > 0) {
    levelsTraversed++;
    const next: number[][] = [];
    for (const path of frontier) {
      const localChildren = local.childrenOf(path);
      const remoteChildren = remote.childrenOf(path);
      roundTrips++;
      const width = Math.max(localChildren.length, remoteChildren.length);
      for (let i = 0; i < width; i++) {
        const l = localChildren[i];
        const r = remoteChildren[i];
        const lHash = l?.hash ?? EMPTY_HASH;
        const rHash = r?.hash ?? EMPTY_HASH;
        nodesCompared++;
        if (lHash === rHash) continue;
        const lKeys = l?.keys ?? null;
        const rKeys = r?.keys ?? null;
        if (lKeys !== null || rKeys !== null) {
          for (const k of lKeys ?? []) keys.add(k);
          for (const k of rKeys ?? []) keys.add(k);
        } else {
          next.push([...path, i]);
        }
      }
    }
    frontier = next;
  }

  return { keys: [...keys].sort(), levelsTraversed, nodesCompared, roundTrips };
}
