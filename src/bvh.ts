import { AABB } from './aabb.ts';
import type { Ray } from './ray.ts';

/** User-supplied accessor: world-space bounds of an object. */
export type BoundsFn<T> = (obj: T) => AABB;
/** User-supplied precise intersection: returns hit distance t >= 0 or null. */
export type IntersectFn<T> = (obj: T, ray: Ray) => number | null;

export interface RaycastHit<T> {
  object: T;
  t: number;
}

/** Per-query traversal counters, used to measure pruning efficiency. */
export interface TraversalStats {
  internalNodesVisited: number;
  leafNodesVisited: number;
  totalLeaves: number;
  /** Leaves never touched because an ancestor AABB rejected the ray. */
  leavesSkipped: number;
}

interface BVHNode<T> {
  bounds: AABB;
  parent: BVHNode<T> | null;
  left: BVHNode<T> | null;
  right: BVHNode<T> | null;
  object: T | null; // non-null only on leaves
  height: number;
}

const isLeaf = <T>(n: BVHNode<T>): boolean => n.object !== null;

/** Number of bins used by the binned SAH builder. */
const SAH_BINS = 12;

interface BuildItem<T> {
  object: T;
  bounds: AABB;
  cx: number;
  cy: number;
  cz: number;
}

/**
 * Dynamic BVH over AABBs.
 *
 * - `BVH.build()` constructs a high-quality tree top-down with the
 *   Surface Area Heuristic (binned SAH over all three axes).
 * - `insert()` / `remove()` keep the tree valid for dynamic scenes,
 *   using SAH-guided sibling search on insertion (Box2D-style).
 * - `raycast()` traverses near-first and prunes any subtree whose AABB
 *   is missed by the ray or lies beyond the current best hit.
 */
export class BVH<T> {
  root: BVHNode<T> | null = null;
  private readonly leafOf = new Map<T, BVHNode<T>>();
  private readonly getBounds: BoundsFn<T>;
  readonly stats: TraversalStats = {
    internalNodesVisited: 0,
    leafNodesVisited: 0,
    totalLeaves: 0,
    leavesSkipped: 0,
  };

  constructor(getBounds: BoundsFn<T>) {
    this.getBounds = getBounds;
  }

  get size(): number {
    return this.leafOf.size;
  }

  /** Build an optimal tree from scratch using binned SAH. */
  static build<T>(objects: readonly T[], getBounds: BoundsFn<T>): BVH<T> {
    const tree = new BVH(getBounds);
    if (objects.length === 0) return tree;
    const items: BuildItem<T>[] = objects.map((object) => {
      const bounds = getBounds(object);
      const c = bounds.centroid();
      return { object, bounds, cx: c.x, cy: c.y, cz: c.z };
    });
    tree.root = tree.buildRecursive(items);
    tree.root.parent = null;
    return tree;
  }

  private buildRecursive(items: BuildItem<T>[]): BVHNode<T> {
    if (items.length === 1) {
      return this.makeLeaf(items[0].object, items[0].bounds);
    }

    // Centroid bounds, to find the split axis/extent.
    let cMinX = Infinity, cMinY = Infinity, cMinZ = Infinity;
    let cMaxX = -Infinity, cMaxY = -Infinity, cMaxZ = -Infinity;
    for (const it of items) {
      if (it.cx < cMinX) cMinX = it.cx;
      if (it.cy < cMinY) cMinY = it.cy;
      if (it.cz < cMinZ) cMinZ = it.cz;
      if (it.cx > cMaxX) cMaxX = it.cx;
      if (it.cy > cMaxY) cMaxY = it.cy;
      if (it.cz > cMaxZ) cMaxZ = it.cz;
    }

    // Binned SAH over the three axes.
    let bestAxis = -1;
    let bestSplit = 0;
    let bestCost = Infinity;
    const extents = [cMaxX - cMinX, cMaxY - cMinY, cMaxZ - cMinZ];
    for (let axis = 0; axis < 3; axis++) {
      const extent = extents[axis];
      if (extent <= 0) continue;
      const cMin = axis === 0 ? cMinX : axis === 1 ? cMinY : cMinZ;
      const scale = SAH_BINS / extent;
      const bins: { count: number; bounds: AABB }[] = [];
      for (let i = 0; i < SAH_BINS; i++) bins.push({ count: 0, bounds: AABB.empty() });
      for (const it of items) {
        const c = axis === 0 ? it.cx : axis === 1 ? it.cy : it.cz;
        let b = Math.floor((c - cMin) * scale);
        if (b >= SAH_BINS) b = SAH_BINS - 1;
        bins[b].count++;
        bins[b].bounds = AABB.union(bins[b].bounds, it.bounds);
      }
      // Sweep left-to-right evaluating split cost SA(L)*|L| + SA(R)*|R|.
      const leftArea = new Array<number>(SAH_BINS - 1);
      const leftCount = new Array<number>(SAH_BINS - 1);
      let accBounds = AABB.empty();
      let accCount = 0;
      for (let i = 0; i < SAH_BINS - 1; i++) {
        accBounds = AABB.union(accBounds, bins[i].bounds);
        accCount += bins[i].count;
        leftArea[i] = accBounds.surfaceArea();
        leftCount[i] = accCount;
      }
      accBounds = AABB.empty();
      accCount = 0;
      for (let i = SAH_BINS - 1; i > 0; i--) {
        accBounds = AABB.union(accBounds, bins[i].bounds);
        accCount += bins[i].count;
        const cost = leftArea[i - 1] * leftCount[i - 1] + accBounds.surfaceArea() * accCount;
        if (cost < bestCost) {
          bestCost = cost;
          bestAxis = axis;
          bestSplit = i;
        }
      }
    }

    let leftItems: BuildItem<T>[];
    let rightItems: BuildItem<T>[];
    if (bestAxis === -1) {
      // All centroids coincide: fall back to a median split.
      const sorted = [...items].sort((a, b) => a.cx - b.cx || a.cy - b.cy || a.cz - b.cz);
      const mid = sorted.length >> 1;
      leftItems = sorted.slice(0, mid);
      rightItems = sorted.slice(mid);
    } else {
      const cMin = bestAxis === 0 ? cMinX : bestAxis === 1 ? cMinY : cMinZ;
      const scale = SAH_BINS / extents[bestAxis];
      leftItems = [];
      rightItems = [];
      for (const it of items) {
        const c = bestAxis === 0 ? it.cx : bestAxis === 1 ? it.cy : it.cz;
        let b = Math.floor((c - cMin) * scale);
        if (b >= SAH_BINS) b = SAH_BINS - 1;
        (b < bestSplit ? leftItems : rightItems).push(it);
      }
      if (leftItems.length === 0 || rightItems.length === 0) {
        const mid = items.length >> 1;
        leftItems = items.slice(0, mid);
        rightItems = items.slice(mid);
      }
    }

    const left = this.buildRecursive(leftItems);
    const right = this.buildRecursive(rightItems);
    const node: BVHNode<T> = {
      bounds: AABB.union(left.bounds, right.bounds),
      parent: null,
      left,
      right,
      object: null,
      height: Math.max(left.height, right.height) + 1,
    };
    left.parent = node;
    right.parent = node;
    return node;
  }

  private makeLeaf(object: T, bounds: AABB): BVHNode<T> {
    const node: BVHNode<T> = {
      bounds: bounds.clone(),
      parent: null,
      left: null,
      right: null,
      object,
      height: 0,
    };
    this.leafOf.set(object, node);
    return node;
  }

  /** Insert an object, choosing the sibling that minimizes SAH cost. */
  insert(object: T): void {
    if (this.leafOf.has(object)) return;
    const leaf = this.makeLeaf(object, this.getBounds(object));
    if (!this.root) {
      this.root = leaf;
      return;
    }

    // Greedy best-sibling search (SAH descent), Box2D b2DynamicTree style.
    const leafBounds = leaf.bounds;
    let bestSibling = this.root;
    let bestCost = AABB.union(this.root.bounds, leafBounds).surfaceArea();
    const stack: { node: BVHNode<T>; inheritedCost: number }[] = [
      { node: this.root, inheritedCost: 0 },
    ];
    while (stack.length > 0) {
      const { node, inheritedCost } = stack.pop()!;
      const combinedArea = AABB.union(node.bounds, leafBounds).surfaceArea();
      const directCost = combinedArea + inheritedCost;
      if (directCost < bestCost) {
        bestCost = directCost;
        bestSibling = node;
      }
      const childInherited =
        inheritedCost + combinedArea - node.bounds.surfaceArea();
      // Lower bound on anything below: if it cannot beat bestCost, prune.
      if (childInherited + leafBounds.surfaceArea() < bestCost) {
        if (node.left) stack.push({ node: node.left, inheritedCost: childInherited });
        if (node.right) stack.push({ node: node.right, inheritedCost: childInherited });
      }
    }

    // Re-seat the sibling under a fresh internal node paired with the leaf.
    const sibling = bestSibling;
    const oldParent = sibling.parent;
    const newParent: BVHNode<T> = {
      bounds: AABB.union(sibling.bounds, leaf.bounds),
      parent: oldParent,
      left: sibling,
      right: leaf,
      object: null,
      height: sibling.height + 1,
    };
    sibling.parent = newParent;
    leaf.parent = newParent;
    if (oldParent) {
      if (oldParent.left === sibling) oldParent.left = newParent;
      else oldParent.right = newParent;
    } else {
      this.root = newParent;
    }

    // Refit ancestors.
    let cursor: BVHNode<T> | null = newParent.parent;
    while (cursor) {
      const l = cursor.left!;
      const r = cursor.right!;
      cursor.bounds = AABB.union(l.bounds, r.bounds);
      cursor.height = Math.max(l.height, r.height) + 1;
      cursor = cursor.parent;
    }
  }

  /** Remove a previously inserted object. Returns false if absent. */
  remove(object: T): boolean {
    const leaf = this.leafOf.get(object);
    if (!leaf) return false;
    this.leafOf.delete(object);

    if (leaf === this.root) {
      this.root = null;
      return true;
    }
    const parent = leaf.parent!;
    const sibling = parent.left === leaf ? parent.right! : parent.left!;
    const grandParent = parent.parent;
    sibling.parent = grandParent;
    if (grandParent) {
      if (grandParent.left === parent) grandParent.left = sibling;
      else grandParent.right = sibling;
    } else {
      this.root = sibling;
    }

    let cursor: BVHNode<T> | null = grandParent;
    while (cursor) {
      const l = cursor.left!;
      const r = cursor.right!;
      cursor.bounds = AABB.union(l.bounds, r.bounds);
      cursor.height = Math.max(l.height, r.height) + 1;
      cursor = cursor.parent;
    }
    return true;
  }

  clear(): void {
    this.root = null;
    this.leafOf.clear();
  }

  /**
   * Cast a ray and return the nearest hit, or null.
   * Traversal is near-first and prunes subtrees whose AABB is missed
   * or farther than the current best hit. Counters land in `this.stats`.
   */
  raycast(ray: Ray, intersect: IntersectFn<T>): RaycastHit<T> | null {
    const stats = this.stats;
    stats.internalNodesVisited = 0;
    stats.leafNodesVisited = 0;
    stats.totalLeaves = this.leafOf.size;
    stats.leavesSkipped = 0;

    if (!this.root) return null;

    let bestT = Infinity;
    let bestObject: T | null = null;
    const stack: BVHNode<T>[] = [this.root];

    while (stack.length > 0) {
      const node = stack.pop()!;
      const t = node.bounds.intersectRay(ray);
      if (t === null || t > bestT) continue;

      if (isLeaf(node)) {
        stats.leafNodesVisited++;
        const hit = intersect(node.object as T, ray);
        if (hit !== null && hit >= 0 && hit < bestT) {
          bestT = hit;
          bestObject = node.object;
        }
        continue;
      }

      stats.internalNodesVisited++;
      const left = node.left!;
      const right = node.right!;
      // Near-first ordering improves early bestT and thus pruning.
      const tLeft = left.bounds.intersectRay(ray);
      const tRight = right.bounds.intersectRay(ray);
      if (tLeft !== null && tRight !== null) {
        if (tLeft < tRight) {
          stack.push(right);
          stack.push(left);
        } else {
          stack.push(left);
          stack.push(right);
        }
      } else if (tLeft !== null) {
        stack.push(left);
      } else if (tRight !== null) {
        stack.push(right);
      }
    }

    stats.leavesSkipped = stats.totalLeaves - stats.leafNodesVisited;
    return bestObject === null ? null : { object: bestObject, t: bestT };
  }

  /** Structural validation, intended for tests. Returns a list of problems. */
  validate(): string[] {
    const problems: string[] = [];
    const seen = new Set<BVHNode<T>>();
    let leafCount = 0;
    const visit = (node: BVHNode<T>, parent: BVHNode<T> | null): void => {
      if (seen.has(node)) problems.push('node visited twice');
      seen.add(node);
      if (node.parent !== parent) problems.push('broken parent link');
      if (isLeaf(node)) {
        leafCount++;
        const actual = this.getBounds(node.object as T);
        if (!node.bounds.intersects(actual)) {
          problems.push('leaf bounds do not contain object bounds');
        }
      } else {
        if (!node.left || !node.right) {
          problems.push('internal node missing a child');
          return;
        }
        const expect = AABB.union(node.left.bounds, node.right.bounds);
        const eps = 1e-9;
        for (const axis of ['x', 'y', 'z'] as const) {
          if (node.bounds.min[axis] > expect.min[axis] + eps) problems.push('bounds too small (min)');
          if (node.bounds.max[axis] < expect.max[axis] - eps) problems.push('bounds too small (max)');
        }
        visit(node.left, node);
        visit(node.right, node);
      }
    };
    if (this.root) visit(this.root, null);
    if (leafCount !== this.leafOf.size) {
      problems.push(`leaf count ${leafCount} != map size ${this.leafOf.size}`);
    }
    return problems;
  }
}
