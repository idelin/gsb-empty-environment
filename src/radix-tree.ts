/**
 * Compressed radix tree (Patricia trie) over byte-string keys.
 *
 * - Keys are `Uint8Array` byte strings and may be empty.
 * - Edge labels are always non-empty; sibling edges have distinct first bytes.
 * - Deletion compresses the tree: a non-terminal node with a single child is
 *   merged into that child (terminal markers are always preserved).
 * - Iterators are bound to the tree version at creation and iterate a stable
 *   snapshot in byte-lexicographic order, page by page.
 */

export type Bytes = Uint8Array;

/** Byte-wise lexicographic comparison; shorter prefix sorts first. */
export function compareBytes(a: Bytes, b: Bytes): number {
  const n = Math.min(a.length, b.length);
  for (let i = 0; i < n; i++) {
    if (a[i] !== b[i]) return a[i] - b[i];
  }
  return a.length - b.length;
}

function commonPrefixLength(label: Bytes, key: Bytes, keyOffset: number): number {
  const n = Math.min(label.length, key.length - keyOffset);
  let i = 0;
  while (i < n && label[i] === key[keyOffset + i]) i++;
  return i;
}

function concatAll(parts: Bytes[]): Bytes {
  let total = 0;
  for (const p of parts) total += p.length;
  const out = new Uint8Array(total);
  let off = 0;
  for (const p of parts) {
    out.set(p, off);
    off += p.length;
  }
  return out;
}

interface Edge<V> {
  label: Bytes; // always non-empty
  child: RadixNode<V>;
}

class RadixNode<V> {
  terminal = false;
  value: V | undefined = undefined;
  // Sorted by label[0]; first bytes are unique across siblings.
  edges: Array<Edge<V>> = [];
}

/** First index whose edge label starts with `firstByte`, or the insertion point. */
function lowerBound<V>(edges: Array<Edge<V>>, firstByte: number): number {
  let lo = 0;
  let hi = edges.length;
  while (lo < hi) {
    const mid = (lo + hi) >>> 1;
    if (edges[mid].label[0] < firstByte) lo = mid + 1;
    else hi = mid;
  }
  return lo;
}

export interface Page<V> {
  entries: Array<[Bytes, V]>;
  done: boolean;
}

const EMPTY: Bytes = new Uint8Array(0);

export class RadixTree<V> {
  private root: RadixNode<V> = new RadixNode<V>();
  private _size = 0;
  private _version = 0;

  get size(): number {
    return this._size;
  }

  /** Increments on every successful mutation. Iterators capture this value. */
  get version(): number {
    return this._version;
  }

  has(key: Bytes): boolean {
    const node = this.findNode(key);
    return node !== null && node.terminal;
  }

  get(key: Bytes): V | undefined {
    const node = this.findNode(key);
    return node !== null && node.terminal ? node.value : undefined;
  }

  /** Insert or overwrite. Returns true when the key is newly added. */
  set(key: Bytes, value: V): boolean {
    let node = this.root;
    let depth = 0;
    for (;;) {
      if (depth === key.length) {
        const isNew = !node.terminal;
        if (isNew) this._size++;
        node.terminal = true;
        node.value = value;
        this._version++;
        return isNew;
      }
      const pos = lowerBound(node.edges, key[depth]);
      const edge =
        pos < node.edges.length && node.edges[pos].label[0] === key[depth]
          ? node.edges[pos]
          : null;
      if (edge === null) {
        const child = new RadixNode<V>();
        child.terminal = true;
        child.value = value;
        node.edges.splice(pos, 0, { label: key.slice(depth), child });
        this._size++;
        this._version++;
        return true;
      }
      const matchLen = commonPrefixLength(edge.label, key, depth);
      if (matchLen === edge.label.length) {
        depth += matchLen;
        node = edge.child;
        continue;
      }
      // Split the edge in the middle: prefix stays on the existing edge,
      // the old child hangs off a fresh intermediate node.
      const mid = new RadixNode<V>();
      mid.edges.push({ label: edge.label.slice(matchLen), child: edge.child });
      edge.label = edge.label.slice(0, matchLen);
      edge.child = mid;
      if (depth + matchLen === key.length) {
        // The new key ends exactly at the split point.
        mid.terminal = true;
        mid.value = value;
      } else {
        const child = new RadixNode<V>();
        child.terminal = true;
        child.value = value;
        const rest = key.slice(depth + matchLen);
        // The two first bytes differ by construction; keep edges sorted.
        const insertPos = mid.edges[0].label[0] < rest[0] ? 1 : 0;
        mid.edges.splice(insertPos, 0, { label: rest, child });
      }
      this._size++;
      this._version++;
      return true;
    }
  }

  /** Remove a key. Returns true when the key existed. */
  delete(key: Bytes): boolean {
    const nodes: Array<RadixNode<V>> = [this.root];
    const indices: number[] = [];
    let node = this.root;
    let depth = 0;
    while (depth < key.length) {
      const pos = lowerBound(node.edges, key[depth]);
      if (pos >= node.edges.length) return false;
      const edge = node.edges[pos];
      if (edge.label[0] !== key[depth]) return false;
      const matchLen = commonPrefixLength(edge.label, key, depth);
      if (matchLen < edge.label.length) return false;
      depth += matchLen;
      node = edge.child;
      nodes.push(node);
      indices.push(pos);
    }
    if (!node.terminal) return false;
    node.terminal = false;
    node.value = undefined;
    this._size--;
    this._version++;

    // Compress upward: drop childless non-terminal nodes, and merge
    // non-terminal single-child nodes into their only child. Terminal
    // markers are never merged away; the root is exempt from merging.
    for (;;) {
      const top = nodes[nodes.length - 1];
      if (top === this.root || top.terminal) break;
      if (top.edges.length === 0) {
        const parent = nodes[nodes.length - 2];
        parent.edges.splice(indices[indices.length - 1], 1);
        nodes.pop();
        indices.pop();
        continue;
      }
      if (top.edges.length === 1) {
        const parent = nodes[nodes.length - 2];
        const parentEdge = parent.edges[indices[indices.length - 1]];
        const only = top.edges[0];
        parentEdge.label = concatAll([parentEdge.label, only.label]);
        parentEdge.child = only.child;
      }
      break;
    }
    return true;
  }

  /**
   * Longest prefix of `query` that is also a prefix of at least one stored
   * key. May end in the middle of an edge label.
   */
  longestCommonPrefix(query: Bytes): Bytes {
    let node = this.root;
    let depth = 0;
    while (depth < query.length) {
      const pos = lowerBound(node.edges, query[depth]);
      if (pos >= node.edges.length) break;
      const edge = node.edges[pos];
      if (edge.label[0] !== query[depth]) break;
      const matchLen = commonPrefixLength(edge.label, query, depth);
      depth += matchLen;
      if (matchLen < edge.label.length) break; // diverged inside the edge
      node = edge.child;
    }
    return query.slice(0, depth);
  }

  /** Longest common prefix of all stored keys (empty for an empty tree). */
  lcpOfAllKeys(): Bytes {
    const parts: Bytes[] = [];
    let node = this.root;
    while (!node.terminal && node.edges.length === 1) {
      const edge = node.edges[0];
      parts.push(edge.label);
      node = edge.child;
    }
    return concatAll(parts);
  }

  /** All key/value pairs whose key starts with `prefix`, in byte order. */
  entries(prefix: Bytes = EMPTY): Array<[Bytes, V]> {
    const start = this.locate(prefix);
    if (start === null) return [];
    const out: Array<[Bytes, V]> = [];
    const stack: Array<{ node: RadixNode<V>; parts: Bytes[] }> = [
      { node: start.node, parts: start.parts },
    ];
    while (stack.length > 0) {
      const { node, parts } = stack.pop()!;
      if (node.terminal) out.push([concatAll(parts), node.value as V]);
      for (let i = node.edges.length - 1; i >= 0; i--) {
        const edge = node.edges[i];
        stack.push({ node: edge.child, parts: [...parts, edge.label] });
      }
    }
    return out;
  }

  /** All keys starting with `prefix`, in byte-lexicographic order. */
  keys(prefix: Bytes = EMPTY): Bytes[] {
    return this.entries(prefix).map(([k]) => k);
  }

  /**
   * Create an iterator over a stable snapshot of the tree as of its current
   * version, optionally restricted to a key prefix. Later mutations do not
   * affect iteration; `isStale()` reports whether the tree has changed since.
   */
  iterator(prefix: Bytes = EMPTY): RadixIterator<V> {
    return new RadixIterator<V>(this, prefix);
  }

  /** Total number of nodes (diagnostic; useful for verifying compression). */
  nodeCount(): number {
    let count = 0;
    const stack: Array<RadixNode<V>> = [this.root];
    while (stack.length > 0) {
      const node = stack.pop()!;
      count++;
      for (const edge of node.edges) stack.push(edge.child);
    }
    return count;
  }

  /** Structural invariant check; throws on violation. Intended for tests. */
  validate(): void {
    let terminals = 0;
    let previousKey: Bytes | null = null;
    const stack: Array<{ node: RadixNode<V>; parts: Bytes[]; isRoot: boolean }> = [
      { node: this.root, parts: [], isRoot: true },
    ];
    while (stack.length > 0) {
      const { node, parts, isRoot } = stack.pop()!;
      if (!isRoot && !node.terminal) {
        if (node.edges.length === 0) {
          throw new Error("non-terminal leaf node present");
        }
        if (node.edges.length === 1) {
          throw new Error("non-terminal single-child node not merged");
        }
      }
      for (let i = 0; i < node.edges.length; i++) {
        const label = node.edges[i].label;
        if (label.length === 0) throw new Error("empty edge label");
        if (i > 0 && node.edges[i - 1].label[0] >= label[0]) {
          throw new Error("sibling edges not sorted by first byte");
        }
      }
      if (node.terminal) {
        terminals++;
        const key = concatAll(parts);
        if (previousKey !== null && compareBytes(previousKey, key) >= 0) {
          throw new Error("keys not in lexicographic order");
        }
        previousKey = key;
      }
      for (let i = node.edges.length - 1; i >= 0; i--) {
        const edge = node.edges[i];
        stack.push({ node: edge.child, parts: [...parts, edge.label], isRoot: false });
      }
    }
    if (terminals !== this._size) {
      throw new Error(`size mismatch: ${terminals} terminals vs size ${this._size}`);
    }
  }

  private findNode(key: Bytes): RadixNode<V> | null {
    let node = this.root;
    let depth = 0;
    while (depth < key.length) {
      const pos = lowerBound(node.edges, key[depth]);
      if (pos >= node.edges.length) return null;
      const edge = node.edges[pos];
      if (edge.label[0] !== key[depth]) return null;
      const matchLen = commonPrefixLength(edge.label, key, depth);
      if (matchLen < edge.label.length) return null; // key ends mid-edge or diverges
      depth += matchLen;
      node = edge.child;
    }
    return node;
  }

  /**
   * Descend to the deepest node whose path covers `prefix`. The returned
   * parts concatenate to a byte string that starts with `prefix` (the last
   * edge label may extend beyond it when the prefix ends mid-edge).
   */
  private locate(prefix: Bytes): { node: RadixNode<V>; parts: Bytes[] } | null {
    let node = this.root;
    const parts: Bytes[] = [];
    let depth = 0;
    while (depth < prefix.length) {
      const pos = lowerBound(node.edges, prefix[depth]);
      if (pos >= node.edges.length) return null;
      const edge = node.edges[pos];
      if (edge.label[0] !== prefix[depth]) return null;
      const matchLen = commonPrefixLength(edge.label, prefix, depth);
      if (matchLen < edge.label.length) {
        if (depth + matchLen === prefix.length) {
          // Prefix ends strictly inside this edge label; the whole subtree matches.
          parts.push(edge.label);
          return { node: edge.child, parts };
        }
        return null;
      }
      parts.push(edge.label);
      depth += edge.label.length;
      node = edge.child;
    }
    return { node, parts };
  }
}

/**
 * Snapshot iterator bound to the tree version at creation. Yields entries in
 * byte-lexicographic order, either one at a time (`next` / `for...of`) or in
 * pages (`nextPage`). Mutations after creation never affect the snapshot.
 */
export class RadixIterator<V> implements IterableIterator<[Bytes, V]> {
  private readonly tree: RadixTree<V>;
  private readonly snapshot: Array<[Bytes, V]>;
  private cursor = 0;
  /** Tree version captured at creation. */
  readonly version: number;

  constructor(tree: RadixTree<V>, prefix: Bytes) {
    this.tree = tree;
    this.version = tree.version;
    this.snapshot = tree.entries(prefix);
  }

  /** True when the tree has been mutated since this iterator was created. */
  isStale(): boolean {
    return this.tree.version !== this.version;
  }

  /** Number of entries not yet consumed. */
  get remaining(): number {
    return this.snapshot.length - this.cursor;
  }

  /** Read up to `limit` entries from the snapshot. */
  nextPage(limit: number): Page<V> {
    if (!Number.isInteger(limit) || limit <= 0) {
      throw new RangeError("page limit must be a positive integer");
    }
    const entries = this.snapshot.slice(this.cursor, this.cursor + limit);
    this.cursor += entries.length;
    return { entries, done: this.cursor >= this.snapshot.length };
  }

  next(): IteratorResult<[Bytes, V], undefined> {
    if (this.cursor >= this.snapshot.length) {
      return { value: undefined, done: true };
    }
    const value = this.snapshot[this.cursor++];
    return { value, done: false };
  }

  [Symbol.iterator](): IterableIterator<[Bytes, V]> {
    return this;
  }
}
