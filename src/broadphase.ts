import type { AABB } from './aabb.ts';
import { cloneAABB, contains, fatten } from './aabb.ts';
import { DynamicTree } from './dynamic-tree.ts';

export interface BodyHandle {
  /** Body id as supplied by the caller. */
  id: number;
  /** Tight (exact) AABB of the body, as last reported by the caller. */
  tightAABB: AABB;
  /** Proxy id inside the dynamic tree. */
  proxyId: number;
}

export interface CollisionPair {
  /** Canonical ordering: bodyA < bodyB. */
  bodyA: number;
  bodyB: number;
}

/**
 * Broadphase collision filter.
 *
 * Maintains one fat-AABB proxy per body in an incremental dynamic AABB
 * tree. When bodies move, `moveBody` updates the proxy (reinserting only
 * when the tight AABB escapes its fat margin) and marks the body dirty.
 * `computePairs` then runs tree queries only for dirty bodies, returning
 * the deduplicated set of candidate pairs whose *tight* AABBs overlap —
 * ready to hand off to the narrowphase.
 */
export class Broadphase {
  private tree = new DynamicTree<BodyHandle>();
  private bodies = new Map<number, BodyHandle>();
  private moved = new Set<number>();
  private margin: number;

  /**
   * @param margin absolute fattening margin applied to every proxy AABB.
   *               Larger margins mean fewer reinsertions but more query
   *               candidates; tight AABB overlap is always re-checked, so
   *               the margin never affects correctness of the pair list.
   */
  constructor(margin = 0.1) {
    this.margin = margin;
  }

  get bodyCount(): number {
    return this.bodies.size;
  }

  addBody(bodyId: number, tightAABB: AABB): void {
    if (this.bodies.has(bodyId)) {
      throw new Error(`body ${bodyId} already exists`);
    }
    const handle: BodyHandle = {
      id: bodyId,
      tightAABB: cloneAABB(tightAABB),
      proxyId: -1,
    };
    handle.proxyId = this.tree.createProxy(fatten(tightAABB, this.margin), handle);
    this.bodies.set(bodyId, handle);
    this.moved.add(bodyId);
  }

  removeBody(bodyId: number): void {
    const handle = this.mustGet(bodyId);
    this.tree.destroyProxy(handle.proxyId);
    this.bodies.delete(bodyId);
    this.moved.delete(bodyId);
  }

  /**
   * Update a body's tight AABB after it moved. The tree proxy is only
   * reinserted when the new tight AABB is no longer contained in the
   * existing fat AABB — this is what makes updates incremental.
   */
  moveBody(bodyId: number, tightAABB: AABB): void {
    const handle = this.mustGet(bodyId);
    const fat = this.tree.getFatAABB(handle.proxyId);
    if (!contains(fat, tightAABB)) {
      this.tree.updateProxy(handle.proxyId, fatten(tightAABB, this.margin));
    }
    handle.tightAABB = cloneAABB(tightAABB);
    this.moved.add(bodyId);
  }

  getTightAABB(bodyId: number): AABB {
    return cloneAABB(this.mustGet(bodyId).tightAABB);
  }

  /**
   * Self-collision traversal: for every body moved since the last call,
   * query the tree and collect candidate pairs whose tight AABBs overlap.
   * Returns pairs in canonical (bodyA < bodyB) order, deduplicated.
   * The dirty set is cleared afterwards.
   */
  computePairs(): CollisionPair[] {
    // Strategy: with few dirty bodies, issue one tree query per dirty body.
    // With many dirty bodies, a single self-collision traversal of the
    // whole tree is cheaper — internal nodes prune entire subtrees.
    const pairs = this.moved.size * 4 < this.bodies.size
      ? this.pairsViaQueries()
      : this.pairsViaSelfTraversal();
    this.moved.clear();
    pairs.sort((p, q) => p.bodyA - q.bodyA || p.bodyB - q.bodyB);
    return pairs;
  }

  /** Incremental path: query the tree once per moved body. */
  private pairsViaQueries(): CollisionPair[] {
    const pairs: CollisionPair[] = [];
    const seen = new Set<number>();
    const queryAABB: AABB = { minX: 0, minY: 0, minZ: 0, maxX: 0, maxY: 0, maxZ: 0 };

    for (const bodyId of this.moved) {
      const handle = this.bodies.get(bodyId);
      if (handle === undefined) continue; // removed while dirty
      const tight = handle.tightAABB;
      queryAABB.minX = tight.minX;
      queryAABB.minY = tight.minY;
      queryAABB.minZ = tight.minZ;
      queryAABB.maxX = tight.maxX;
      queryAABB.maxY = tight.maxY;
      queryAABB.maxZ = tight.maxZ;

      this.tree.query(queryAABB, (otherProxyId) => {
        const other = this.tree.getUserData(otherProxyId) as BodyHandle;
        const otherId = other.id;
        if (otherId === bodyId) return;
        const a = Math.min(bodyId, otherId);
        const b = Math.max(bodyId, otherId);
        const key = a * 0x100000000 + b;
        if (seen.has(key)) return;
        // The tree matched on fat AABBs; confirm with tight bounds so the
        // narrowphase only receives genuinely overlapping candidates.
        if (!tightOverlaps(tight, other.tightAABB)) return;
        seen.add(key);
        pairs.push({ bodyA: a, bodyB: b });
      });
    }
    return pairs;
  }

  /** Batch path: one self-collision traversal, keep pairs touching a moved body. */
  private pairsViaSelfTraversal(): CollisionPair[] {
    const pairs: CollisionPair[] = [];
    const moved = this.moved;
    this.tree.forEachOverlappingPair((proxyA, proxyB) => {
      const handleA = this.tree.getUserData(proxyA) as BodyHandle;
      const handleB = this.tree.getUserData(proxyB) as BodyHandle;
      if (!moved.has(handleA.id) && !moved.has(handleB.id)) return;
      if (!tightOverlaps(handleA.tightAABB, handleB.tightAABB)) return;
      const idA = handleA.id;
      const idB = handleB.id;
      pairs.push(idA < idB ? { bodyA: idA, bodyB: idB } : { bodyA: idB, bodyB: idA });
    });
    return pairs;
  }

  /** Number of bodies currently marked dirty (moved since last computePairs). */
  get dirtyCount(): number {
    return this.moved.size;
  }

  /** Height of the underlying tree — useful for sanity checks. */
  get treeHeight(): number {
    return this.tree.getHeight();
  }

  private mustGet(bodyId: number): BodyHandle {
    const handle = this.bodies.get(bodyId);
    if (handle === undefined) throw new Error(`unknown body ${bodyId}`);
    return handle;
  }
}

function tightOverlaps(a: AABB, b: AABB): boolean {
  return (
    a.minX <= b.maxX && a.maxX >= b.minX &&
    a.minY <= b.maxY && a.maxY >= b.minY &&
    a.minZ <= b.maxZ && a.maxZ >= b.minZ
  );
}
