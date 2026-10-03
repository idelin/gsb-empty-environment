// Inclusion proofs: the node sequence from root to leaf, plus the range
// summaries of every sibling referenced along the way. A verifier can
// recompute each node's summary and hash bottom-up... actually top-down
// linkage check: each step's referenced child hash must equal the next
// proof node's recomputed hash, and the first must equal the root hash.

import type {
  BranchNode,
  ExtensionNode,
  LeafNode,
  NodeHash,
  RangeSummary,
  TrieNode,
} from './nodes.ts';
import { computeSummary, hashNode } from './nodes.ts';
import type { Nibbles } from './encoding.ts';
import { keyToNibbles, nibblesEqual } from './encoding.ts';

export interface ProofNode {
  hash: NodeHash;
  node: TrieNode;
  /** Summaries of all children referenced by this node (null = empty slot). */
  childSummaries: ({ hash: NodeHash; summary: RangeSummary } | null)[];
}

export interface Proof {
  rootHash: NodeHash;
  key: string;
  value: string | null; // hex-encoded value found at the key, null if absent
  nodes: ProofNode[]; // ordered root -> leaf
}

export interface VerifyResult {
  valid: boolean;
  reason?: string;
}

function summaryOf(proofNode: ProofNode): RangeSummary {
  const byHash = new Map<NodeHash, RangeSummary>();
  for (const entry of proofNode.childSummaries) {
    if (entry) byHash.set(entry.hash, entry.summary);
  }
  return computeSummary(proofNode.node, (hash) => {
    const summary = byHash.get(hash);
    if (!summary) throw new Error(`missing child summary for ${hash}`);
    return summary;
  });
}

function recomputeHash(proofNode: ProofNode): NodeHash {
  return hashNode(proofNode.node, summaryOf(proofNode));
}

function childRefAt(
  node: TrieNode,
  nibbles: Nibbles,
  depth: number,
): { hash: NodeHash; consumed: number } | { error: string } {
  if (node.kind === 'extension') {
    const ext = node as ExtensionNode;
    const remaining = nibbles.slice(depth);
    if (
      remaining.length < ext.path.length ||
      !nibblesEqual(remaining.slice(0, ext.path.length), ext.path)
    ) {
      return { error: 'extension path does not match key' };
    }
    return { hash: ext.child, consumed: ext.path.length };
  }
  if (node.kind === 'branch') {
    const branch = node as BranchNode;
    const idx = nibbles[depth];
    const child = branch.children[idx];
    if (child === null) return { error: `branch has no child at nibble ${idx}` };
    return { hash: child, consumed: 1 };
  }
  return { error: 'cannot descend past a leaf' };
}

/**
 * Verify an inclusion proof against a trusted root hash.
 * Detects: tampered parent hashes, tampered node content, tampered
 * summaries, broken hash linkage, and key/value mismatches.
 */
export function verifyProof(proof: Proof): VerifyResult {
  if (proof.nodes.length === 0) return { valid: false, reason: 'empty proof' };

  const nibbles = keyToNibbles(proof.key);

  // 1. Anchor: first node must hash to the trusted root hash.
  let recomputed: NodeHash;
  try {
    recomputed = recomputeHash(proof.nodes[0]);
  } catch (err) {
    return { valid: false, reason: (err as Error).message };
  }
  if (recomputed !== proof.rootHash) {
    return { valid: false, reason: 'root hash mismatch (tampered or wrong root)' };
  }
  if (proof.nodes[0].hash !== proof.rootHash) {
    return { valid: false, reason: 'claimed node hash does not match root hash' };
  }

  // 2. Walk the path, checking hash linkage at every level.
  let depth = 0;
  for (let i = 0; i < proof.nodes.length; i++) {
    const current = proof.nodes[i];
    let currentHash: NodeHash;
    try {
      currentHash = recomputeHash(current);
    } catch (err) {
      return { valid: false, reason: (err as Error).message };
    }
    if (currentHash !== current.hash) {
      return {
        valid: false,
        reason: `node ${i} content/summary does not match its claimed hash`,
      };
    }

    const node = current.node;
    if (node.kind === 'leaf') {
      const leaf = node as LeafNode;
      const remaining = nibbles.slice(depth);
      if (!nibblesEqual(remaining, leaf.path)) {
        return { valid: false, reason: 'leaf path does not match key suffix' };
      }
      if (i !== proof.nodes.length - 1) {
        return { valid: false, reason: 'proof continues past a leaf' };
      }
      const expected = proof.value === null ? null : Buffer.from(proof.value, 'hex');
      if (expected === null || !leaf.value.equals(expected)) {
        return { valid: false, reason: 'leaf value mismatch' };
      }
      return { valid: true };
    }

    const ref = childRefAt(node, nibbles, depth);
    if ('error' in ref) return { valid: false, reason: `node ${i}: ${ref.error}` };
    depth += ref.consumed;

    const next = proof.nodes[i + 1];
    if (!next) return { valid: false, reason: 'proof ends before reaching a leaf' };
    if (next.hash !== ref.hash) {
      return {
        valid: false,
        reason: `hash linkage broken between node ${i} and node ${i + 1}`,
      };
    }
  }
  return { valid: false, reason: 'proof path exhausted without a leaf' };
}
