/**
 * Persistent rope over UTF-8 text.
 *
 * - Every node maintains both `bytes` (UTF-8 length) and `scalars`
 *   (Unicode scalar value count); all positional operations use scalars.
 * - Nodes are immutable. Edits path-copy via split/join, so old roots keep
 *   sharing every unchanged subtree with new roots.
 * - A NodePool reference-counts nodes: releasing a root frees exactly the
 *   nodes no longer reachable from any live root, which makes snapshot
 *   reclamation deterministic and testable.
 * - Balance is AVL by subtree height, maintained by join-based
 *   split/concat (Blelloch et al., "Just Join for Parallel Ordered Sets").
 */

import { countScalars, sliceScalars, utf8ByteLength } from "./utf8";

/** Maximum number of scalars stored in a single leaf. */
export const LEAF_MAX = 32;

export enum NodeKind {
  Leaf = 0,
  Branch = 1,
}

export interface Leaf {
  kind: NodeKind.Leaf;
  text: string;
  bytes: number;
  scalars: number;
  refs: number;
}

export interface Branch {
  kind: NodeKind.Branch;
  left: Node;
  right: Node;
  bytes: number;
  scalars: number;
  height: number;
  refs: number;
}

export type Node = Leaf | Branch;

/**
 * Reference-counted node allocator.
 *
 * Convention: every function that returns a node returns a *fresh
 * reference* the caller owns (constructor refs start at 1). The caller
 * must either `release` it exactly once or transfer ownership (e.g. store
 * it into a history entry). Composite operations retain what they keep.
 */
export class NodePool {
  /** Nodes currently allocated (not yet freed). */
  live = 0;
  totalAllocated = 0;
  totalFreed = 0;

  leaf(text: string): Leaf {
    const node: Leaf = {
      kind: NodeKind.Leaf,
      text,
      bytes: utf8ByteLength(text),
      scalars: countScalars(text),
      refs: 1,
    };
    this.live++;
    this.totalAllocated++;
    return node;
  }

  branch(left: Node, right: Node): Branch {
    const node: Branch = {
      kind: NodeKind.Branch,
      left,
      right,
      bytes: left.bytes + right.bytes,
      scalars: left.scalars + right.scalars,
      height: Math.max(height(left), height(right)) + 1,
      refs: 1,
    };
    this.live++;
    this.totalAllocated++;
    return node;
  }

  retain(node: Node | null): void {
    if (node) node.refs++;
  }

  release(node: Node | null): void {
    if (!node) return;
    node.refs--;
    if (node.refs <= 0) {
      if (node.kind === NodeKind.Branch) {
        this.release(node.left);
        this.release(node.right);
      }
      this.live--;
      this.totalFreed++;
    }
  }
}

export function height(node: Node): number {
  return node.kind === NodeKind.Leaf ? 0 : node.height;
}

/**
 * Creates a parent for two adjacent nodes, merging them into a single
 * leaf when both are small leaves. Returns a fresh reference.
 */
function mkNode(pool: NodePool, left: Node, right: Node): Node {
  if (
    left.kind === NodeKind.Leaf &&
    right.kind === NodeKind.Leaf &&
    left.scalars + right.scalars <= LEAF_MAX
  ) {
    return pool.leaf(left.text + right.text);
  }
  pool.retain(left);
  pool.retain(right);
  return pool.branch(left, right);
}

/**
 * Rebalances a node with children (a, b) whose heights may differ by up
 * to 2, performing single or double rotations as needed. Both inputs must
 * themselves be AVL-balanced. Returns a fresh reference.
 */
function balance(pool: NodePool, a: Node, b: Node): Node {
  if (height(a) > height(b) + 1) {
    const heavy = a as Branch;
    if (height(heavy.left) >= height(heavy.right)) {
      // single right rotation: ((x, y), b) -> (x, (y, b))
      const newRight = mkNode(pool, heavy.right, b);
      const res = mkNode(pool, heavy.left, newRight);
      pool.release(newRight);
      return res;
    }
    // double rotation: ((x, (y1, y2)), b) -> ((x, y1), (y2, b))
    const y = heavy.right as Branch;
    const newLeft = mkNode(pool, heavy.left, y.left);
    const newRight = mkNode(pool, y.right, b);
    const res = mkNode(pool, newLeft, newRight);
    pool.release(newLeft);
    pool.release(newRight);
    return res;
  }
  if (height(b) > height(a) + 1) {
    const heavy = b as Branch;
    if (height(heavy.right) >= height(heavy.left)) {
      // single left rotation: (a, (y, z)) -> ((a, y), z)
      const newLeft = mkNode(pool, a, heavy.left);
      const res = mkNode(pool, newLeft, heavy.right);
      pool.release(newLeft);
      return res;
    }
    // double rotation: (a, ((y1, y2), z)) -> ((a, y1), (y2, z))
    const y = heavy.left as Branch;
    const newLeft = mkNode(pool, a, y.left);
    const newRight = mkNode(pool, y.right, heavy.right);
    const res = mkNode(pool, newLeft, newRight);
    pool.release(newLeft);
    pool.release(newRight);
    return res;
  }
  return mkNode(pool, a, b);
}

/** Requires height(l) > height(r) + 1. */
function joinRight(pool: NodePool, l: Branch, r: Node): Node {
  const c = l.right;
  if (height(c) <= height(r) + 1) {
    const t = mkNode(pool, c, r);
    const res = balance(pool, l.left, t);
    pool.release(t);
    return res;
  }
  const t = joinRight(pool, c as Branch, r);
  const res = balance(pool, l.left, t);
  pool.release(t);
  return res;
}

/** Requires height(r) > height(l) + 1. */
function joinLeft(pool: NodePool, l: Node, r: Branch): Node {
  const c = r.left;
  if (height(c) <= height(l) + 1) {
    const t = mkNode(pool, l, c);
    const res = balance(pool, t, r.right);
    pool.release(t);
    return res;
  }
  const t = joinLeft(pool, l, c as Branch);
  const res = balance(pool, t, r.right);
  pool.release(t);
  return res;
}

/**
 * Concatenates two ropes, restoring AVL balance. Borrows both inputs and
 * returns a fresh reference; either input may be null (empty rope).
 */
export function join(pool: NodePool, l: Node | null, r: Node | null): Node | null {
  if (!l) {
    pool.retain(r);
    return r;
  }
  if (!r) {
    pool.retain(l);
    return l;
  }
  if (height(l) > height(r) + 1) return joinRight(pool, l as Branch, r);
  if (height(r) > height(l) + 1) return joinLeft(pool, l, r as Branch);
  return mkNode(pool, l, r);
}

/**
 * Splits a rope at scalar offset k (0 <= k <= n.scalars) into
 * [first-k-scalars, rest]. Both results are fresh references.
 */
export function split(pool: NodePool, n: Node, k: number): [Node | null, Node | null] {
  if (n.kind === NodeKind.Leaf) {
    if (k <= 0) {
      pool.retain(n);
      return [null, n];
    }
    if (k >= n.scalars) {
      pool.retain(n);
      return [n, null];
    }
    // Scalar-boundary split: a surrogate pair is never divided.
    const left = pool.leaf(sliceScalars(n.text, 0, k));
    const right = pool.leaf(sliceScalars(n.text, k, n.scalars));
    return [left, right];
  }
  const ls = n.left.scalars;
  if (k < ls) {
    const [a, b] = split(pool, n.left, k);
    const r = join(pool, b, n.right);
    pool.release(b);
    return [a, r];
  }
  if (k > ls) {
    const [a, b] = split(pool, n.right, k - ls);
    const l = join(pool, n.left, a);
    pool.release(a);
    return [l, b];
  }
  pool.retain(n.left);
  pool.retain(n.right);
  return [n.left, n.right];
}

/** split() variant that accepts an empty (null) rope. */
export function splitOpt(
  pool: NodePool,
  n: Node | null,
  k: number
): [Node | null, Node | null] {
  if (!n) return [null, null];
  return split(pool, n, k);
}

/** Builds a balanced rope from a string, chunking into <= LEAF_MAX leaves. */
export function build(pool: NodePool, text: string): Node | null {
  if (text.length === 0) return null;
  const total = countScalars(text);
  let level: Node[] = [];
  for (let start = 0; start < total; start += LEAF_MAX) {
    level.push(pool.leaf(sliceScalars(text, start, Math.min(start + LEAF_MAX, total))));
  }
  while (level.length > 1) {
    const next: Node[] = [];
    for (let i = 0; i < level.length; i += 2) {
      if (i + 1 < level.length) {
        const merged = mkNode(pool, level[i], level[i + 1]);
        pool.release(level[i]);
        pool.release(level[i + 1]);
        next.push(merged);
      } else {
        next.push(level[i]);
      }
    }
    level = next;
  }
  return level[0];
}

/** In-order traversal, appending leaf texts to `out`. */
export function collect(n: Node | null, out: string[]): void {
  if (!n) return;
  if (n.kind === NodeKind.Leaf) {
    out.push(n.text);
    return;
  }
  collect(n.left, out);
  collect(n.right, out);
}

/** Number of distinct nodes reachable from the given roots. */
export function reachableCount(roots: Array<Node | null>): number {
  const seen = new Set<Node>();
  const stack: Node[] = [];
  for (const r of roots) {
    if (r) stack.push(r);
  }
  while (stack.length > 0) {
    const n = stack.pop() as Node;
    if (seen.has(n)) continue;
    seen.add(n);
    if (n.kind === NodeKind.Branch) {
      stack.push(n.left);
      stack.push(n.right);
    }
  }
  return seen.size;
}
