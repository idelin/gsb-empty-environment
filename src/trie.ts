import { BranchNode, EMPTY_SUMMARY, ExtensionNode, LeafNode } from './node.ts';
import type { RangeSummary, TrieNode } from './node.ts';
import { commonPrefixLength, keyToNibbles, nibblesToHex } from './nibbles.ts';
import type { Nibbles } from './nibbles.ts';
import { serializeNode } from './proof.ts';
import type { Proof } from './proof.ts';

export interface Entry {
  key: string;
  value: Buffer;
}

export class MerklePatriciaTrie {
  private rootNode: TrieNode | null = null;

  get rootHash(): string | null {
    return this.rootNode === null ? null : this.rootNode.hash;
  }

  get summary(): RangeSummary {
    return this.rootNode === null ? { ...EMPTY_SUMMARY } : this.rootNode.summary;
  }

  get size(): number {
    return this.summary.count;
  }

  put(key: string, value: string | Buffer): void {
    if (key.length === 0) throw new Error('key must not be empty');
    const buf = typeof value === 'string' ? Buffer.from(value, 'utf8') : value;
    this.rootNode = this.insert(this.rootNode, key, keyToNibbles(key), buf);
  }

  get(key: string): Buffer | null {
    let node: TrieNode | null = this.rootNode;
    let nibbles = keyToNibbles(key);
    while (node !== null) {
      if (node instanceof LeafNode) {
        return nibblesToHex(node.path) === nibblesToHex(nibbles) && node.key === key
          ? node.value
          : null;
      }
      if (node instanceof ExtensionNode) {
        if (nibbles.length < node.path.length) return null;
        for (let i = 0; i < node.path.length; i++) {
          if (node.path[i] !== nibbles[i]) return null;
        }
        nibbles = nibbles.slice(node.path.length);
        node = node.child;
        continue;
      }
      const branch = node as BranchNode;
      if (nibbles.length === 0) {
        return branch.key === key ? branch.value : null;
      }
      node = branch.children[nibbles[0]];
      nibbles = nibbles.slice(1);
    }
    return null;
  }

  has(key: string): boolean {
    return this.get(key) !== null;
  }

  getByPrefix(prefix: string): Entry[] {
    const out: Entry[] = [];
    if (this.rootNode === null) return out;
    this.matchPrefix(this.rootNode, keyToNibbles(prefix), out);
    out.sort((a, b) => (a.key < b.key ? -1 : a.key > b.key ? 1 : 0));
    return out;
  }

  prove(key: string): Proof | null {
    const target = this.get(key);
    if (target === null) return null;
    const nodes = [];
    let node: TrieNode | null = this.rootNode;
    let nibbles = keyToNibbles(key);
    while (node !== null) {
      nodes.push(serializeNode(node));
      if (node instanceof LeafNode) break;
      if (node instanceof ExtensionNode) {
        nibbles = nibbles.slice(node.path.length);
        node = node.child;
        continue;
      }
      const branch = node as BranchNode;
      if (nibbles.length === 0) break;
      node = branch.children[nibbles[0]];
      nibbles = nibbles.slice(1);
    }
    return { key, value: target.toString('base64'), nodes };
  }

  describe(): string {
    if (this.rootNode === null) return '(empty trie)';
    const lines: string[] = [];
    this.describeNode(this.rootNode, '', lines);
    return lines.join('\n');
  }

  private describeNode(node: TrieNode, indent: string, lines: string[]): void {
    const short = node.hash.slice(0, 12);
    const s = node.summary;
    const range = `count=${s.count} range=[${s.minKey ?? ''} .. ${s.maxKey ?? ''}]`;
    if (node instanceof LeafNode) {
      lines.push(`${indent}Leaf path=${nibblesToHex(node.path)} key=${JSON.stringify(node.key)} value=${JSON.stringify(node.value.toString('utf8'))} hash=${short}.. ${range}`);
      return;
    }
    if (node instanceof ExtensionNode) {
      lines.push(`${indent}Extension path=${nibblesToHex(node.path)} hash=${short}.. ${range}`);
      this.describeNode(node.child, indent + '  ', lines);
      return;
    }
    const branch = node as BranchNode;
    const own = branch.value !== null ? ` key=${JSON.stringify(branch.key)} value=${JSON.stringify(branch.value.toString('utf8'))}` : '';
    lines.push(`${indent}Branch hash=${short}..${own} ${range}`);
    for (let i = 0; i < 16; i++) {
      const child = branch.children[i];
      if (child !== null) {
        lines.push(`${indent}  [${i.toString(16)}]`);
        this.describeNode(child, indent + '    ', lines);
      }
    }
  }

  private insert(node: TrieNode | null, key: string, path: Nibbles, value: Buffer): TrieNode {
    if (node === null) {
      return new LeafNode(path, key, value);
    }

    if (node instanceof LeafNode) {
      const common = commonPrefixLength(node.path, path);
      if (common === node.path.length && common === path.length) {
        return new LeafNode(path, key, value);
      }
      let branch = new BranchNode(new Array(16).fill(null));
      branch = this.placeLeaf(branch, node.path.slice(common), node.key, node.value);
      branch = this.placeLeaf(branch, path.slice(common), key, value);
      if (common > 0) {
        return new ExtensionNode(path.slice(0, common), branch);
      }
      return branch;
    }

    if (node instanceof ExtensionNode) {
      const common = commonPrefixLength(node.path, path);
      if (common === node.path.length) {
        const child = this.insert(node.child, key, path.slice(common), value);
        return new ExtensionNode(node.path, child);
      }
      const children: (TrieNode | null)[] = new Array(16).fill(null);
      const extRest = node.path.slice(common);
      if (extRest.length === 1) {
        children[extRest[0]] = node.child;
      } else {
        children[extRest[0]] = new ExtensionNode(extRest.slice(1), node.child);
      }
      let branch = new BranchNode(children);
      branch = this.placeLeaf(branch, path.slice(common), key, value);
      if (common > 0) {
        return new ExtensionNode(path.slice(0, common), branch);
      }
      return branch;
    }

    const branch = node as BranchNode;
    if (path.length === 0) {
      return new BranchNode([...branch.children], value, key);
    }
    const children = [...branch.children];
    children[path[0]] = this.insert(children[path[0]], key, path.slice(1), value);
    return new BranchNode(children, branch.value, branch.key);
  }

  private placeLeaf(branch: BranchNode, rest: Nibbles, key: string, value: Buffer): BranchNode {
    if (rest.length === 0) {
      return new BranchNode([...branch.children], value, key);
    }
    const children = [...branch.children];
    children[rest[0]] = new LeafNode(rest.slice(1), key, value);
    return new BranchNode(children, branch.value, branch.key);
  }

  private matchPrefix(node: TrieNode, prefix: Nibbles, out: Entry[]): void {
    if (node instanceof LeafNode) {
      if (prefix.length <= node.path.length && startsWith(node.path, prefix)) {
        out.push({ key: node.key, value: node.value });
      }
      return;
    }
    if (node instanceof ExtensionNode) {
      if (prefix.length <= node.path.length) {
        if (startsWith(node.path, prefix)) this.collectAll(node.child, out);
        return;
      }
      if (!startsWith(prefix, node.path)) return;
      this.matchPrefix(node.child, prefix.slice(node.path.length), out);
      return;
    }
    const branch = node as BranchNode;
    if (prefix.length === 0) {
      this.collectAll(branch, out);
      return;
    }
    const child = branch.children[prefix[0]];
    if (child !== null) {
      this.matchPrefix(child, prefix.slice(1), out);
    }
  }

  private collectAll(node: TrieNode, out: Entry[]): void {
    if (node instanceof LeafNode) {
      out.push({ key: node.key, value: node.value });
      return;
    }
    if (node instanceof ExtensionNode) {
      this.collectAll(node.child, out);
      return;
    }
    const branch = node as BranchNode;
    if (branch.value !== null && branch.key !== null) {
      out.push({ key: branch.key, value: branch.value });
    }
    for (const child of branch.children) {
      if (child !== null) this.collectAll(child, out);
    }
  }
}

function startsWith(nibbles: Nibbles, prefix: Nibbles): boolean {
  if (prefix.length > nibbles.length) return false;
  for (let i = 0; i < prefix.length; i++) {
    if (nibbles[i] !== prefix[i]) return false;
  }
  return true;
}
