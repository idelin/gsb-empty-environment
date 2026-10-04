/**
 * Compressed radix tree (Patricia trie) over byte-string keys.
 *
 * - Keys are arbitrary byte strings (`Uint8Array`); the empty key is allowed
 *   and is stored on the root node. Edge labels are always non-empty.
 * - Supports update (`set`), point lookup (`get`/`has`), prefix enumeration
 *   (`entries`/`iterator`), longest common prefix (`longestCommonPrefix`)
 *   and deletion (`delete`). Deleting a key merges non-terminal single-child
 *   nodes back into their parent edge, while terminal (value-carrying) nodes
 *   are always preserved.
 * - The tree is persistent: every mutation performs copy-on-write along the
 *   affected path and publishes a new root plus a bumped `version`. Iterators
 *   capture the root of the version they were created on, so they keep
 *   traversing that exact snapshot no matter how the tree is mutated later.
 * - Enumeration is in unsigned byte lexicographic order and supports
 *   resumable pagination via `RadixIterator.nextPage(limit)`.
 */

export interface Entry<V> {
  readonly key: Uint8Array;
  readonly value: V;
}

/** Unsigned byte-wise lexicographic comparison; shorter prefix sorts first. */
export function compareBytes(a: Uint8Array, b: Uint8Array): number {
  const n = Math.min(a.length, b.length);
  for (let i = 0; i < n; i++) {
    if (a[i] !== b[i]) return a[i] - b[i];
  }
  return a.length - b.length;
}

/** Length of the longest common prefix of two byte strings. */
export function commonPrefixLength(a: Uint8Array, b: Uint8Array): number {
  const n = Math.min(a.length, b.length);
  let i = 0;
  while (i < n && a[i] === b[i]) i++;
  return i;
}

function concatBytes(a: Uint8Array, b: Uint8Array): Uint8Array {
  const out = new Uint8Array(a.length + b.length);
  out.set(a, 0);
  out.set(b, a.length);
  return out;
}

function startsWithBytes(a: Uint8Array, prefix: Uint8Array): boolean {
  if (a.length < prefix.length) return false;
  for (let i = 0; i < prefix.length; i++) {
    if (a[i] !== prefix[i]) return false;
  }
  return true;
}

class Node<V> {
  hasValue = false;
  value: V | undefined = undefined;
  /** Edges keyed by the first byte of their label. */
  children: Map<number, Edge<V>> = new Map();
}

interface Edge<V> {
  /** Invariant: label.length >= 1. */
  readonly label: Uint8Array;
  readonly child: Node<V>;
}

function cloneNode<V>(node: Node<V>): Node<V> {
  const copy = new Node<V>();
  copy.hasValue = node.hasValue;
  copy.value = node.value;
  copy.children = new Map(node.children);
  return copy;
}

interface PutResult<V> {
  node: Node<V>;
  inserted: boolean;
}

function put<V>(node: Node<V>, key: Uint8Array, depth: number, value: V): PutResult<V> {
  if (depth === key.length) {
    const next = cloneNode(node);
    const inserted = !next.hasValue;
    next.hasValue = true;
    next.value = value;
    return { node: next, inserted };
  }
  const byte = key[depth];
  const edge = node.children.get(byte);
  if (edge === undefined) {
    const leaf = new Node<V>();
    leaf.hasValue = true;
    leaf.value = value;
    const next = cloneNode(node);
    next.children.set(byte, { label: key.slice(depth), child: leaf });
    return { node: next, inserted: true };
  }
  const lcp = commonPrefixLength(edge.label, key.subarray(depth));
  if (lcp === edge.label.length) {
    // The edge label is fully consumed; descend into its child.
    const res = put(edge.child, key, depth + lcp, value);
    const next = cloneNode(node);
    next.children.set(byte, { label: edge.label, child: res.node });
    return { node: next, inserted: res.inserted };
  }
  // Split the edge in the middle: parent --label[0:lcp]--> mid --label[lcp:]--> old child.
  // lcp >= 1 because the edge was found by matching first byte, so both
  // resulting labels are non-empty.
  const mid = new Node<V>();
  mid.children.set(edge.label[lcp], { label: edge.label.slice(lcp), child: edge.child });
  const remaining = key.length - depth - lcp;
  if (remaining === 0) {
    // The new key ends exactly at the split point: mid becomes terminal.
    mid.hasValue = true;
    mid.value = value;
  } else {
    const leaf = new Node<V>();
    leaf.hasValue = true;
    leaf.value = value;
    mid.children.set(key[depth + lcp], { label: key.slice(depth + lcp), child: leaf });
  }
  const next = cloneNode(node);
  next.children.set(byte, { label: edge.label.slice(0, lcp), child: mid });
  return { node: next, inserted: true };
}

interface RemoveResult<V> {
  /** The replacement node, or null if it became empty and must be detached. */
  node: Node<V> | null;
  removed: boolean;
}

function remove<V>(node: Node<V>, key: Uint8Array, depth: number): RemoveResult<V> {
  if (depth === key.length) {
    if (!node.hasValue) return { node, removed: false };
    const next = cloneNode(node);
    next.hasValue = false;
    next.value = undefined;
    return { node: next.children.size > 0 ? next : null, removed: true };
  }
  const byte = key[depth];
  const edge = node.children.get(byte);
  if (edge === undefined) return { node, removed: false };
  if (!startsWithBytes(key.subarray(depth), edge.label)) return { node, removed: false };
  const res = remove(edge.child, key, depth + edge.label.length);
  if (!res.removed) return { node, removed: false };
  const next = cloneNode(node);
  if (res.node === null) {
    next.children.delete(byte);
  } else if (!res.node.hasValue && res.node.children.size === 1) {
    // The child lost its terminal marker and has a single child left:
    // merge it into this edge by concatenating labels. Terminal nodes are
    // never merged away, which preserves key markers.
    const only = res.node.children.values().next().value as Edge<V>;
    next.children.set(byte, { label: concatBytes(edge.label, only.label), child: only.child });
  } else {
    next.children.set(byte, { label: edge.label, child: res.node });
  }
  return { node: next.hasValue || next.children.size > 0 ? next : null, removed: true };
}

function lookup<V>(root: Node<V>, key: Uint8Array): Node<V> | undefined {
  let node = root;
  let depth = 0;
  while (depth < key.length) {
    const edge = node.children.get(key[depth]);
    if (edge === undefined) return undefined;
    if (!startsWithBytes(key.subarray(depth), edge.label)) return undefined;
    depth += edge.label.length;
    node = edge.child;
  }
  return node;
}

function lcpWalk<V>(root: Node<V>, key: Uint8Array): Uint8Array {
  let node = root;
  let depth = 0;
  while (depth < key.length) {
    const edge = node.children.get(key[depth]);
    if (edge === undefined) break;
    const lcp = commonPrefixLength(edge.label, key.subarray(depth));
    depth += lcp;
    if (lcp < edge.label.length) break;
    node = edge.child;
  }
  return key.slice(0, depth);
}

function checkStructure<V>(root: Node<V>, size: number): void {
  let count = 0;
  let prev: Uint8Array | undefined;
  const visit = (node: Node<V>, bytes: Uint8Array, isRoot: boolean): void => {
    if (node.hasValue) {
      count++;
      if (prev !== undefined && compareBytes(prev, bytes) >= 0) {
        throw new Error('radix tree invariant violated: keys out of order');
      }
      prev = bytes.slice();
    }
    if (!isRoot && !node.hasValue && node.children.size < 2) {
      throw new Error('radix tree invariant violated: unmerged non-terminal node');
    }
    const childBytes = Array.from(node.children.keys()).sort((a, b) => a - b);
    for (const byte of childBytes) {
      const edge = node.children.get(byte) as Edge<V>;
      if (edge.label.length === 0) {
        throw new Error('radix tree invariant violated: empty edge label');
      }
      if (edge.label[0] !== byte) {
        throw new Error('radix tree invariant violated: edge keyed by wrong byte');
      }
      visit(edge.child, concatBytes(bytes, edge.label), false);
    }
  };
  visit(root, new Uint8Array(0), true);
  if (count !== size) {
    throw new Error(`radix tree invariant violated: size ${size} but counted ${count}`);
  }
}

interface Frame<V> {
  node: Node<V>;
  /** Key bytes from the root to this node. */
  bytes: Uint8Array;
  /** Sorted first-bytes of outgoing edges. */
  childBytes: number[];
  index: number;
  valueEmitted: boolean;
}

function makeFrame<V>(node: Node<V>, bytes: Uint8Array): Frame<V> {
  return {
    node,
    bytes,
    childBytes: Array.from(node.children.keys()).sort((a, b) => a - b),
    index: 0,
    valueEmitted: false,
  };
}

/**
 * Iterator over the entries of one tree version, in unsigned byte
 * lexicographic order. Bound to the version of the tree at creation time;
 * later mutations of the tree never affect it.
 */
export class RadixIterator<V> implements IterableIterator<Entry<V>> {
  /** The tree version this iterator is bound to. */
  readonly version: number;
  private readonly stack: Array<Frame<V>> = [];

  constructor(root: Node<V>, version: number, prefix: Uint8Array) {
    this.version = version;
    // Navigate to the subtree covering `prefix`. The prefix may end exactly
    // on a node or in the middle of an edge label; in the latter case the
    // whole subtree below that edge matches.
    let node: Node<V> | undefined = root;
    let bytes = new Uint8Array(0);
    let depth = 0;
    while (node !== undefined && depth < prefix.length) {
      const edge = node.children.get(prefix[depth]);
      if (edge === undefined) {
        node = undefined;
        break;
      }
      const rest = prefix.subarray(depth);
      if (rest.length < edge.label.length) {
        // Prefix ends inside this edge label.
        if (startsWithBytes(edge.label, rest)) {
          bytes = concatBytes(bytes, edge.label);
          node = edge.child;
        } else {
          node = undefined;
        }
        break;
      }
      if (!startsWithBytes(rest, edge.label)) {
        node = undefined;
        break;
      }
      bytes = concatBytes(bytes, edge.label);
      depth += edge.label.length;
      node = edge.child;
    }
    if (node !== undefined) {
      this.stack.push(makeFrame(node, bytes));
    }
  }

  private advance(): Entry<V> | null {
    while (this.stack.length > 0) {
      const top = this.stack[this.stack.length - 1];
      if (!top.valueEmitted) {
        top.valueEmitted = true;
        if (top.node.hasValue) {
          return { key: top.bytes.slice(), value: top.node.value as V };
        }
      }
      if (top.index < top.childBytes.length) {
        const byte = top.childBytes[top.index++];
        const edge = top.node.children.get(byte) as Edge<V>;
        this.stack.push(makeFrame(edge.child, concatBytes(top.bytes, edge.label)));
      } else {
        this.stack.pop();
      }
    }
    return null;
  }

  next(): IteratorResult<Entry<V>> {
    const entry = this.advance();
    if (entry === null) return { done: true, value: undefined };
    return { done: false, value: entry };
  }

  /**
   * Return the next page of up to `limit` entries in byte lexicographic
   * order. A short (possibly empty) page means the iteration is exhausted.
   */
  nextPage(limit: number): Array<Entry<V>> {
    if (!Number.isInteger(limit) || limit < 1) {
      throw new RangeError('page limit must be a positive integer');
    }
    const page: Array<Entry<V>> = [];
    while (page.length < limit) {
      const entry = this.advance();
      if (entry === null) break;
      page.push(entry);
    }
    return page;
  }

  [Symbol.iterator](): IterableIterator<Entry<V>> {
    return this;
  }
}

const EMPTY_KEY = new Uint8Array(0);

/** Read-only view of one tree version. Safe to use after further mutations. */
export class RadixSnapshot<V> {
  private readonly root: Node<V>;

  /** The tree version this snapshot was taken at. */
  readonly version: number;
  readonly size: number;

  constructor(root: Node<V>, version: number, size: number) {
    this.root = root;
    this.version = version;
    this.size = size;
  }

  has(key: Uint8Array): boolean {
    const node = lookup(this.root, key);
    return node !== undefined && node.hasValue;
  }

  get(key: Uint8Array): V | undefined {
    const node = lookup(this.root, key);
    return node !== undefined && node.hasValue ? node.value : undefined;
  }

  longestCommonPrefix(key: Uint8Array): Uint8Array {
    return lcpWalk(this.root, key);
  }

  iterator(prefix: Uint8Array = EMPTY_KEY): RadixIterator<V> {
    return new RadixIterator<V>(this.root, this.version, prefix);
  }

  entries(prefix: Uint8Array = EMPTY_KEY): IterableIterator<Entry<V>> {
    return this.iterator(prefix);
  }
}

export class RadixTree<V> {
  private root: Node<V> = new Node<V>();
  private _version = 0;
  private _size = 0;

  /** Bumped by every successful mutation. Iterators bind to a version. */
  get version(): number {
    return this._version;
  }

  get size(): number {
    return this._size;
  }

  has(key: Uint8Array): boolean {
    const node = lookup(this.root, key);
    return node !== undefined && node.hasValue;
  }

  get(key: Uint8Array): V | undefined {
    const node = lookup(this.root, key);
    return node !== undefined && node.hasValue ? node.value : undefined;
  }

  /**
   * Insert or replace the value for `key`. Returns true when a new key was
   * inserted, false when an existing key was updated.
   */
  set(key: Uint8Array, value: V): boolean {
    const res = put(this.root, key, 0, value);
    this.root = res.node;
    if (res.inserted) this._size++;
    this._version++;
    return res.inserted;
  }

  /**
   * Remove `key`. Returns true when the key existed. Non-terminal
   * single-child nodes left behind are merged into their parent edge;
   * terminal nodes are preserved.
   */
  delete(key: Uint8Array): boolean {
    const res = remove(this.root, key, 0);
    if (!res.removed) return false;
    this.root = res.node === null ? new Node<V>() : res.node;
    this._size--;
    this._version++;
    return true;
  }

  /**
   * The longest byte string that is a prefix of `key` and a prefix of at
   * least one key stored in the tree.
   */
  longestCommonPrefix(key: Uint8Array): Uint8Array {
    return lcpWalk(this.root, key);
  }

  /**
   * Create an iterator over the entries whose key starts with `prefix`,
   * bound to the current tree version, in byte lexicographic order.
   */
  iterator(prefix: Uint8Array = EMPTY_KEY): RadixIterator<V> {
    return new RadixIterator<V>(this.root, this._version, prefix);
  }

  entries(prefix: Uint8Array = EMPTY_KEY): IterableIterator<Entry<V>> {
    return this.iterator(prefix);
  }

  *keys(prefix: Uint8Array = EMPTY_KEY): IterableIterator<Uint8Array> {
    for (const entry of this.iterator(prefix)) yield entry.key;
  }

  /** Capture the current version as an immutable read-only view. */
  snapshot(): RadixSnapshot<V> {
    return new RadixSnapshot<V>(this.root, this._version, this._size);
  }

  /** Throw if any structural invariant is violated. Intended for tests. */
  checkInvariants(): void {
    checkStructure(this.root, this._size);
  }
}
