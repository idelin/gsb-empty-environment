import { apply, invert, multiply, isInvertible, type Mat, type Point } from './matrix.ts';
import {
  aabbContains,
  aabbOfClip,
  aabbOfRect,
  intersectAABB,
  transformAABB,
  type AABB,
} from './geometry.ts';
import { exactPathContains, type Hit } from './hittest.ts';
import type { SceneNode } from './scene.ts';

interface Entry {
  /** Conservative world AABB of the node's hittable region; null if unhittable. */
  aabb: AABB | null;
  path: readonly SceneNode[];
}

interface Table {
  entries: Map<SceneNode, Entry>;
  /** All nodes in hit-priority order (topmost first). */
  priority: SceneNode[];
}

/**
 * Spatial index over an immutable scene graph.
 *
 * The cache stores world-space AABBs (node rect ∩ all ancestor clip AABBs,
 * in world space). The cache is keyed per root snapshot: because updates are
 * path-copying, changing an ancestor transform produces a new root whose
 * table is rebuilt from scratch (invalidating every affected descendant),
 * while tables of older snapshots keep serving the old transform chain.
 *
 * The AABB is only a filter — the authoritative answer always comes from
 * `exactPathContains`, i.e. local geometry plus clipping.
 */
export class SpatialIndex {
  private tables = new WeakMap<SceneNode, Table>();

  private table(root: SceneNode): Table {
    let t = this.tables.get(root);
    if (!t) {
      t = buildTable(root);
      this.tables.set(root, t);
    }
    return t;
  }

  /** Candidate nodes whose cached world AABB contains the point, topmost first. */
  query(root: SceneNode, worldPoint: Point): SceneNode[] {
    const t = this.table(root);
    const out: SceneNode[] = [];
    for (const node of t.priority) {
      const entry = t.entries.get(node)!;
      if (entry.aabb && aabbContains(entry.aabb, worldPoint)) out.push(node);
    }
    return out;
  }

  /**
   * Indexed hit test. Equivalent to `hitTest(root, worldPoint)`: AABB
   * candidates are confirmed with the exact inverse-transform walk, so local
   * geometry and ancestor clipping are always honored.
   */
  hitTest(root: SceneNode, worldPoint: Point): Hit | null {
    const t = this.table(root);
    for (const node of t.priority) {
      const entry = t.entries.get(node)!;
      if (!entry.aabb || !aabbContains(entry.aabb, worldPoint)) continue;
      if (exactPathContains(entry.path, worldPoint)) {
        // Recompute the local point for the result.
        let p = worldPoint;
        for (const n of entry.path) {
          p = apply(invert(n.matrix)!, p);
        }
        return { node, localPoint: p, path: entry.path };
      }
    }
    return null;
  }
}

function buildTable(root: SceneNode): Table {
  const entries = new Map<SceneNode, Entry>();
  const documentOrder: SceneNode[] = [];

  // parentWorld === null means an ancestor matrix is singular: the whole
  // remaining subtree is unhittable.
  const visit = (
    node: SceneNode,
    parentWorld: Mat | null,
    parentClipAABB: AABB | null,
    path: readonly SceneNode[],
  ): void => {
    documentOrder.push(node);
    let world: Mat | null = null;
    let clipAABB = parentClipAABB;
    if (parentWorld !== null && isInvertible(node.matrix)) {
      world = multiply(parentWorld, node.matrix);
      if (node.clip) {
        const own = aabbOfClip(world, node.clip);
        clipAABB = clipAABB ? intersectAABB(clipAABB, own) : own;
      }
    } else {
      clipAABB = null;
    }
    let aabb: AABB | null = null;
    if (world !== null && node.rect) {
      const rectWorld = transformAABB(world, aabbOfRect(node.rect));
      aabb = clipAABB ? intersectAABB(rectWorld, clipAABB) : rectWorld;
    }
    entries.set(node, { aabb, path });
    for (const child of node.children) {
      visit(child, world, clipAABB, [...path, child]);
    }
  };

  visit(root, { a: 1, b: 0, c: 0, d: 1, e: 0, f: 0 }, null, [root]);

  // Hit-priority order = reverse document order (later subtrees first,
  // descendants before ancestors).
  const priority = documentOrder.reverse();
  return { entries, priority };
}
