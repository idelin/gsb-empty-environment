import type { AABB } from './aabb.ts';
import { cloneAABB, overlaps, perimeter, unionInto, unionOf } from './aabb.ts';

const NULL_NODE = -1;

interface TreeNode<T> {
  /** Fat AABB for leaves, union of children for internal nodes. */
  aabb: AABB;
  userData: T | null;
  /** parent index, or next free-list index for unused nodes. */
  parentOrNext: number;
  child1: number;
  child2: number;
  height: number;
}

/**
 * Incremental dynamic AABB tree (Box2D `b2DynamicTree` style), in 3D.
 *
 * - Insertion picks the sibling that minimizes the surface-area heuristic.
 * - Removals and insertions rebalance the tree via rotations.
 * - Leaf AABBs are expected to be "fat" (slightly larger than the tight
 *   bounds) so small movements do not trigger reinsertion.
 */
export class DynamicTree<T> {
  private nodes: Array<TreeNode<T>> = [];
  private root: number = NULL_NODE;
  private freeList: number = NULL_NODE;
  private nodeCount = 0;
  private capacity = 0;

  get size(): number {
    return this.nodeCount;
  }

  /** Create a proxy (leaf) holding a fat AABB. Returns the proxy id. */
  createProxy(aabb: AABB, userData: T): number {
    const proxyId = this.allocateNode();
    const node = this.nodes[proxyId];
    node.aabb = cloneAABB(aabb);
    node.userData = userData;
    node.height = 0;
    node.child1 = NULL_NODE;
    node.child2 = NULL_NODE;
    this.insertLeaf(proxyId);
    return proxyId;
  }

  destroyProxy(proxyId: number): void {
    this.removeLeaf(proxyId);
    this.freeNode(proxyId);
  }

  /** Replace the fat AABB of a proxy (reinserts the leaf). */
  updateProxy(proxyId: number, aabb: AABB): void {
    this.removeLeaf(proxyId);
    const node = this.nodes[proxyId];
    node.aabb = cloneAABB(aabb);
    this.insertLeaf(proxyId);
  }

  getUserData(proxyId: number): T | null {
    return this.nodes[proxyId].userData;
  }

  getFatAABB(proxyId: number): AABB {
    return this.nodes[proxyId].aabb;
  }

  /** Visit every leaf whose fat AABB overlaps `aabb`. Return false from the callback to stop. */
  query(aabb: AABB, callback: (proxyId: number) => boolean | void): void {
    const stack: number[] = [this.root];
    while (stack.length > 0) {
      const nodeId = stack.pop() as number;
      if (nodeId === NULL_NODE) continue;
      const node = this.nodes[nodeId];
      if (!overlaps(node.aabb, aabb)) continue;
      if (node.child1 === NULL_NODE) {
        if (callback(nodeId) === false) return;
      } else {
        stack.push(node.child1, node.child2);
      }
    }
  }


  /**
   * Enumerate every pair of leaf proxies whose fat AABBs overlap, via
   * top-down self-collision traversal. Each pair is reported exactly once.
   * Cost is proportional to the number of overlapping node pairs — disjoint
   * subtrees are pruned at internal nodes without descending.
   */
  forEachOverlappingPair(callback: (proxyA: number, proxyB: number) => void): void {
    if (this.root === NULL_NODE) return;

    const pairStackA: number[] = [];
    const pairStackB: number[] = [];

    // Seed the pair stack with (child1, child2) of every internal node.
    // Any leaf pair's LCA is some internal node, so this covers all pairs
    // exactly once.
    const walk: number[] = [this.root];
    while (walk.length > 0) {
      const id = walk.pop() as number;
      const node = this.nodes[id];
      if (node.child1 === NULL_NODE) continue;
      pairStackA.push(node.child1);
      pairStackB.push(node.child2);
      walk.push(node.child1, node.child2);
    }

    // Expand pairs: descend into the larger side until both are leaves.
    while (pairStackA.length > 0) {
      const idA = pairStackA.pop() as number;
      const idB = pairStackB.pop() as number;
      const a = this.nodes[idA];
      const b = this.nodes[idB];
      const aa = a.aabb;
      const bb = b.aabb;
      if (
        aa.minX > bb.maxX || aa.maxX < bb.minX ||
        aa.minY > bb.maxY || aa.maxY < bb.minY ||
        aa.minZ > bb.maxZ || aa.maxZ < bb.minZ
      ) {
        continue;
      }
      const aLeaf = a.child1 === NULL_NODE;
      const bLeaf = b.child1 === NULL_NODE;
      if (aLeaf && bLeaf) {
        callback(idA, idB);
      } else if (bLeaf || (!aLeaf && a.height > b.height)) {
        pairStackA.push(a.child1, a.child2);
        pairStackB.push(idB, idB);
      } else {
        pairStackA.push(idA, idA);
        pairStackB.push(b.child1, b.child2);
      }
    }
  }

  /** Current tree height (0 for empty/single-leaf trees). */
  getHeight(): number {
    return this.root === NULL_NODE ? 0 : this.nodes[this.root].height;
  }

  /** Total perimeter of all node AABBs — a quality metric (lower is better). */
  getAreaRatio(): number {
    if (this.root === NULL_NODE) return 1;
    const root = this.nodes[this.root];
    const rootPerimeter = perimeter(root.aabb);
    let total = 0;
    for (const node of this.nodes) {
      if (node.height < 0) continue; // free node
      total += perimeter(node.aabb);
    }
    return total / rootPerimeter;
  }

  private allocateNode(): number {
    if (this.freeList !== NULL_NODE) {
      const nodeId = this.freeList;
      const node = this.nodes[nodeId];
      this.freeList = node.parentOrNext;
      node.parentOrNext = NULL_NODE;
      node.height = 0;
      node.userData = null;
      this.nodeCount++;
      return nodeId;
    }
    const nodeId = this.capacity;
    this.capacity++;
    this.nodes.push({
      aabb: { minX: 0, minY: 0, minZ: 0, maxX: 0, maxY: 0, maxZ: 0 },
      userData: null,
      parentOrNext: NULL_NODE,
      child1: NULL_NODE,
      child2: NULL_NODE,
      height: 0,
    });
    this.nodeCount++;
    return nodeId;
  }

  private freeNode(nodeId: number): void {
    const node = this.nodes[nodeId];
    node.parentOrNext = this.freeList;
    node.height = -1; // marks "free"
    node.userData = null;
    this.freeList = nodeId;
    this.nodeCount--;
  }

  private insertLeaf(leaf: number): void {
    if (this.root === NULL_NODE) {
      this.root = leaf;
      this.nodes[leaf].parentOrNext = NULL_NODE;
      return;
    }

    const leafAABB = this.nodes[leaf].aabb;

    // Find the best sibling using the surface-area heuristic.
    let index = this.root;
    while (this.nodes[index].child1 !== NULL_NODE) {
      const node = this.nodes[index];
      const child1 = node.child1;
      const child2 = node.child2;

      const area = perimeter(node.aabb);
      const combined = unionOf(node.aabb, leafAABB);
      const combinedArea = perimeter(combined);

      // Cost of creating a new parent for this node and the leaf.
      const cost = 2 * combinedArea;
      // Cost of descending further.
      const inheritanceCost = 2 * (combinedArea - area);

      const cost1 = this.descendCost(this.nodes[child1], leafAABB, inheritanceCost);
      const cost2 = this.descendCost(this.nodes[child2], leafAABB, inheritanceCost);

      if (cost < cost1 && cost < cost2) break;
      index = cost1 < cost2 ? child1 : child2;
    }

    const sibling = index;
    const oldParent = this.nodes[sibling].parentOrNext;
    const newParent = this.allocateNode();
    const newParentNode = this.nodes[newParent];
    newParentNode.parentOrNext = oldParent;
    newParentNode.userData = null;
    newParentNode.aabb = unionOf(leafAABB, this.nodes[sibling].aabb);
    newParentNode.height = this.nodes[sibling].height + 1;

    if (oldParent !== NULL_NODE) {
      const oldParentNode = this.nodes[oldParent];
      if (oldParentNode.child1 === sibling) oldParentNode.child1 = newParent;
      else oldParentNode.child2 = newParent;
    } else {
      this.root = newParent;
    }
    newParentNode.child1 = sibling;
    newParentNode.child2 = leaf;
    this.nodes[sibling].parentOrNext = newParent;
    this.nodes[leaf].parentOrNext = newParent;

    // Walk back up, refitting AABBs and rebalancing.
    let walk = this.nodes[leaf].parentOrNext;
    while (walk !== NULL_NODE) {
      walk = this.balance(walk);
      const node = this.nodes[walk];
      const child1 = this.nodes[node.child1];
      const child2 = this.nodes[node.child2];
      node.height = 1 + Math.max(child1.height, child2.height);
      unionInto(cloneInto(node.aabb, child1.aabb), child2.aabb);
      walk = node.parentOrNext;
    }
  }

  private descendCost(node: TreeNode<T>, leafAABB: AABB, inheritanceCost: number): number {
    if (node.child1 === NULL_NODE) {
      return perimeter(unionOf(node.aabb, leafAABB)) + inheritanceCost;
    }
    const oldArea = perimeter(node.aabb);
    const newArea = perimeter(unionOf(node.aabb, leafAABB));
    return newArea - oldArea + inheritanceCost;
  }

  private removeLeaf(leaf: number): void {
    if (leaf === this.root) {
      this.root = NULL_NODE;
      return;
    }
    const parent = this.nodes[leaf].parentOrNext;
    const parentNode = this.nodes[parent];
    const grandParent = parentNode.parentOrNext;
    const sibling = parentNode.child1 === leaf ? parentNode.child2 : parentNode.child1;

    if (grandParent !== NULL_NODE) {
      const grandParentNode = this.nodes[grandParent];
      if (grandParentNode.child1 === parent) grandParentNode.child1 = sibling;
      else grandParentNode.child2 = sibling;
      this.nodes[sibling].parentOrNext = grandParent;
      this.freeNode(parent);

      let walk = grandParent;
      while (walk !== NULL_NODE) {
        walk = this.balance(walk);
        const node = this.nodes[walk];
        const child1 = this.nodes[node.child1];
        const child2 = this.nodes[node.child2];
        unionInto(cloneInto(node.aabb, child1.aabb), child2.aabb);
        node.height = 1 + Math.max(child1.height, child2.height);
        walk = node.parentOrNext;
      }
    } else {
      this.root = sibling;
      this.nodes[sibling].parentOrNext = NULL_NODE;
      this.freeNode(parent);
    }
  }

  /** AVL-style rotations to keep the tree balanced. */
  private balance(iA: number): number {
    const a = this.nodes[iA];
    if (a.child1 === NULL_NODE || a.height < 2) return iA;

    const iB = a.child1;
    const iC = a.child2;
    const b = this.nodes[iB];
    const c = this.nodes[iC];
    const balanceFactor = c.height - b.height;

    if (balanceFactor > 1) {
      // Rotate C up.
      const iF = c.child1;
      const iG = c.child2;
      const f = this.nodes[iF];
      const g = this.nodes[iG];

      c.child1 = iA;
      c.parentOrNext = a.parentOrNext;
      a.parentOrNext = iC;
      if (c.parentOrNext !== NULL_NODE) {
        const p = this.nodes[c.parentOrNext];
        if (p.child1 === iA) p.child1 = iC;
        else p.child2 = iC;
      } else {
        this.root = iC;
      }

      if (f.height > g.height) {
        c.child2 = iF;
        a.child2 = iG;
        g.parentOrNext = iA;
        unionInto(cloneInto(a.aabb, b.aabb), g.aabb);
        unionInto(cloneInto(c.aabb, a.aabb), f.aabb);
        a.height = 1 + Math.max(b.height, g.height);
        c.height = 1 + Math.max(a.height, f.height);
      } else {
        c.child2 = iG;
        a.child2 = iF;
        f.parentOrNext = iA;
        unionInto(cloneInto(a.aabb, b.aabb), f.aabb);
        unionInto(cloneInto(c.aabb, a.aabb), g.aabb);
        a.height = 1 + Math.max(b.height, f.height);
        c.height = 1 + Math.max(a.height, g.height);
      }
      return iC;
    }

    if (balanceFactor < -1) {
      // Rotate B up.
      const iD = b.child1;
      const iE = b.child2;
      const d = this.nodes[iD];
      const e = this.nodes[iE];

      b.child1 = iA;
      b.parentOrNext = a.parentOrNext;
      a.parentOrNext = iB;
      if (b.parentOrNext !== NULL_NODE) {
        const p = this.nodes[b.parentOrNext];
        if (p.child1 === iA) p.child1 = iB;
        else p.child2 = iB;
      } else {
        this.root = iB;
      }

      if (d.height > e.height) {
        b.child2 = iD;
        a.child1 = iE;
        e.parentOrNext = iA;
        unionInto(cloneInto(a.aabb, c.aabb), e.aabb);
        unionInto(cloneInto(b.aabb, a.aabb), d.aabb);
        a.height = 1 + Math.max(c.height, e.height);
        b.height = 1 + Math.max(a.height, d.height);
      } else {
        b.child2 = iE;
        a.child1 = iD;
        d.parentOrNext = iA;
        unionInto(cloneInto(a.aabb, c.aabb), d.aabb);
        unionInto(cloneInto(b.aabb, a.aabb), e.aabb);
        a.height = 1 + Math.max(c.height, d.height);
        b.height = 1 + Math.max(a.height, e.height);
      }
      return iB;
    }

    return iA;
  }
}

function cloneInto(target: AABB, source: AABB): AABB {
  target.minX = source.minX;
  target.minY = source.minY;
  target.minZ = source.minZ;
  target.maxX = source.maxX;
  target.maxY = source.maxY;
  target.maxZ = source.maxZ;
  return target;
}
