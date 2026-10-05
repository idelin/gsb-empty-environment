/**
 * Persistent ordered map: an AVL tree with path-copying updates.
 *
 * Every update returns a new `PMap`; untouched subtrees are shared by
 * reference with the previous version, which is what allows the diff
 * engine to skip identical regions wholesale. Each node carries a
 * content `hash` summarizing its whole subtree (keys, values, shape).
 */

import { defaultHash, mix3, type HashFn } from './hash.ts';

export type CompareFn<K> = (a: K, b: K) => number;

export function defaultCompare<K>(a: K, b: K): number {
  return a < b ? -1 : a > b ? 1 : 0;
}

export class PNode<K, V> {
  readonly height: number;
  readonly size: number;
  readonly hash: number;
  readonly key: K;
  readonly value: V;
  readonly left: PNode<K, V> | null;
  readonly right: PNode<K, V> | null;

  constructor(
    key: K,
    value: V,
    left: PNode<K, V> | null,
    right: PNode<K, V> | null,
    keyHash: number,
    valueHash: number,
  ) {
    this.key = key;
    this.value = value;
    this.left = left;
    this.right = right;
    const leftHeight = left?.height ?? 0;
    const rightHeight = right?.height ?? 0;
    this.height = 1 + Math.max(leftHeight, rightHeight);
    this.size = 1 + (left?.size ?? 0) + (right?.size ?? 0);
    this.hash = mix3(
      mix3(left?.hash ?? 0, keyHash, valueHash),
      right?.hash ?? 0,
      this.size,
    );
    Object.freeze(this);
  }
}

export interface PMapOptions<K, V> {
  compare?: CompareFn<K>;
  hashKey?: HashFn<K>;
  hashValue?: HashFn<V>;
}

export class PMap<K, V> {
  readonly root: PNode<K, V> | null;
  readonly compare: CompareFn<K>;
  readonly hashKey: HashFn<K>;
  readonly hashValue: HashFn<V>;

  private constructor(
    root: PNode<K, V> | null,
    compare: CompareFn<K>,
    hashKey: HashFn<K>,
    hashValue: HashFn<V>,
  ) {
    this.root = root;
    this.compare = compare;
    this.hashKey = hashKey;
    this.hashValue = hashValue;
  }

  static empty<K, V>(options: PMapOptions<K, V> = {}): PMap<K, V> {
    return new PMap<K, V>(
      null,
      options.compare ?? defaultCompare,
      options.hashKey ?? (defaultHash as HashFn<K>),
      options.hashValue ?? (defaultHash as HashFn<V>),
    );
  }

  static from<K, V>(
    entries: Iterable<readonly [K, V]>,
    options: PMapOptions<K, V> = {},
  ): PMap<K, V> {
    let map = PMap.empty<K, V>(options);
    for (const [key, value] of entries) map = map.set(key, value);
    return map;
  }

  get size(): number {
    return this.root?.size ?? 0;
  }

  private mk(
    key: K,
    value: V,
    left: PNode<K, V> | null,
    right: PNode<K, V> | null,
  ): PNode<K, V> {
    return new PNode<K, V>(
      key,
      value,
      left,
      right,
      this.hashKey(key),
      this.hashValue(value),
    );
  }

  has(key: K): boolean {
    let node = this.root;
    while (node) {
      const cmp = this.compare(key, node.key);
      if (cmp === 0) return true;
      node = cmp < 0 ? node.left : node.right;
    }
    return false;
  }

  get(key: K): V | undefined {
    let node = this.root;
    while (node) {
      const cmp = this.compare(key, node.key);
      if (cmp === 0) return node.value;
      node = cmp < 0 ? node.left : node.right;
    }
    return undefined;
  }

  set(key: K, value: V): PMap<K, V> {
    const root = this.setNode(this.root, key, value);
    if (root === this.root) return this;
    return new PMap(root, this.compare, this.hashKey, this.hashValue);
  }

  private setNode(
    node: PNode<K, V> | null,
    key: K,
    value: V,
  ): PNode<K, V> {
    if (!node) return this.mk(key, value, null, null);
    const cmp = this.compare(key, node.key);
    if (cmp === 0) {
      if (Object.is(node.value, value)) return node;
      return this.mk(key, value, node.left, node.right);
    }
    if (cmp < 0) {
      const left = this.setNode(node.left, key, value);
      if (left === node.left) return node;
      return this.rebalance(this.mk(node.key, node.value, left, node.right));
    }
    const right = this.setNode(node.right, key, value);
    if (right === node.right) return node;
    return this.rebalance(this.mk(node.key, node.value, node.left, right));
  }

  delete(key: K): PMap<K, V> {
    const root = this.deleteNode(this.root, key);
    if (root === this.root) return this;
    return new PMap(root, this.compare, this.hashKey, this.hashValue);
  }

  private deleteNode(
    node: PNode<K, V> | null,
    key: K,
  ): PNode<K, V> | null {
    if (!node) return null;
    const cmp = this.compare(key, node.key);
    if (cmp < 0) {
      const left = this.deleteNode(node.left, key);
      if (left === node.left) return node;
      return this.rebalance(this.mk(node.key, node.value, left, node.right));
    }
    if (cmp > 0) {
      const right = this.deleteNode(node.right, key);
      if (right === node.right) return node;
      return this.rebalance(this.mk(node.key, node.value, node.left, right));
    }
    if (!node.left) return node.right;
    if (!node.right) return node.left;
    // Two children: replace with in-order successor.
    let successor = node.right;
    while (successor.left) successor = successor.left;
    const right = this.deleteNode(node.right, successor.key);
    return this.rebalance(
      this.mk(successor.key, successor.value, node.left, right),
    );
  }

  private rebalance(node: PNode<K, V>): PNode<K, V> {
    const balance = (node.left?.height ?? 0) - (node.right?.height ?? 0);
    if (balance > 1) {
      const left = node.left!;
      if ((left.left?.height ?? 0) >= (left.right?.height ?? 0)) {
        // Left-left: rotate right.
        return this.mk(left.key, left.value, left.left,
          this.mk(node.key, node.value, left.right, node.right));
      }
      // Left-right: rotate left, then right.
      const lr = left.right!;
      const newLeft = this.mk(lr.key, lr.value,
        this.mk(left.key, left.value, left.left, lr.left), lr.right);
      return this.mk(newLeft.key, newLeft.value, newLeft.left,
        this.mk(node.key, node.value, newLeft.right, node.right));
    }
    if (balance < -1) {
      const right = node.right!;
      if ((right.right?.height ?? 0) >= (right.left?.height ?? 0)) {
        // Right-right: rotate left.
        return this.mk(right.key, right.value,
          this.mk(node.key, node.value, node.left, right.left), right.right);
      }
      // Right-left: rotate right, then left.
      const rl = right.left!;
      const newRight = this.mk(rl.key, rl.value, rl.left,
        this.mk(right.key, right.value, rl.right, right.right));
      return this.mk(newRight.key, newRight.value,
        this.mk(node.key, node.value, node.left, newRight.left), newRight.right);
    }
    return node;
  }

  /** In-order (ascending key) entries. */
  *entries(): IterableIterator<readonly [K, V]> {
    const stack: PNode<K, V>[] = [];
    let node = this.root;
    while (node || stack.length > 0) {
      while (node) {
        stack.push(node);
        node = node.left;
      }
      node = stack.pop()!;
      yield [node.key, node.value] as const;
      node = node.right;
    }
  }
}
