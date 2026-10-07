import { apply, invert, type Point } from './matrix.ts';
import { clipContains, rectContains } from './geometry.ts';
import type { SceneNode } from './scene.ts';

export interface Hit {
  node: SceneNode;
  /** The hit point in the node's local coordinates. */
  localPoint: Point;
  /** Path of nodes from the root to the hit node (inclusive). */
  path: readonly SceneNode[];
}

/**
 * Exact hit test: walks the tree topmost-first (later siblings and
 * descendants before ancestors), transforming the world point into each
 * node's local space with the per-level inverse matrix.
 *
 * - A node whose local matrix is singular cannot be inverted, so neither it
 *   nor its subtree is hittable.
 * - Rect/circle containment is boundary-inclusive.
 * - A node's clip (in its own local space) must contain the point, otherwise
 *   the node and its whole subtree are pruned — so clips along the ancestor
 *   path effectively intersect.
 */
export function hitTest(root: SceneNode, worldPoint: Point): Hit | null {
  return visit(root, worldPoint, [root]);
}

function visit(node: SceneNode, pointInParent: Point, path: SceneNode[]): Hit | null {
  const inv = invert(node.matrix);
  if (!inv) return null;
  const local = apply(inv, pointInParent);
  if (node.clip && !clipContains(node.clip, local)) return null;
  for (let i = node.children.length - 1; i >= 0; i--) {
    const hit = visit(node.children[i], local, [...path, node.children[i]]);
    if (hit) return hit;
  }
  if (node.rect && rectContains(node.rect, local)) {
    return { node, localPoint: local, path };
  }
  return null;
}

/**
 * Exact per-path verification: transforms the world point down the given
 * root-to-node path with the inverse matrices, requiring every clip on the
 * path and the final node's rect to contain it.
 */
export function exactPathContains(
  path: readonly SceneNode[],
  worldPoint: Point,
): boolean {
  let p = worldPoint;
  for (const node of path) {
    const inv = invert(node.matrix);
    if (!inv) return false;
    p = apply(inv, p);
    if (node.clip && !clipContains(node.clip, p)) return false;
  }
  const leaf = path[path.length - 1];
  return leaf.rect !== null && rectContains(leaf.rect, p);
}
