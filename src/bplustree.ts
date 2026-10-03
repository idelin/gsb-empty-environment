import { PageManager } from "./pageManager.ts";

/** Max keys per node (leaf records or internal separators). */
export const MAX_KEYS = 4;
/** Min keys for any non-root node: ceil(MAX_KEYS / 2). */
export const MIN_KEYS = 2;

class BNode {
  id: number;
  isLeaf: boolean;
  keys: number[] = [];
  children: BNode[] = [];
  parent: BNode | null = null;

  constructor(id: number, isLeaf: boolean) {
    this.id = id;
    this.isLeaf = isLeaf;
  }
}

function subtreeMin(node: BNode): number {
  let cur = node;
  while (!cur.isLeaf) cur = cur.children[0];
  return cur.keys[0];
}

/**
 * B+ tree over a paged storage layer.
 * Deletion performs: borrow-from-sibling first, physical merge when neither
 * sibling can lend, cascading merges upward, and root shrink. Every emptied
 * page is returned to the page manager's free list.
 */
export class BPlusTree {
  readonly pm = new PageManager<BNode>();
  root: BNode | null = null;

  /** Observability counters. */
  mergeCount = 0;
  borrowCount = 0;
  rootShrinkCount = 0;

  private newNode(isLeaf: boolean): BNode {
    const node = new BNode(-1, isLeaf);
    node.id = this.pm.allocate(node);
    return node;
  }

  /** Build a maximally packed tree from sorted-or-unsorted records. */
  static bulkLoad(records: number[]): BPlusTree {
    const tree = new BPlusTree();
    if (records.length === 0) {
      tree.root = tree.newNode(true);
      return tree;
    }
    const sorted = [...records].sort((a, b) => a - b);

    let level: BNode[] = [];
    for (let i = 0; i < sorted.length; i += MAX_KEYS) {
      const leaf = tree.newNode(true);
      leaf.keys = sorted.slice(i, i + MAX_KEYS);
      level.push(leaf);
    }

    while (level.length > 1) {
      const next: BNode[] = [];
      for (let i = 0; i < level.length; i += MAX_KEYS + 1) {
        const group = level.slice(i, i + MAX_KEYS + 1);
        const parent = tree.newNode(false);
        parent.children = group;
        for (const child of group) child.parent = parent;
        parent.keys = group.slice(1).map(subtreeMin);
        next.push(parent);
      }
      level = next;
    }
    tree.root = level[0];
    return tree;
  }

  private findLeaf(key: number): BNode {
    let node = this.root!;
    while (!node.isLeaf) {
      let i = 0;
      while (i < node.keys.length && key >= node.keys[i]) i++;
      node = node.children[i];
    }
    return node;
  }

  search(key: number): boolean {
    if (!this.root) return false;
    const leaf = this.findLeaf(key);
    return leaf.keys.includes(key);
  }

  /** In-order record sequence (leaf level, left to right). */
  leafKeys(): number[] {
    const out: number[] = [];
    const walk = (n: BNode): void => {
      if (n.isLeaf) out.push(...n.keys);
      else n.children.forEach(walk);
    };
    if (this.root) walk(this.root);
    return out;
  }

  /** Number of levels; a leaf-only root has height 1. */
  height(): number {
    let h = 0;
    let node = this.root;
    while (node) {
      h++;
      node = node.isLeaf ? null : node.children[0];
    }
    return h;
  }

  get allocatedPages(): number {
    return this.pm.allocatedCount;
  }

  get freePages(): number {
    return this.pm.freeCount;
  }

  delete(key: number): boolean {
    if (!this.root) return false;
    const leaf = this.findLeaf(key);
    const pos = leaf.keys.indexOf(key);
    if (pos < 0) return false;
    leaf.keys.splice(pos, 1);

    if (leaf === this.root) return true; // root may hold any number of keys

    if (leaf.keys.length >= MIN_KEYS) {
      // Smallest key of this subtree changed: refresh ancestor separators.
      if (pos === 0) this.fixSeparatorUp(leaf);
      return true;
    }
    this.rebalance(leaf);
    return true;
  }

  /** Propagate a node's new minimum key up to the separator that covers it. */
  private fixSeparatorUp(node: BNode): void {
    let child = node;
    let parent = node.parent;
    while (parent) {
      const idx = parent.children.indexOf(child);
      if (idx > 0) {
        parent.keys[idx - 1] = subtreeMin(child);
        return;
      }
      child = parent;
      parent = parent.parent;
    }
  }

  /** Restore min-fill for an underflowed node: borrow, else physically merge. */
  private rebalance(node: BNode): void {
    const parent = node.parent!;
    const idx = parent.children.indexOf(node);
    const left = idx > 0 ? parent.children[idx - 1] : null;
    const right = idx < parent.children.length - 1 ? parent.children[idx + 1] : null;

    if (left && left.keys.length > MIN_KEYS) {
      this.borrowFromLeft(node, left, parent, idx);
      return;
    }
    if (right && right.keys.length > MIN_KEYS) {
      this.borrowFromRight(node, right, parent, idx);
      // The deletion may have shrunk this node's minimum key; refresh
      // the ancestor separator that covers it.
      this.fixSeparatorUp(node);
      return;
    }

    // Neither sibling can lend: physical merge of two pages into one.
    if (left) {
      this.mergeNodes(parent, idx - 1); // merge `node` into left sibling
    } else {
      this.mergeNodes(parent, idx); // merge right sibling into `node`
      this.fixSeparatorUp(node);
    }

    // The parent lost one index entry; it may now underflow -> cascade up.
    if (parent === this.root) {
      if (!parent.isLeaf && parent.keys.length === 0) {
        const newRoot = parent.children[0];
        newRoot.parent = null;
        this.pm.free(parent.id); // old root page recycled
        this.root = newRoot;
        this.rootShrinkCount++;
      }
    } else if (parent.keys.length < MIN_KEYS) {
      this.rebalance(parent);
    }
  }

  private borrowFromLeft(node: BNode, left: BNode, parent: BNode, idx: number): void {
    this.borrowCount++;
    if (node.isLeaf) {
      node.keys.unshift(left.keys.pop()!);
      parent.keys[idx - 1] = node.keys[0];
    } else {
      node.keys.unshift(parent.keys[idx - 1]);
      const moved = left.children.pop()!;
      node.children.unshift(moved);
      moved.parent = node;
      parent.keys[idx - 1] = left.keys.pop()!;
    }
  }

  private borrowFromRight(node: BNode, right: BNode, parent: BNode, idx: number): void {
    this.borrowCount++;
    if (node.isLeaf) {
      node.keys.push(right.keys.shift()!);
      parent.keys[idx] = right.keys[0];
    } else {
      node.keys.push(parent.keys[idx]);
      const moved = right.children.shift()!;
      node.children.push(moved);
      moved.parent = node;
      parent.keys[idx] = right.keys.shift()!;
    }
  }

  /**
   * Physical merge: fold children[i+1] into children[i], pull the separator
   * key down (internal case), delete the index entry from the parent, and
   * return the emptied page to the free page list.
   */
  private mergeNodes(parent: BNode, i: number): void {
    this.mergeCount++;
    const target = parent.children[i];
    const drained = parent.children[i + 1];
    if (target.isLeaf) {
      target.keys.push(...drained.keys);
    } else {
      target.keys.push(parent.keys[i], ...drained.keys);
      target.children.push(...drained.children);
      for (const child of drained.children) child.parent = target;
    }
    parent.keys.splice(i, 1);
    parent.children.splice(i + 1, 1);
    this.pm.free(drained.id);
  }

  /** Throw on any structural invariant violation. */
  validate(): void {
    if (!this.root) return;
    const keys = this.leafKeys();
    for (let i = 1; i < keys.length; i++) {
      if (keys[i - 1] >= keys[i]) throw new Error("leaf keys not strictly increasing");
    }

    let leafDepth = -1;
    const check = (node: BNode, isRoot: boolean, depth: number): void => {
      if (node.keys.length > MAX_KEYS) throw new Error("node overfull");
      if (!isRoot && node.keys.length < MIN_KEYS) throw new Error("node underflow");
      if (isRoot && !node.isLeaf && node.keys.length < 1) throw new Error("root not shrunk");
      if (node.isLeaf) {
        if (leafDepth === -1) leafDepth = depth;
        else if (leafDepth !== depth) throw new Error("leaves at different depths");
        return;
      }
      if (node.children.length !== node.keys.length + 1) {
        throw new Error("children != keys + 1");
      }
      for (let i = 0; i < node.children.length; i++) {
        const child = node.children[i];
        if (child.parent !== node) throw new Error("broken parent pointer");
        if (i > 0 && node.keys[i - 1] !== subtreeMin(child)) {
          throw new Error("stale separator key");
        }
        check(child, false, depth + 1);
      }
    };
    check(this.root, true, 1);
  }
}
