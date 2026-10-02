/**
 * Order-statistic treap (randomized balanced BST).
 *
 * Supports insert / remove / select-by-rank in O(log n) expected time.
 * Keys are ordered by a caller-supplied comparator; a composite comparator
 * (e.g. value then unique id) turns the tree into a multiset.
 */

export type Compare<T> = (a: T, b: T) => number;

interface TreapNode<T> {
  key: T;
  priority: number;
  size: number;
  left: TreapNode<T> | null;
  right: TreapNode<T> | null;
}

function sizeOf<T>(node: TreapNode<T> | null): number {
  return node === null ? 0 : node.size;
}

function pull<T>(node: TreapNode<T>): void {
  node.size = 1 + sizeOf(node.left) + sizeOf(node.right);
}

export class OrderStatisticTree<T> {
  private root: TreapNode<T> | null = null;
  private readonly cmp: Compare<T>;
  private readonly rand: () => number;

  constructor(cmp: Compare<T>, rand: () => number = Math.random) {
    this.cmp = cmp;
    this.rand = rand;
  }

  get size(): number {
    return sizeOf(this.root);
  }

  /** Insert a key that is not already present (per the comparator). */
  insert(key: T): void {
    this.root = this.insertNode(this.root, key);
  }

  /** Remove one key equal to `key`. Returns false when absent. */
  remove(key: T): boolean {
    const [root, removed] = this.removeNode(this.root, key);
    this.root = root;
    return removed;
  }

  /** Return the key at 0-based rank `k` in sorted order. */
  select(k: number): T {
    if (k < 0 || k >= this.size) {
      throw new RangeError(`rank ${k} out of bounds for size ${this.size}`);
    }
    let node = this.root;
    let rank = k;
    while (node !== null) {
      const leftSize = sizeOf(node.left);
      if (rank < leftSize) {
        node = node.left;
      } else if (rank === leftSize) {
        return node.key;
      } else {
        rank -= leftSize + 1;
        node = node.right;
      }
    }
    throw new Error("unreachable");
  }

  private insertNode(node: TreapNode<T> | null, key: T): TreapNode<T> {
    if (node === null) {
      return { key, priority: this.rand(), size: 1, left: null, right: null };
    }
    if (this.cmp(key, node.key) < 0) {
      node.left = this.insertNode(node.left, key);
      if (node.left!.priority > node.priority) {
        node = this.rotateRight(node);
      }
    } else {
      node.right = this.insertNode(node.right, key);
      if (node.right!.priority > node.priority) {
        node = this.rotateLeft(node);
      }
    }
    pull(node);
    return node;
  }

  private removeNode(
    node: TreapNode<T> | null,
    key: T,
  ): [TreapNode<T> | null, boolean] {
    if (node === null) {
      return [null, false];
    }
    const order = this.cmp(key, node.key);
    if (order < 0) {
      const [left, removed] = this.removeNode(node.left, key);
      node.left = left;
      pull(node);
      return [node, removed];
    }
    if (order > 0) {
      const [right, removed] = this.removeNode(node.right, key);
      node.right = right;
      pull(node);
      return [node, removed];
    }
    return [this.merge(node.left, node.right), true];
  }

  private merge(
    left: TreapNode<T> | null,
    right: TreapNode<T> | null,
  ): TreapNode<T> | null {
    if (left === null) return right;
    if (right === null) return left;
    if (left.priority > right.priority) {
      left.right = this.merge(left.right, right);
      pull(left);
      return left;
    }
    right.left = this.merge(left, right.left);
    pull(right);
    return right;
  }

  private rotateRight(node: TreapNode<T>): TreapNode<T> {
    const pivot = node.left!;
    node.left = pivot.right;
    pivot.right = node;
    pull(node);
    pull(pivot);
    return pivot;
  }

  private rotateLeft(node: TreapNode<T>): TreapNode<T> {
    const pivot = node.right!;
    node.right = pivot.left;
    pivot.left = node;
    pull(node);
    pull(pivot);
    return pivot;
  }
}
