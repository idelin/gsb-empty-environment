// Merkle Patricia Trie: persistent (copy-on-write) nodes stored by hash.
// Insert splits compressed paths automatically; every write produces a new
// root hash, so any tampering with stored nodes is detectable by comparing
// root hashes or by proof verification.

import type {
  BranchNode,
  ExtensionNode,
  LeafNode,
  NodeHash,
  RangeSummary,
  StoredNode,
  TrieNode,
} from './nodes.ts';
import { computeSummary, emptyBranch, hashNode } from './nodes.ts';
import type { Nibbles } from './encoding.ts';
import {
  commonPrefixLength,
  keyToNibbles,
  nibblesToHex,
  nibblesToKey,
} from './encoding.ts';
import type { Proof, ProofNode } from './proof.ts';

export class MerklePatriciaTrie {
  private db = new Map<NodeHash, StoredNode>();
  rootHash: NodeHash | null = null;

  /** Store a node, returning its content hash. */
  private put(node: TrieNode): NodeHash {
    const summary = computeSummary(node, (hash) => {
      const stored = this.db.get(hash);
      if (!stored) throw new Error(`missing child node ${hash}`);
      return stored.summary;
    });
    const hash = hashNode(node, summary);
    this.db.set(hash, { node, summary });
    return hash;
  }

  private getStored(hash: NodeHash): StoredNode {
    const stored = this.db.get(hash);
    if (!stored) throw new Error(`node not found: ${hash}`);
    return stored;
  }

  insert(key: string, value: Buffer | string): void {
    const nibbles = keyToNibbles(key);
    const buf = typeof value === 'string' ? Buffer.from(value, 'utf8') : value;
    this.rootHash = this.insertAt(this.rootHash, nibbles, buf);
  }

  private insertAt(hash: NodeHash | null, key: Nibbles, value: Buffer): NodeHash {
    if (hash === null) {
      return this.put({ kind: 'leaf', path: key, value } satisfies LeafNode);
    }
    const node = this.getStored(hash).node;

    if (node.kind === 'leaf') {
      const shared = commonPrefixLength(node.path, key);
      const restOld = node.path.slice(shared);
      const restNew = key.slice(shared);
      if (restOld.length === 0 && restNew.length === 0) {
        return this.put({ kind: 'leaf', path: node.path, value });
      }
      const branch = emptyBranch();
      if (restOld.length === 0) branch.value = node.value;
      else
        branch.children[restOld[0]] = this.put({
          kind: 'leaf',
          path: restOld.slice(1),
          value: node.value,
        });
      if (restNew.length === 0) branch.value = value;
      else
        branch.children[restNew[0]] = this.put({
          kind: 'leaf',
          path: restNew.slice(1),
          value,
        });
      const branchHash = this.put(branch);
      if (shared > 0) {
        return this.put({
          kind: 'extension',
          path: key.slice(0, shared),
          child: branchHash,
        } satisfies ExtensionNode);
      }
      return branchHash;
    }

    if (node.kind === 'extension') {
      const shared = commonPrefixLength(node.path, key);
      if (shared === node.path.length) {
        const child = this.insertAt(node.child, key.slice(shared), value);
        return this.put({ kind: 'extension', path: node.path, child });
      }
      const restExt = node.path.slice(shared);
      const restNew = key.slice(shared);
      const branch = emptyBranch();
      if (restExt.length === 1) {
        branch.children[restExt[0]] = node.child;
      } else {
        branch.children[restExt[0]] = this.put({
          kind: 'extension',
          path: restExt.slice(1),
          child: node.child,
        });
      }
      if (restNew.length === 0) branch.value = value;
      else
        branch.children[restNew[0]] = this.put({
          kind: 'leaf',
          path: restNew.slice(1),
          value,
        });
      const branchHash = this.put(branch);
      if (shared > 0) {
        return this.put({
          kind: 'extension',
          path: key.slice(0, shared),
          child: branchHash,
        });
      }
      return branchHash;
    }

    // branch
    const branch: BranchNode = {
      kind: 'branch',
      children: [...node.children],
      value: node.value,
    };
    if (key.length === 0) {
      branch.value = value;
    } else {
      branch.children[key[0]] = this.insertAt(branch.children[key[0]], key.slice(1), value);
    }
    return this.put(branch);
  }

  get(key: string): Buffer | null {
    if (this.rootHash === null) return null;
    const nibbles = keyToNibbles(key);
    let hash: NodeHash | null = this.rootHash;
    let depth = 0;
    while (hash !== null) {
      const node = this.getStored(hash).node;
      if (node.kind === 'leaf') {
        const remaining = nibbles.slice(depth);
        return remaining.length === node.path.length &&
          commonPrefixLength(remaining, node.path) === remaining.length
          ? node.value
          : null;
      }
      if (node.kind === 'extension') {
        const remaining = nibbles.slice(depth);
        if (
          remaining.length < node.path.length ||
          commonPrefixLength(remaining, node.path) !== node.path.length
        ) {
          return null;
        }
        depth += node.path.length;
        hash = node.child;
        continue;
      }
      if (depth === nibbles.length) return node.value;
      hash = node.children[nibbles[depth]];
      depth += 1;
    }
    return null;
  }

  /** Root range summary: [minKey, maxKey] and total size of the tree. */
  summary(): (RangeSummary & { minKey: string; maxKey: string }) | null {
    if (this.rootHash === null) return null;
    const { summary } = this.getStored(this.rootHash);
    return {
      ...summary,
      minKey: nibblesToKey(hexToNibblesChecked(summary.min)),
      maxKey: nibblesToKey(hexToNibblesChecked(summary.max)),
    };
  }

  /** Inclusion proof: node sequence root -> leaf with sibling summaries. */
  getProof(key: string): Proof {
    if (this.rootHash === null) throw new Error('empty trie');
    const nibbles = keyToNibbles(key);
    const nodes: ProofNode[] = [];
    let hash: NodeHash | null = this.rootHash;
    let depth = 0;
    let value: string | null = null;

    while (hash !== null) {
      const stored = this.getStored(hash);
      const node = stored.node;
      nodes.push({
        hash,
        node,
        childSummaries: this.childSummariesOf(node),
      });
      if (node.kind === 'leaf') {
        const remaining = nibbles.slice(depth);
        if (
          remaining.length !== node.path.length ||
          commonPrefixLength(remaining, node.path) !== remaining.length
        ) {
          throw new Error(`key not present: ${key}`);
        }
        value = node.value.toString('hex');
        break;
      }
      if (node.kind === 'extension') {
        const remaining = nibbles.slice(depth);
        if (
          remaining.length < node.path.length ||
          commonPrefixLength(remaining, node.path) !== node.path.length
        ) {
          throw new Error(`key not present: ${key}`);
        }
        depth += node.path.length;
        hash = node.child;
        continue;
      }
      if (depth === nibbles.length) {
        if (node.value === null) throw new Error(`key not present: ${key}`);
        value = node.value.toString('hex');
        break;
      }
      hash = node.children[nibbles[depth]];
      depth += 1;
      if (hash === null) throw new Error(`key not present: ${key}`);
    }
    return { rootHash: this.rootHash, key, value, nodes };
  }

  private childSummariesOf(
    node: TrieNode,
  ): ({ hash: NodeHash; summary: RangeSummary } | null)[] {
    const refs: (NodeHash | null)[] =
      node.kind === 'branch' ? node.children : node.kind === 'extension' ? [node.child] : [];
    return refs.map((hash) =>
      hash === null ? null : { hash, summary: this.getStored(hash).summary },
    );
  }

  /** All key/value pairs whose key starts with `prefix`, in sorted order. */
  keysWithPrefix(prefix: string): [string, Buffer][] {
    if (this.rootHash === null) return [];
    const target = keyToNibbles(prefix);
    const out: [string, Buffer][] = [];
    const walk = (hash: NodeHash, path: Nibbles): void => {
      const node = this.getStored(hash).node;
      if (node.kind === 'leaf') {
        const full = [...path, ...node.path];
        if (startsWith(full, target)) out.push([nibblesToKey(full), node.value]);
        return;
      }
      if (node.kind === 'extension') {
        const full = [...path, ...node.path];
        // prune: extension path must be compatible with the sought prefix
        const n = Math.min(full.length, target.length);
        if (commonPrefixLength(full.slice(0, n), target.slice(0, n)) !== n) return;
        if (full.length <= target.length || startsWith(full, target)) {
          walk(node.child, full);
        }
        return;
      }
      if (startsWith(path, target) && node.value !== null) {
        out.push([nibblesToKey(path), node.value]);
      }
      for (let i = 0; i < 16; i++) {
        const child = node.children[i];
        if (child === null) continue;
        const childPath = [...path, i];
        const n = Math.min(childPath.length, target.length);
        if (commonPrefixLength(childPath.slice(0, n), target.slice(0, n)) !== n) continue;
        walk(child, childPath);
      }
    };
    walk(this.rootHash, []);
    out.sort((a, b) => (a[0] < b[0] ? -1 : a[0] > b[0] ? 1 : 0));
    return out;
  }

  /** Indented rendering of the tree with hashes and range summaries. */
  prettyPrint(): string {
    if (this.rootHash === null) return '(empty)';
    const lines: string[] = [];
    const walk = (hash: NodeHash, indent: string, label: string): void => {
      const { node, summary } = this.getStored(hash);
      const short = hash.slice(0, 12);
      const range = `[${summary.min || '∅'}..${summary.max || '∅'}] n=${summary.size}`;
      if (node.kind === 'leaf') {
        lines.push(
          `${indent}${label}Leaf path=${nibblesToHex(node.path)} value=${node.value.toString('utf8')} #${short} ${range}`,
        );
      } else if (node.kind === 'extension') {
        lines.push(
          `${indent}${label}Ext  path=${nibblesToHex(node.path)} #${short} ${range}`,
        );
        walk(node.child, indent + '    ', '└─ ');
      } else {
        const v = node.value === null ? '' : ` value=${node.value.toString('utf8')}`;
        lines.push(`${indent}${label}Branch${v} #${short} ${range}`);
        for (let i = 0; i < 16; i++) {
          const child = node.children[i];
          if (child !== null) walk(child, indent + '    ', `[${i.toString(16)}] `);
        }
      }
    };
    walk(this.rootHash, '', '');
    return lines.join('\n');
  }
}

function startsWith(path: Nibbles, prefix: Nibbles): boolean {
  return path.length >= prefix.length && commonPrefixLength(path, prefix) === prefix.length;
}

function hexToNibblesChecked(hex: string): Nibbles {
  const out: Nibbles = new Array(hex.length);
  for (let i = 0; i < hex.length; i++) out[i] = parseInt(hex[i], 16);
  return out;
}
