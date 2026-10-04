/**
 * Branchable persistent ordered multiset.
 *
 * Each `PersistentMultiset` handle is one version. Versions share nodes
 * structurally; mutations use path copying, so old versions never change.
 * `fork()` branches a version in O(1). `release()` frees exactly the nodes
 * that become unreachable (reference counting on shared nodes).
 * `select(k)` is 0-based. Empty-structure and out-of-range accesses throw
 * dedicated error types.
 */
export class MultisetError extends Error {}

/** Operation required at least one element, but the multiset is empty. */
export class EmptyMultisetError extends MultisetError {}

/** select(k) with k < 0 or k >= size. */
export class IndexOutOfRangeError extends MultisetError {}

/** remove(value) for a value that is not present. */
export class ValueNotFoundError extends MultisetError {}

/** Use of a version after release(). */
export class ReleasedVersionError extends MultisetError {}

export type Comparator<T> = (a: T, b: T) => number;

export const numberComparator: Comparator<number> = (a, b) => a - b;

class Node<T> {
  value: T;
  count: number;
  size: number;
  height: number;
  left: Node<T> | null = null;
  right: Node<T> | null = null;
  refs = 0;

  constructor(value: T, count: number, left: Node<T> | null, right: Node<T> | null) {
    this.value = value;
    this.count = count;
    this.left = left;
    this.right = right;
    this.size = count + nodeSize(left) + nodeSize(right);
    this.height = 1 + Math.max(nodeHeight(left), nodeHeight(right));
  }
}

let liveNodeCount = 0;
let totalAllocated = 0;

/** Nodes currently reachable from at least one live version. */
export function getLiveNodeCount(): number {
  return liveNodeCount;
}

/** Nodes allocated since the module loaded (diagnostics). */
export function getTotalAllocatedNodeCount(): number {
  return totalAllocated;
}

function acquire<T>(node: Node<T> | null): void {
  if (node !== null) node.refs++;
}

function releaseNode<T>(node: Node<T> | null): void {
  if (node === null) return;
  node.refs--;
  if (node.refs === 0) {
    liveNodeCount--;
    const left = node.left;
    const right = node.right;
    node.left = null;
    node.right = null;
    releaseNode(left);
    releaseNode(right);
  }
}

/**
 * Recycle a freshly allocated node that ended up unused (e.g. a subtree
 * replaced by a rotation). Only valid for nodes with refs === 0, i.e. nodes
 * that were never linked into any version. Shared nodes are left untouched.
 * Children kept by the new shape must be acquired before this runs.
 */
function discardIfFresh<T>(node: Node<T> | null): void {
  if (node === null || node.refs !== 0) return;
  liveNodeCount--;
  const left = node.left;
  const right = node.right;
  node.left = null;
  node.right = null;
  releaseNode(left);
  releaseNode(right);
}

function makeNode<T>(
  value: T,
  count: number,
  left: Node<T> | null,
  right: Node<T> | null,
): Node<T> {
  const node = new Node(value, count, left, right);
  liveNodeCount++;
  totalAllocated++;
  acquire(left);
  acquire(right);
  return node;
}

function nodeSize<T>(node: Node<T> | null): number {
  return node === null ? 0 : node.size;
}

function nodeHeight<T>(node: Node<T> | null): number {
  return node === null ? 0 : node.height;
}

/**
 * Build a balanced node from parts, rotating as needed. Never allocates a
 * node that is then discarded: rotations read existing (possibly shared)
 * nodes and only allocate the nodes of the final shape.
 */
function balanceNode<T>(
  value: T,
  count: number,
  left: Node<T> | null,
  right: Node<T> | null,
): Node<T> {
  const balance = nodeHeight(left) - nodeHeight(right);
  if (balance > 1) {
    const l = left as Node<T>;
    if (nodeHeight(l.left) < nodeHeight(l.right)) {
      const lr = l.right as Node<T>;
      const newLeft = makeNode(l.value, l.count, l.left, lr.left);
      const result = makeNode(lr.value, lr.count, newLeft, makeNode(value, count, lr.right, right));
      discardIfFresh(l);
      return result;
    }
    const result = makeNode(l.value, l.count, l.left, makeNode(value, count, l.right, right));
    discardIfFresh(l);
    return result;
  }
  if (balance < -1) {
    const r = right as Node<T>;
    if (nodeHeight(r.right) < nodeHeight(r.left)) {
      const rl = r.left as Node<T>;
      const newRight = makeNode(r.value, r.count, rl.right, r.right);
      const result = makeNode(rl.value, rl.count, makeNode(value, count, left, rl.left), newRight);
      discardIfFresh(r);
      return result;
    }
    const result = makeNode(r.value, r.count, makeNode(value, count, left, r.left), r.right);
    discardIfFresh(r);
    return result;
  }
  return makeNode(value, count, left, right);
}

function insertNode<T>(node: Node<T> | null, value: T, cmp: Comparator<T>): Node<T> {
  if (node === null) return makeNode(value, 1, null, null);
  const c = cmp(value, node.value);
  if (c === 0) return makeNode(node.value, node.count + 1, node.left, node.right);
  if (c < 0) {
    return balanceNode(node.value, node.count, insertNode(node.left, value, cmp), node.right);
  }
  return balanceNode(node.value, node.count, node.left, insertNode(node.right, value, cmp));
}

/** Removes the minimum node; returns [newSubtree, removedMinNode]. */
function removeMinNode<T>(node: Node<T>): [Node<T> | null, Node<T>] {
  if (node.left === null) return [node.right, node];
  const pair = removeMinNode(node.left);
  const newLeft = pair[0];
  const min = pair[1];
  return [balanceNode(node.value, node.count, newLeft, node.right), min];
}

function removeNode<T>(node: Node<T> | null, value: T, cmp: Comparator<T>): Node<T> | null {
  if (node === null) {
    throw new ValueNotFoundError("cannot remove: value is not present in the multiset");
  }
  const c = cmp(value, node.value);
  if (c === 0) {
    if (node.count > 1) {
      return makeNode(node.value, node.count - 1, node.left, node.right);
    }
    if (node.left === null) return node.right;
    if (node.right === null) return node.left;
    const pair = removeMinNode(node.right);
    const newRight = pair[0];
    const min = pair[1];
    return balanceNode(min.value, min.count, node.left, newRight);
  }
  if (c < 0) {
    return balanceNode(node.value, node.count, removeNode(node.left, value, cmp), node.right);
  }
  return balanceNode(node.value, node.count, node.left, removeNode(node.right, value, cmp));
}

export class PersistentMultiset<T> {
  private root: Node<T> | null;
  private readonly cmp: Comparator<T>;
  private released = false;

  constructor(comparator?: Comparator<T>) {
    this.cmp = comparator !== undefined ? comparator : (numberComparator as unknown as Comparator<T>);
    this.root = null;
  }

  private static fromRoot<T>(root: Node<T> | null, cmp: Comparator<T>): PersistentMultiset<T> {
    const ms = new PersistentMultiset<T>(cmp);
    ms.root = root;
    return ms;
  }

  private ensureAlive(): void {
    if (this.released) {
      throw new ReleasedVersionError("this version has been released");
    }
  }

  private setRoot(newRoot: Node<T> | null): void {
    acquire(newRoot);
    const oldRoot = this.root;
    this.root = newRoot;
    releaseNode(oldRoot);
  }

  /** O(1) branch: the new version shares all nodes with this one. */
  fork(): PersistentMultiset<T> {
    this.ensureAlive();
    acquire(this.root);
    return PersistentMultiset.fromRoot(this.root, this.cmp);
  }

  /**
   * Drop this version. Nodes that no other version can reach are reclaimed.
   * Releasing twice (or any later use) throws ReleasedVersionError.
   */
  release(): void {
    this.ensureAlive();
    this.released = true;
    const oldRoot = this.root;
    this.root = null;
    releaseNode(oldRoot);
  }

  get isReleased(): boolean {
    return this.released;
  }

  size(): number {
    this.ensureAlive();
    return nodeSize(this.root);
  }

  isEmpty(): boolean {
    return this.size() === 0;
  }

  /** Insert one copy of value. O(log n), old versions unaffected. */
  insert(value: T): void {
    this.ensureAlive();
    this.setRoot(insertNode(this.root, value, this.cmp));
  }

  /**
   * Remove one copy of value. O(log n).
   * Throws ValueNotFoundError if the value is absent.
   */
  remove(value: T): void {
    this.ensureAlive();
    this.setRoot(removeNode(this.root, value, this.cmp));
  }

  /** Multiplicity of value (0 when absent). */
  countOf(value: T): number {
    this.ensureAlive();
    let node = this.root;
    while (node !== null) {
      const c = this.cmp(value, node.value);
      if (c === 0) return node.count;
      node = c < 0 ? node.left : node.right;
    }
    return 0;
  }

  has(value: T): boolean {
    return this.countOf(value) > 0;
  }

  /**
   * Number of elements strictly less than value
   * (equivalently: the 0-based position where value would be inserted).
   */
  rank(value: T): number {
    this.ensureAlive();
    let rank = 0;
    let node = this.root;
    while (node !== null) {
      const c = this.cmp(value, node.value);
      if (c <= 0) {
        node = node.left;
      } else {
        rank += nodeSize(node.left) + node.count;
        node = node.right;
      }
    }
    return rank;
  }

  /**
   * The k-th smallest element, 0-based.
   * Throws EmptyMultisetError when empty, IndexOutOfRangeError when
   * k < 0 or k >= size().
   */
  select(k: number): T {
    this.ensureAlive();
    const total = nodeSize(this.root);
    if (total === 0) {
      throw new EmptyMultisetError("cannot select from an empty multiset");
    }
    if (!Number.isInteger(k) || k < 0 || k >= total) {
      throw new IndexOutOfRangeError("select(" + k + ") out of range: valid indices are 0.." + (total - 1));
    }
    let node = this.root as Node<T>;
    let index = k;
    while (true) {
      const leftSize = nodeSize(node.left);
      if (index < leftSize) {
        node = node.left as Node<T>;
      } else if (index < leftSize + node.count) {
        return node.value;
      } else {
        index -= leftSize + node.count;
        node = node.right as Node<T>;
      }
    }
  }

  /** Smallest element. Throws EmptyMultisetError when empty. */
  min(): T {
    return this.select(0);
  }

  /** Largest element. Throws EmptyMultisetError when empty. */
  max(): T {
    return this.select(this.size() - 1);
  }

  /** All elements in ascending order, duplicates repeated. */
  toArray(): T[] {
    this.ensureAlive();
    const out: T[] = [];
    const stack: Node<T>[] = [];
    let node = this.root;
    while (node !== null || stack.length > 0) {
      while (node !== null) {
        stack.push(node);
        node = node.left;
      }
      const current = stack.pop() as Node<T>;
      for (let i = 0; i < current.count; i++) out.push(current.value);
      node = current.right;
    }
    return out;
  }

  /**
   * Diagnostics for tests: identity set of every node reachable from this
   * version. The union over all live versions must equal the live pool size.
   */
  collectReachableNodes(): Set<object> {
    this.ensureAlive();
    const seen = new Set<object>();
    const stack: Node<T>[] = [];
    if (this.root !== null) stack.push(this.root);
    while (stack.length > 0) {
      const node = stack.pop() as Node<T>;
      if (seen.has(node)) continue;
      seen.add(node);
      if (node.left !== null) stack.push(node.left);
      if (node.right !== null) stack.push(node.right);
    }
    return seen;
  }
}
