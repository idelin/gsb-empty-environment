/**
 * Persistent rope over UTF-8 text.
 *
 * Every node maintains BOTH its UTF-8 byte length and its Unicode scalar
 * (code point) count. All edit offsets are scalar offsets, so an edit can
 * never split a multi-byte encoding.
 *
 * The structure is fully persistent: insert/delete return a new Rope that
 * shares every untouched node with the previous one. Nodes are reference
 * counted through RopeArena; when the last snapshot referencing a node is
 * released the node is reclaimed into the arena's free pool for reuse.
 */

import {
  assertWellFormedUtf8,
  countScalars,
  scalarOffsetToUtf16Index,
  utf8ByteLength,
} from "./utf8.js";

export const LEAF_MAX_SCALARS = 32;

export class Leaf {
  text: string = "";
  scalars: number = 0;
  bytes: number = 0;
  height: number = 0;
  refs: number = 0;
}

export class Branch {
  left: RopeNode;
  right: RopeNode;
  scalars: number;
  bytes: number;
  height: number;
  refs: number = 0;

  constructor(left: RopeNode, right: RopeNode) {
    this.left = left;
    this.right = right;
    this.scalars = left.scalars + right.scalars;
    this.bytes = left.bytes + right.bytes;
    this.height = 1 + Math.max(left.height, right.height);
  }
}

export type RopeNode = Leaf | Branch;
export type RopeNodePair = [RopeNode | null, RopeNode | null];

/**
 * Owns all rope nodes and tracks reclamation. Nodes whose reference count
 * drops to zero are recycled into free lists and reused by later edits.
 */
export class RopeArena {
  allocated: number = 0;
  reclaimed: number = 0;
  private leafPool: Leaf[] = [];
  private branchPool: Branch[] = [];

  get live(): number {
    return this.allocated - this.reclaimed;
  }

  makeLeaf(text: string): Leaf {
    let node = this.leafPool.pop();
    if (node === undefined) {
      node = new Leaf();
      this.allocated++;
    } else {
      this.reclaimed--;
    }
    node.text = text;
    node.scalars = countScalars(text);
    node.bytes = utf8ByteLength(text);
    node.height = 0;
    node.refs = 1;
    return node;
  }

  makeBranch(left: RopeNode, right: RopeNode): Branch {
    let node = this.branchPool.pop();
    if (node === undefined) {
      node = new Branch(left, right);
      this.allocated++;
    } else {
      this.reclaimed--;
      node.left = left;
      node.right = right;
      node.scalars = left.scalars + right.scalars;
      node.bytes = left.bytes + right.bytes;
      node.height = 1 + Math.max(left.height, right.height);
    }
    node.refs = 1;
    this.retain(left);
    this.retain(right);
    return node;
  }

  retain(node: RopeNode | null): void {
    if (node !== null) node.refs++;
  }

  release(node: RopeNode | null): void {
    if (node === null) return;
    node.refs--;
    if (node.refs > 0) return;
    if (node instanceof Branch) {
      this.release(node.left);
      this.release(node.right);
      this.branchPool.push(node);
    } else if (node instanceof Leaf) {
      this.leafPool.push(node);
    }
    this.reclaimed++;
  }
}

function buildBalanced(arena: RopeArena, text: string): RopeNode | null {
  const total = countScalars(text);
  if (total === 0) return null;
  const leaves: RopeNode[] = [];
  let utf16 = 0;
  let done = 0;
  while (done < total) {
    const next = scalarOffsetToUtf16Index(text, done + LEAF_MAX_SCALARS);
    leaves.push(arena.makeLeaf(text.slice(utf16, next)));
    utf16 = next;
    done += LEAF_MAX_SCALARS;
  }
  return buildTree(arena, leaves, 0, leaves.length);
}

/** Builds a height-balanced tree over leaves[lo, hi); sizes differ by at most one. */
function buildTree(
  arena: RopeArena,
  leaves: RopeNode[],
  lo: number,
  hi: number
): RopeNode {
  if (hi - lo === 1) return leaves[lo];
  const mid = lo + ((hi - lo) >> 1);
  const left = buildTree(arena, leaves, lo, mid);
  const right = buildTree(arena, leaves, mid, hi);
  const branch = arena.makeBranch(left, right);
  arena.release(left);
  arena.release(right);
  return branch;
}

/** Rotates if needed, then links left/right. Retains its arguments. */
function balanceBranch(
  arena: RopeArena,
  left: RopeNode | null,
  right: RopeNode | null
): RopeNode | null {
  if (left === null) {
    arena.retain(right);
    return right;
  }
  if (right === null) {
    arena.retain(left);
    return left;
  }
  if (left.height - right.height >= 2 && left instanceof Branch) {
    const ll = left.left;
    const lr = left.right;
    if (lr instanceof Branch && ll.height < lr.height) {
      const newLeft = arena.makeBranch(ll, lr.left);
      const newRight = arena.makeBranch(lr.right, right);
      const res = arena.makeBranch(newLeft, newRight);
      arena.release(newLeft);
      arena.release(newRight);
      return res;
    }
    const newRight = arena.makeBranch(lr, right);
    const res = arena.makeBranch(ll, newRight);
    arena.release(newRight);
    return res;
  }
  if (right.height - left.height >= 2 && right instanceof Branch) {
    const rl = right.left;
    const rr = right.right;
    if (rl instanceof Branch && rl.height > rr.height) {
      const newLeft = arena.makeBranch(left, rl.left);
      const newRight = arena.makeBranch(rl.right, rr);
      const res = arena.makeBranch(newLeft, newRight);
      arena.release(newLeft);
      arena.release(newRight);
      return res;
    }
    const newLeft = arena.makeBranch(left, rl);
    const res = arena.makeBranch(newLeft, rr);
    arena.release(newLeft);
    return res;
  }
  return arena.makeBranch(left, right);
}

/** Concatenates two ropes. Returns a new reference owned by the caller. */
function concat(
  arena: RopeArena,
  a: RopeNode | null,
  b: RopeNode | null
): RopeNode | null {
  if (a === null) {
    arena.retain(b);
    return b;
  }
  if (b === null) {
    arena.retain(a);
    return a;
  }
  if (a instanceof Leaf && b instanceof Leaf && a.scalars + b.scalars <= LEAF_MAX_SCALARS) {
    return arena.makeLeaf(a.text + b.text);
  }
  if (a.height > b.height + 1 && a instanceof Branch) {
    const merged = concat(arena, a.right, b);
    const res = balanceBranch(arena, a.left, merged);
    arena.release(merged);
    return res;
  }
  if (b.height > a.height + 1 && b instanceof Branch) {
    const merged = concat(arena, a, b.left);
    const res = balanceBranch(arena, merged, b.right);
    arena.release(merged);
    return res;
  }
  return arena.makeBranch(a, b);
}

/** Splits at a scalar offset. Both results are new references owned by the caller. */
function split(arena: RopeArena, node: RopeNode | null, offset: number): RopeNodePair {
  if (node === null) return [null, null];
  if (offset <= 0) {
    arena.retain(node);
    return [null, node];
  }
  if (offset >= node.scalars) {
    arena.retain(node);
    return [node, null];
  }
  if (node instanceof Leaf) {
    const idx = scalarOffsetToUtf16Index(node.text, offset);
    const leftText = node.text.slice(0, idx);
    const rightText = node.text.slice(idx);
    const left = leftText.length > 0 ? arena.makeLeaf(leftText) : null;
    const right = rightText.length > 0 ? arena.makeLeaf(rightText) : null;
    return [left, right];
  }
  if (offset < node.left.scalars) {
    const pair = split(arena, node.left, offset);
    const right = concat(arena, pair[1], node.right);
    arena.release(pair[1]);
    return [pair[0], right];
  }
  const pair = split(arena, node.right, offset - node.left.scalars);
  const left = concat(arena, node.left, pair[0]);
  arena.release(pair[0]);
  return [left, pair[1]];
}

function insertAt(
  arena: RopeArena,
  root: RopeNode | null,
  offset: number,
  text: string
): RopeNode | null {
  const mid = buildBalanced(arena, text);
  if (mid === null) {
    arena.retain(root);
    return root;
  }
  if (root === null) return mid;
  const pair = split(arena, root, offset);
  const lm = concat(arena, pair[0], mid);
  const res = concat(arena, lm, pair[1]);
  arena.release(pair[0]);
  arena.release(pair[1]);
  arena.release(mid);
  arena.release(lm);
  return res;
}

function deleteRange(
  arena: RopeArena,
  root: RopeNode | null,
  start: number,
  end: number
): RopeNode | null {
  if (root === null || start >= end) {
    arena.retain(root);
    return root;
  }
  const first = split(arena, root, start);
  const second = split(arena, first[1], end - start);
  const res = concat(arena, first[0], second[1]);
  arena.release(first[0]);
  arena.release(first[1]);
  arena.release(second[0]);
  arena.release(second[1]);
  return res;
}

function validateNode(node: RopeNode): void {
  if (node instanceof Leaf) {
    if (node.scalars !== countScalars(node.text)) {
      throw new Error("leaf scalar count mismatch");
    }
    if (node.bytes !== utf8ByteLength(node.text)) {
      throw new Error("leaf byte count mismatch");
    }
    if (node.height !== 0) {
      throw new Error("leaf height must be 0");
    }
    return;
  }
  validateNode(node.left);
  validateNode(node.right);
  if (node.scalars !== node.left.scalars + node.right.scalars) {
    throw new Error("branch scalar count mismatch");
  }
  if (node.bytes !== node.left.bytes + node.right.bytes) {
    throw new Error("branch byte count mismatch");
  }
  if (node.height !== 1 + Math.max(node.left.height, node.right.height)) {
    throw new Error("branch height mismatch");
  }
  const diff = node.left.height - node.right.height;
  if (diff > 1 || diff < -1) {
    throw new Error("rope balance invariant violated");
  }
}

/**
 * An immutable snapshot of text. Edits produce new Ropes; the receiver is
 * never modified. Call dispose() when the snapshot is no longer needed so
 * its unshared nodes can be reclaimed by the arena.
 */
export class Rope {
  private arena: RopeArena;
  private root: RopeNode | null;
  private disposed: boolean;

  private constructor(arena: RopeArena, root: RopeNode | null) {
    this.arena = arena;
    this.root = root;
    this.disposed = false;
  }

  static empty(arena: RopeArena): Rope {
    return new Rope(arena, null);
  }

  static from(arena: RopeArena, text: string): Rope {
    assertWellFormedUtf8(text);
    return new Rope(arena, buildBalanced(arena, text));
  }

  private assertUsable(): void {
    if (this.disposed) throw new Error("rope has been disposed");
  }

  /** Number of Unicode scalar values. */
  get length(): number {
    this.assertUsable();
    return this.root === null ? 0 : this.root.scalars;
  }

  /** Number of bytes when encoded as UTF-8. */
  get byteLength(): number {
    this.assertUsable();
    return this.root === null ? 0 : this.root.bytes;
  }

  get nodeCount(): number {
    this.assertUsable();
    let count = 0;
    const stack: RopeNode[] = [];
    if (this.root !== null) stack.push(this.root);
    while (stack.length > 0) {
      const node = stack.pop();
      if (node === undefined) break;
      count++;
      if (node instanceof Branch) {
        stack.push(node.left);
        stack.push(node.right);
      }
    }
    return count;
  }

  insert(offset: number, text: string): Rope {
    this.assertUsable();
    assertWellFormedUtf8(text);
    const len = this.length;
    if (!Number.isInteger(offset) || offset < 0 || offset > len) {
      throw new RangeError("insert offset " + offset + " out of range [0, " + len + "]");
    }
    return new Rope(this.arena, insertAt(this.arena, this.root, offset, text));
  }

  delete(start: number, end: number): Rope {
    this.assertUsable();
    const len = this.length;
    if (
      !Number.isInteger(start) ||
      !Number.isInteger(end) ||
      start < 0 ||
      end > len ||
      start > end
    ) {
      throw new RangeError(
        "delete range [" + start + ", " + end + ") out of bounds for length " + len
      );
    }
    return new Rope(this.arena, deleteRange(this.arena, this.root, start, end));
  }

  toString(): string {
    this.assertUsable();
    if (this.root === null) return "";
    const parts: string[] = [];
    const stack: RopeNode[] = [this.root];
    while (stack.length > 0) {
      const node = stack.pop();
      if (node === undefined) break;
      if (node instanceof Leaf) {
        parts.push(node.text);
      } else {
        stack.push(node.right);
        stack.push(node.left);
      }
    }
    return parts.join("");
  }

  /** Debug invariant check: recomputes every node's metrics and balance. */
  validate(): void {
    this.assertUsable();
    if (this.root !== null) validateNode(this.root);
  }

  dispose(): void {
    if (this.disposed) return;
    this.arena.release(this.root);
    this.root = null;
    this.disposed = true;
  }
}
