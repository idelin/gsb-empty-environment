import { AABB } from "./aabb.ts";

/** An object stored in the tree: its tight bounds plus a user payload. */
export interface BVHItem<T> {
  aabb: AABB;
  data: T;
}

/** Result of a ray cast: the closest hit payload and its ray distance. */
export interface RayHit<T> {
  data: T;
  t: number;
}

/** Traversal counters, resettable per measurement window. */
export interface BVHStats {
  /** Internal+leaf nodes visited during traversal. */
  nodeVisits: number;
  /** Leaf objects actually intersection-tested. */
  leafTests: number;
}

type Node<T> = {
  aabb: AABB;
  parent: Node<T> | null;
  /** null on leaves */
  left: Node<T> | null;
  right: Node<T> | null;
  /** present only on leaves */
  item: BVHItem<T> | null;
  /** subtree height, leaves are 0 */
  height: number;
};

function makeLeaf<T>(item: BVHItem<T>): Node<T> {
  return { aabb: item.aabb.clone(), parent: null, left: null, right: null, item, height: 0 };
}

function makeInternal<T>(left: Node<T>, right: Node<T>): Node<T> {
  const node: Node<T> = {
    aabb: AABB.union(left.aabb, right.aabb),
    parent: null,
    left,
    right,
    item: null,
    height: Math.max(left.height, right.height) + 1,
  };
  left.parent = node;
  right.parent = node;
  return node;
}

const SAH_BIN_COUNT = 16;
/** Cost of traversing one internal node relative to intersecting one leaf. */
const TRAVERSAL_COST = 1;
const INTERSECT_COST = 1;

/**
 * Dynamic BVH over AABBs.
 *
 * - Bulk build uses top-down binned SAH (surface area heuristic) splits.
 * - insert/remove keep the tree valid and refit ancestor bounds; insert
 *   descends along the path of minimal surface-area increase.
 * - raycast prunes subtrees whose box is missed or farther than the best hit.
 */
export class BVH<T> {
  root: Node<T> | null = null;
  /** Number of leaves (objects) in the tree. */
  size = 0;
  readonly stats: BVHStats = { nodeVisits: 0, leafTests: 0 };

  /** Bulk-build a balanced tree from items using SAH. */
  static build<T>(items: BVHItem<T>[]): BVH<T> {
    const tree = new BVH<T>();
    if (items.length === 0) return tree;
    const leaves = items.map(makeLeaf);
    tree.root = BVH.buildRecursive(leaves);
    tree.size = leaves.length;
    return tree;
  }

  private static buildRecursive<T>(leaves: Node<T>[]): Node<T> {
    if (leaves.length === 1) return leaves[0];

    // Centroid bounds decide the split axes.
    const centroidBounds = AABB.empty(leaves[0].aabb.dim);
    for (const leaf of leaves) {
      centroidBounds.expandToIncludePoint(leaf.aabb.centroid());
    }

    let bestAxis = -1;
    let bestBin = -1;
    let bestCost = Infinity;

    for (let axis = 0; axis < centroidBounds.dim; axis++) {
      const extent = centroidBounds.extent(axis);
      if (extent <= 1e-12) continue; // degenerate axis, nothing to split on

      const binCount = SAH_BIN_COUNT;
      const binBox: AABB[] = new Array(binCount);
      const binLeafCount: number[] = new Array(binCount).fill(0);
      const scale = binCount / extent;
      const base = centroidBounds.min[axis];

      for (const leaf of leaves) {
        const c = (leaf.aabb.min[axis] + leaf.aabb.max[axis]) * 0.5;
        let b = Math.floor((c - base) * scale);
        if (b < 0) b = 0;
        else if (b >= binCount) b = binCount - 1;
        if (!binBox[b]) binBox[b] = leaf.aabb.clone();
        else binBox[b].expandToIncludeBox(leaf.aabb);
        binLeafCount[b]++;
      }

      // Sweep the bins, accumulating left/right areas and counts.
      const leftArea = new Array(binCount - 1).fill(0);
      const leftCount = new Array(binCount - 1).fill(0);
      let accBox: AABB | null = null;
      let accCount = 0;
      for (let i = 0; i < binCount - 1; i++) {
        if (binBox[i]) {
          accBox = accBox ? AABB.union(accBox, binBox[i]) : binBox[i].clone();
          accCount += binLeafCount[i];
        }
        leftArea[i] = accBox ? accBox.surfaceArea() : 0;
        leftCount[i] = accCount;
      }
      accBox = null;
      accCount = 0;
      for (let i = binCount - 1; i > 0; i--) {
        if (binBox[i]) {
          accBox = accBox ? AABB.union(accBox, binBox[i]) : binBox[i].clone();
          accCount += binLeafCount[i];
        }
        const rightArea = accBox ? accBox.surfaceArea() : 0;
        const cost =
          TRAVERSAL_COST +
          INTERSECT_COST * (leftArea[i - 1] * leftCount[i - 1] + rightArea * accCount);
        if (cost < bestCost) {
          bestCost = cost;
          bestAxis = axis;
          bestBin = i;
        }
      }
    }

    // Fallback: no useful split found (all centroids coincident) — split in half.
    if (bestAxis < 0) {
      const mid = leaves.length >> 1;
      return makeInternal(
        BVH.buildRecursive(leaves.slice(0, mid)),
        BVH.buildRecursive(leaves.slice(mid)),
      );
    }

    const extent = centroidBounds.extent(bestAxis);
    const scale = SAH_BIN_COUNT / extent;
    const base = centroidBounds.min[bestAxis];
    const leftLeaves: Node<T>[] = [];
    const rightLeaves: Node<T>[] = [];
    for (const leaf of leaves) {
      const c = (leaf.aabb.min[bestAxis] + leaf.aabb.max[bestAxis]) * 0.5;
      let b = Math.floor((c - base) * scale);
      if (b < 0) b = 0;
      else if (b >= SAH_BIN_COUNT) b = SAH_BIN_COUNT - 1;
      if (b < bestBin) leftLeaves.push(leaf);
      else rightLeaves.push(leaf);
    }
    // Guard against a lopsided numerical edge case.
    if (leftLeaves.length === 0 || rightLeaves.length === 0) {
      const mid = leaves.length >> 1;
      return makeInternal(
        BVH.buildRecursive(leaves.slice(0, mid)),
        BVH.buildRecursive(leaves.slice(mid)),
      );
    }
    return makeInternal(BVH.buildRecursive(leftLeaves), BVH.buildRecursive(rightLeaves));
  }

  /**
   * Insert an item. Descends along the child whose merged bounds grow least
   * in surface area (SAH-style greedy choice), then refits ancestors.
   * Returns the leaf handle needed for `remove`.
   */
  insert(item: BVHItem<T>): BVHItem<T> {
    const leaf = makeLeaf(item);
    if (!this.root) {
      this.root = leaf;
      this.size = 1;
      return item;
    }

    let node = this.root;
    while (node.left !== null) {
      const leftCost = AABB.union(node.left!.aabb, item.aabb).surfaceArea();
      const rightCost = AABB.union(node.right!.aabb, item.aabb).surfaceArea();
      node = leftCost < rightCost ? node.left! : node.right!;
    }

    const sibling = node;
    const oldParent = sibling.parent;
    const newParent = makeInternal(sibling, leaf);
    newParent.parent = oldParent;
    if (oldParent === null) {
      this.root = newParent;
    } else if (oldParent.left === sibling) {
      oldParent.left = newParent;
    } else {
      oldParent.right = newParent;
    }

    this.refitUpwards(newParent.parent);
    this.size++;
    return item;
  }

  /** Remove a previously inserted item. Returns true if it was present. */
  remove(item: BVHItem<T>): boolean {
    const leaf = this.findLeaf(item);
    if (!leaf) return false;

    const parent = leaf.parent;
    if (parent === null) {
      this.root = null;
      this.size = 0;
      return true;
    }
    const sibling = parent.left === leaf ? parent.right! : parent.left!;
    const grand = parent.parent;
    sibling.parent = grand;
    if (grand === null) {
      this.root = sibling;
    } else if (grand.left === parent) {
      grand.left = sibling;
    } else {
      grand.right = sibling;
    }
    this.refitUpwards(grand);
    this.size--;
    return true;
  }

  private findLeaf(item: BVHItem<T>): Node<T> | null {
    // Leaves are found by identity of the item record. A stack walk is
    // O(n) worst case; callers doing heavy churn should keep handles.
    const stack: (Node<T> | null)[] = [this.root];
    while (stack.length > 0) {
      const node = stack.pop()!;
      if (node === null) continue;
      if (node.item === item) return node;
      if (node.left) stack.push(node.left, node.right);
    }
    return null;
  }

  private refitUpwards(start: Node<T> | null): void {
    let node = start;
    while (node) {
      node.aabb = AABB.union(node.left!.aabb, node.right!.aabb);
      node.height = Math.max(node.left!.height, node.right!.height) + 1;
      node = node.parent;
    }
  }

  /**
   * Cast a ray and return the closest hit, or null.
   *
   * `intersect` is called only for leaves whose box the ray can still hit
   * closer than the best distance found so far; it must return the hit
   * distance t >= 0 for the payload, or null on a miss.
   */
  raycast(
    origin: number[],
    dir: number[],
    intersect: (data: T, aabb: AABB) => number | null,
  ): RayHit<T> | null {
    if (!this.root) return null;
    const invDir = dir.map((d) => 1 / d);
    let bestT = Infinity;
    let bestData: T | null = null;

    // Stack entries carry the box entry distance so nearer nodes pop first.
    const stack: { node: Node<T>; t: number }[] = [];
    const rootT = this.root.aabb.rayHit(origin, invDir, bestT);
    if (rootT !== null) stack.push({ node: this.root, t: rootT });

    while (stack.length > 0) {
      const { node, t } = stack.pop()!;
      this.stats.nodeVisits++;
      if (t > bestT) continue; // a closer hit already exists

      if (node.item !== null) {
        this.stats.leafTests++;
        const hit = intersect(node.item.data, node.aabb);
        if (hit !== null && hit >= 0 && hit < bestT) {
          bestT = hit;
          bestData = node.item.data;
        }
        continue;
      }

      const leftT = node.left!.aabb.rayHit(origin, invDir, bestT);
      const rightT = node.right!.aabb.rayHit(origin, invDir, bestT);
      if (leftT !== null && rightT !== null) {
        // Push farther first so the nearer child is processed first.
        if (leftT < rightT) {
          stack.push({ node: node.right!, t: rightT });
          stack.push({ node: node.left!, t: leftT });
        } else {
          stack.push({ node: node.left!, t: leftT });
          stack.push({ node: node.right!, t: rightT });
        }
      } else if (leftT !== null) {
        stack.push({ node: node.left!, t: leftT });
      } else if (rightT !== null) {
        stack.push({ node: node.right!, t: rightT });
      }
    }

    return bestData === null ? null : { data: bestData, t: bestT };
  }

  /** Visit every leaf whose box overlaps `box`. */
  query(box: AABB, visit: (data: T) => void): void {
    const stack: (Node<T> | null)[] = [this.root];
    while (stack.length > 0) {
      const node = stack.pop()!;
      if (node === null || !node.aabb.overlaps(box)) continue;
      if (node.item !== null) visit(node.item.data);
      else stack.push(node.left, node.right);
    }
  }

  resetStats(): void {
    this.stats.nodeVisits = 0;
    this.stats.leafTests = 0;
  }

  /**
   * Structural invariant check used by tests: bounds are tight unions,
   * heights consistent, leaf count matches `size`.
   */
  validate(): boolean {
    let leaves = 0;
    const walk = (node: Node<T> | null): boolean => {
      if (node === null) return true;
      if (node.item !== null) {
        leaves++;
        return node.left === null && node.right === null && node.height === 0;
      }
      if (node.left === null || node.right === null) return false;
      const expected = AABB.union(node.left.aabb, node.right.aabb);
      for (let i = 0; i < expected.dim; i++) {
        if (
          Math.abs(expected.min[i] - node.aabb.min[i]) > 1e-9 ||
          Math.abs(expected.max[i] - node.aabb.max[i]) > 1e-9
        ) {
          return false;
        }
      }
      if (node.height !== Math.max(node.left.height, node.right.height) + 1) return false;
      return walk(node.left) && walk(node.right);
    };
    return walk(this.root) && leaves === this.size;
  }
}
