/**
 * Persistent (fully versioned, branchable) ordered multiset.
 *
 * - Every mutating operation (`insert` / `remove`) takes a version and returns
 *   a NEW version; the source version is never modified, so any old version
 *   can be used as the base of further branches at any time.
 * - Internally this is an AVL tree with path copying: nodes are immutable and
 *   shared freely between versions, so old versions never observe changes.
 * - Memory is reclaimed by reference counting. `release(v)` drops the version
 *   root; a node is recycled only when no version and no other node can reach
 *   it. Shared nodes keep old versions intact.
 *
 * Ranks and select:
 * - `rank(v, key)`  = number of elements strictly smaller than `key`.
 * - `select(v, k)`  = k-th smallest element, 0-based (duplicates counted).
 */

export type VersionId = number;

/** Thrown when a version id is unknown or has already been released. */
export class UnknownVersionError extends Error {
  constructor(public readonly version: VersionId) {
    super(`unknown or released version: ${version}`);
    this.name = "UnknownVersionError";
  }
}

/** Thrown when removing a key that is not present in the version. */
export class KeyNotFoundError extends Error {
  constructor(public readonly key: number) {
    super(`key not present in this version: ${key}`);
    this.name = "KeyNotFoundError";
  }
}

/** Thrown by `select` on an empty multiset. */
export class EmptyMultisetError extends Error {
  constructor() {
    super("select on an empty multiset");
    this.name = "EmptyMultisetError";
  }
}

interface Node {
  readonly key: number;
  readonly count: number; // multiplicity of `key` at this node (>= 1)
  readonly size: number; // total elements in this subtree, duplicates counted
  readonly height: number;
  readonly left: Node | null;
  readonly right: Node | null;
  /** GC bookkeeping: number of incoming references (parents + version roots). */
  ref: number;
}

function sizeOf(node: Node | null): number {
  return node === null ? 0 : node.size;
}

function heightOf(node: Node | null): number {
  return node === null ? 0 : node.height;
}

export class PersistentMultiset {
  private readonly versions = new Map<VersionId, Node | null>();
  private nextVersion: VersionId = 0;
  private liveNodes = 0;

  // ---------------------------------------------------------------- versions

  /** Creates a new empty version. */
  create(): VersionId {
    return this.registerVersion(null);
  }

  /** Creates an independent branch of `version` (O(1); structure is shared). */
  fork(version: VersionId): VersionId {
    return this.registerVersion(this.getRoot(version));
  }

  /**
   * Releases a version. Nodes that become unreachable from every remaining
   * version are reclaimed; nodes still shared with live versions are kept.
   */
  release(version: VersionId): void {
    const root = this.getRoot(version);
    this.versions.delete(version);
    this.unlink(root);
  }

  hasVersion(version: VersionId): boolean {
    return this.versions.has(version);
  }

  versionCount(): number {
    return this.versions.size;
  }

  // ------------------------------------------------------------- mutations

  /** Returns a new version with one extra copy of `key`. */
  insert(version: VersionId, key: number): VersionId {
    return this.registerVersion(this.insertRec(this.getRoot(version), key));
  }

  /**
   * Returns a new version with one copy of `key` removed.
   * Throws KeyNotFoundError if `key` is not present in `version`.
   */
  remove(version: VersionId, key: number): VersionId {
    const root = this.getRoot(version);
    if (root === null) throw new KeyNotFoundError(key);
    return this.registerVersion(this.removeRec(root, key));
  }

  // -------------------------------------------------------------- queries

  /** Number of elements in the version, duplicates counted. */
  size(version: VersionId): number {
    return sizeOf(this.getRoot(version));
  }

  isEmpty(version: VersionId): boolean {
    return this.size(version) === 0;
  }

  /** Multiplicity of `key` in the version (0 if absent). */
  count(version: VersionId, key: number): number {
    let node = this.getRoot(version);
    while (node !== null) {
      if (key < node.key) node = node.left;
      else if (key > node.key) node = node.right;
      else return node.count;
    }
    return 0;
  }

  /** Number of elements strictly smaller than `key`. */
  rank(version: VersionId, key: number): number {
    let node = this.getRoot(version);
    let result = 0;
    while (node !== null) {
      if (key <= node.key) {
        node = node.left;
      } else {
        result += sizeOf(node.left) + node.count;
        node = node.right;
      }
    }
    return result;
  }

  /**
   * k-th smallest element, 0-based, duplicates counted.
   * Throws EmptyMultisetError on an empty version and RangeError when
   * `k` is not an integer in [0, size).
   */
  select(version: VersionId, k: number): number {
    const root = this.getRoot(version);
    const total = sizeOf(root);
    if (total === 0) throw new EmptyMultisetError();
    if (!Number.isInteger(k) || k < 0 || k >= total) {
      throw new RangeError(`select index ${k} out of range [0, ${total})`);
    }
    let node = root;
    let index = k;
    while (node !== null) {
      const leftSize = sizeOf(node.left);
      if (index < leftSize) {
        node = node.left;
      } else if (index < leftSize + node.count) {
        return node.key;
      } else {
        index -= leftSize + node.count;
        node = node.right;
      }
    }
    throw new Error("unreachable: corrupted tree");
  }

  /** All elements in ascending order, duplicates repeated. */
  toArray(version: VersionId): number[] {
    const out: number[] = [];
    const stack: Node[] = [];
    let node = this.getRoot(version);
    while (node !== null || stack.length > 0) {
      while (node !== null) {
        stack.push(node);
        node = node.left;
      }
      node = stack.pop()!;
      for (let i = 0; i < node.count; i++) out.push(node.key);
      node = node.right;
    }
    return out;
  }

  // ----------------------------------------------------------- diagnostics

  /** Number of tree nodes currently kept alive by all versions together. */
  liveNodeCount(): number {
    return this.liveNodes;
  }

  /**
   * Number of nodes reachable from all live version roots. Always equals
   * liveNodeCount(); exposed so tests can verify that release() reclaims
   * exactly the unreachable nodes. O(reachable nodes).
   */
  reachableNodeCount(): number {
    const seen = new Set<Node>();
    const stack: (Node | null)[] = [...this.versions.values()];
    let reached = 0;
    while (stack.length > 0) {
      const node = stack.pop()!;
      if (node === null || seen.has(node)) continue;
      seen.add(node);
      reached++;
      stack.push(node.left, node.right);
    }
    return reached;
  }

  /**
   * Diagnostic: verifies AVL balance, cached size/height fields and key
   * ordering for a version. Throws on any inconsistency.
   */
  validate(version: VersionId): void {
    const check = (node: Node | null, lo: number, hi: number): [number, number] => {
      if (node === null) return [0, 0];
      if (!(node.key > lo && node.key < hi)) {
        throw new Error(`ordering violated at key ${node.key}`);
      }
      if (node.count < 1) throw new Error(`bad count at key ${node.key}`);
      const [ls, lh] = check(node.left, lo, node.key);
      const [rs, rh] = check(node.right, node.key, hi);
      if (node.size !== ls + rs + node.count) {
        throw new Error(`bad size at key ${node.key}`);
      }
      if (node.height !== Math.max(lh, rh) + 1) {
        throw new Error(`bad height at key ${node.key}`);
      }
      if (Math.abs(lh - rh) > 1) throw new Error(`unbalanced at key ${node.key}`);
      return [node.size, node.height];
    };
    check(this.getRoot(version), -Infinity, Infinity);
  }

  // ------------------------------------------------------------- internals

  private getRoot(version: VersionId): Node | null {
    const root = this.versions.get(version);
    if (root === undefined && !this.versions.has(version)) {
      throw new UnknownVersionError(version);
    }
    return root ?? null;
  }

  private registerVersion(root: Node | null): VersionId {
    this.link(root);
    const id = this.nextVersion++;
    this.versions.set(id, root);
    return id;
  }

  private link(node: Node | null): void {
    if (node !== null) node.ref++;
  }

  private unlink(node: Node | null): void {
    if (node === null) return;
    node.ref--;
    if (node.ref === 0) {
      this.liveNodes--;
      this.unlink(node.left);
      this.unlink(node.right);
    }
  }

  /**
   * Reclaims a node that was constructed during the current operation but
   * ended up unused (a rotation took it apart instead of linking it).
   * Such a node has ref === 0; shared nodes (ref > 0) are left untouched.
   * Children that were only referenced by this node cascade-free.
   */
  private unmakeIfFresh(node: Node): void {
    if (node.ref !== 0) return;
    this.liveNodes--;
    this.unlink(node.left);
    this.unlink(node.right);
  }

  private makeNode(
    key: number,
    count: number,
    left: Node | null,
    right: Node | null
  ): Node {
    const node: Node = {
      key,
      count,
      left,
      right,
      size: count + sizeOf(left) + sizeOf(right),
      height: 1 + Math.max(heightOf(left), heightOf(right)),
      ref: 0,
    };
    this.link(left);
    this.link(right);
    this.liveNodes++;
    return node;
  }

  /** AVL rebalancing; only creates nodes that end up in the new tree. */
  private balance(
    key: number,
    count: number,
    left: Node | null,
    right: Node | null
  ): Node {
    const lh = heightOf(left);
    const rh = heightOf(right);
    if (lh > rh + 1) {
      const l = left!;
      if (heightOf(l.left) >= heightOf(l.right)) {
        const newRight = this.makeNode(key, count, l.right, right);
        const result = this.makeNode(l.key, l.count, l.left, newRight);
        this.unmakeIfFresh(l);
        return result;
      }
      const lr = l.right!;
      const newLeft = this.makeNode(l.key, l.count, l.left, lr.left);
      const newRight = this.makeNode(key, count, lr.right, right);
      const result = this.makeNode(lr.key, lr.count, newLeft, newRight);
      this.unmakeIfFresh(l); // cascades into lr if lr was also freshly built
      return result;
    }
    if (rh > lh + 1) {
      const r = right!;
      if (heightOf(r.right) >= heightOf(r.left)) {
        const newLeft = this.makeNode(key, count, left, r.left);
        const result = this.makeNode(r.key, r.count, newLeft, r.right);
        this.unmakeIfFresh(r);
        return result;
      }
      const rl = r.left!;
      const newRight = this.makeNode(r.key, r.count, rl.right, r.right);
      const newLeft = this.makeNode(key, count, left, rl.left);
      const result = this.makeNode(rl.key, rl.count, newLeft, newRight);
      this.unmakeIfFresh(r); // cascades into rl if rl was also freshly built
      return result;
    }
    return this.makeNode(key, count, left, right);
  }

  private insertRec(node: Node | null, key: number): Node {
    if (node === null) return this.makeNode(key, 1, null, null);
    if (key < node.key) {
      return this.balance(node.key, node.count, this.insertRec(node.left, key), node.right);
    }
    if (key > node.key) {
      return this.balance(node.key, node.count, node.left, this.insertRec(node.right, key));
    }
    return this.makeNode(node.key, node.count + 1, node.left, node.right);
  }

  private removeRec(node: Node, key: number): Node | null {
    if (key < node.key) {
      if (node.left === null) throw new KeyNotFoundError(key);
      return this.balance(node.key, node.count, this.removeRec(node.left, key), node.right);
    }
    if (key > node.key) {
      if (node.right === null) throw new KeyNotFoundError(key);
      return this.balance(node.key, node.count, node.left, this.removeRec(node.right, key));
    }
    if (node.count > 1) {
      return this.makeNode(node.key, node.count - 1, node.left, node.right);
    }
    if (node.left === null) return node.right;
    if (node.right === null) return node.left;
    const successor = this.minNode(node.right);
    const newRight = this.removeMinRec(node.right);
    return this.balance(successor.key, successor.count, node.left, newRight);
  }

  private minNode(node: Node): Node {
    let current = node;
    while (current.left !== null) current = current.left;
    return current;
  }

  private removeMinRec(node: Node): Node | null {
    if (node.left === null) return node.right;
    return this.balance(node.key, node.count, this.removeMinRec(node.left), node.right);
  }
}
