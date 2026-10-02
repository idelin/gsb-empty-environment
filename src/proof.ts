import { BranchNode, ExtensionNode, LeafNode, hashBranch, hashExtension, hashLeaf } from './node.ts';
import type { RangeSummary, TrieNode } from './node.ts';
import { hexToNibbles, keyToNibbles, nibblesToHex } from './nibbles.ts';
import type { Nibbles } from './nibbles.ts';

export type SerializedNode =
  | { kind: 'leaf'; path: string; key: string; value: string; summary: RangeSummary }
  | { kind: 'extension'; path: string; child: string; summary: RangeSummary }
  | { kind: 'branch'; children: (string | null)[]; value: string | null; key: string | null; summary: RangeSummary };

export interface Proof {
  key: string;
  value: string;
  nodes: SerializedNode[];
}

export function serializeNode(node: TrieNode): SerializedNode {
  if (node instanceof LeafNode) {
    return {
      kind: 'leaf',
      path: nibblesToHex(node.path),
      key: node.key,
      value: node.value.toString('base64'),
      summary: node.summary,
    };
  }
  if (node instanceof ExtensionNode) {
    return {
      kind: 'extension',
      path: nibblesToHex(node.path),
      child: node.child.hash,
      summary: node.summary,
    };
  }
  const branch = node as BranchNode;
  return {
    kind: 'branch',
    children: branch.children.map((c) => (c === null ? null : c.hash)),
    value: branch.value === null ? null : branch.value.toString('base64'),
    key: branch.key,
    summary: branch.summary,
  };
}

export function hashSerializedNode(node: SerializedNode): string {
  if (node.kind === 'leaf') {
    return hashLeaf(node.path, node.key, Buffer.from(node.value, 'base64'), node.summary);
  }
  if (node.kind === 'extension') {
    return hashExtension(node.path, node.child, node.summary);
  }
  return hashBranch(
    node.children,
    node.value === null ? null : Buffer.from(node.value, 'base64'),
    node.key,
    node.summary,
  );
}

function pathMatches(keyNibbles: Nibbles, offset: number, pathHex: string): boolean {
  const path = hexToNibbles(pathHex);
  if (offset + path.length > keyNibbles.length) return false;
  for (let i = 0; i < path.length; i++) {
    if (keyNibbles[offset + i] !== path[i]) return false;
  }
  return true;
}

export function verifyProof(rootHash: string, proof: Proof): boolean {
  if (proof.nodes.length === 0) return false;
  const keyNibbles = keyToNibbles(proof.key);
  let offset = 0;

  for (let i = 0; i < proof.nodes.length; i++) {
    const node = proof.nodes[i];
    const hash = hashSerializedNode(node);
    if (i === 0 && hash !== rootHash) return false;

    const isLast = i === proof.nodes.length - 1;
    const nextHash = isLast ? null : hashSerializedNode(proof.nodes[i + 1]);

    if (node.kind === 'leaf') {
      if (!isLast) return false;
      if (!pathMatches(keyNibbles, offset, node.path)) return false;
      offset += hexToNibbles(node.path).length;
      if (offset !== keyNibbles.length) return false;
      if (node.key !== proof.key) return false;
      if (node.value !== proof.value) return false;
      return true;
    }

    if (node.kind === 'extension') {
      if (isLast) return false;
      if (!pathMatches(keyNibbles, offset, node.path)) return false;
      offset += hexToNibbles(node.path).length;
      if (node.child !== nextHash) return false;
      continue;
    }

    if (isLast) {
      if (offset !== keyNibbles.length) return false;
      if (node.key !== proof.key) return false;
      if (node.value !== proof.value) return false;
      return true;
    }
    if (offset >= keyNibbles.length) return false;
    const slot = keyNibbles[offset];
    offset += 1;
    if (node.children[slot] !== nextHash) return false;
  }
  return false;
}
