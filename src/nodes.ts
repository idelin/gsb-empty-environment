// Node model, canonical serialization, hashing and range summaries.
//
// Every node is content-addressed: hash = sha256(canonical(node, summary)).
// The range summary (min/max key suffix + subtree size) is committed inside
// the hash, so a parent hash authenticates the key range of its whole
// subtree. Any mutation anywhere cascades to a different root hash.

import { createHash } from 'node:crypto';
import type { Nibbles } from './encoding.ts';
import { nibblesToHex } from './encoding.ts';

export type NodeHash = string; // 64-char lowercase hex (sha256)

export interface LeafNode {
  kind: 'leaf';
  path: Nibbles; // remaining key suffix, compressed
  value: Buffer;
}

export interface ExtensionNode {
  kind: 'extension';
  path: Nibbles; // shared path segment, compressed
  child: NodeHash;
}

export interface BranchNode {
  kind: 'branch';
  children: (NodeHash | null)[]; // always length 16
  value: Buffer | null; // set when a key terminates exactly at this branch
}

export type TrieNode = LeafNode | ExtensionNode | BranchNode;

/** Range summary of the subtree rooted at a node, relative to that node. */
export interface RangeSummary {
  min: string; // hex-encoded nibble suffix of the smallest key in subtree
  max: string; // hex-encoded nibble suffix of the largest key in subtree
  size: number; // number of keys stored in subtree
}

export interface StoredNode {
  node: TrieNode;
  summary: RangeSummary;
}

export function emptyBranch(): BranchNode {
  return { kind: 'branch', children: new Array(16).fill(null), value: null };
}

function compareHex(a: string, b: string): number {
  return a < b ? -1 : a > b ? 1 : 0;
}

/**
 * Derive the range summary of a node from the summaries of its children.
 * `childSummary` resolves a child hash to its stored summary.
 */
export function computeSummary(
  node: TrieNode,
  childSummary: (hash: NodeHash) => RangeSummary,
): RangeSummary {
  switch (node.kind) {
    case 'leaf': {
      const hex = nibblesToHex(node.path);
      return { min: hex, max: hex, size: 1 };
    }
    case 'extension': {
      const prefix = nibblesToHex(node.path);
      const child = childSummary(node.child);
      return { min: prefix + child.min, max: prefix + child.max, size: child.size };
    }
    case 'branch': {
      let min: string | null = null;
      let max: string | null = null;
      let size = 0;
      if (node.value !== null) {
        min = '';
        max = '';
        size = 1;
      }
      for (let i = 0; i < 16; i++) {
        const childHash = node.children[i];
        if (childHash === null) continue;
        const child = childSummary(childHash);
        const edge = i.toString(16);
        const childMin = edge + child.min;
        const childMax = edge + child.max;
        if (min === null || compareHex(childMin, min) < 0) min = childMin;
        if (max === null || compareHex(childMax, max) > 0) max = childMax;
        size += child.size;
      }
      if (min === null || max === null) {
        throw new Error('branch node must have at least one child or a value');
      }
      return { min, max, size };
    }
  }
}

/** Canonical, deterministic serialization of a node (no summary). */
export function serializeNode(node: TrieNode): unknown {
  switch (node.kind) {
    case 'leaf':
      return { k: 'l', p: nibblesToHex(node.path), v: node.value.toString('hex') };
    case 'extension':
      return { k: 'e', p: nibblesToHex(node.path), c: node.child };
    case 'branch':
      return {
        k: 'b',
        c: node.children.map((c) => c ?? ''),
        v: node.value === null ? '' : node.value.toString('hex'),
      };
  }
}

/** Hash commits to both the node content and its range summary. */
export function hashNode(node: TrieNode, summary: RangeSummary): NodeHash {
  const payload = JSON.stringify([serializeNode(node), summary]);
  return createHash('sha256').update(payload).digest('hex');
}
