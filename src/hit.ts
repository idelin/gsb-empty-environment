import { IDENTITY, apply, invert, multiply, type Mat2D } from "./matrix.ts";
import { clipContains, rectContains } from "./geometry.ts";
import type { SceneNode } from "./node.ts";
import { liveView, type TreeView } from "./view.ts";

interface Frame {
  node: SceneNode;
  world: Mat2D;
  worldInv: Mat2D;
}

/**
 * Depth-first traversal, topmost first: children are visited in reverse
 * sibling order (last child = topmost) before the node itself. The world
 * point is inverse-transformed layer by layer; a singular world matrix
 * prunes the whole subtree. `onHit` returns true to stop the search.
 */
function walk(
  node: SceneNode,
  ancestors: Frame[],
  view: TreeView,
  x: number,
  y: number,
  onHit: (node: SceneNode) => boolean,
): boolean {
  const local = view.matrixOf(node);
  if (local === undefined) return false;
  const parentWorld = ancestors.length === 0 ? IDENTITY : ancestors[ancestors.length - 1].world;
  const world = multiply(parentWorld, local);
  const worldInv = invert(world);
  if (worldInv === null) return false; // singular: node and descendants unreachable
  const chain = [...ancestors, { node, world, worldInv }];

  const children = view.childrenOf(node);
  for (let i = children.length - 1; i >= 0; i--) {
    if (walk(children[i], chain, view, x, y, onHit)) return true;
  }

  const point = apply(worldInv, x, y);
  if (!rectContains(node.localRect, point.x, point.y)) return false;
  if (!clipsSatisfied(chain, x, y)) return false;
  return onHit(node);
}

/** Every clipping ancestor (rect or circle) must contain the point. */
function clipsSatisfied(chain: Frame[], x: number, y: number): boolean {
  for (let i = 0; i < chain.length - 1; i++) {
    const clip = chain[i].node.clip;
    if (!clip) continue;
    const point = apply(chain[i].worldInv, x, y);
    if (!clipContains(clip, point.x, point.y)) return false;
  }
  return true;
}

/** Topmost node hit at the world-space point, or `null`. */
export function hitTest(
  root: SceneNode,
  x: number,
  y: number,
  view: TreeView = liveView(),
): SceneNode | null {
  let found: SceneNode | null = null;
  walk(root, [], view, x, y, (node) => {
    found = node;
    return true;
  });
  return found;
}

/** All nodes hit at the world-space point, topmost first. */
export function collectHits(
  root: SceneNode,
  x: number,
  y: number,
  view: TreeView = liveView(),
): SceneNode[] {
  const hits: SceneNode[] = [];
  walk(root, [], view, x, y, (node) => {
    hits.push(node);
    return false;
  });
  return hits;
}

/**
 * Exact hit test of a single node: local-geometry check plus every
 * ancestor clip on the path. Independent of any spatial index.
 */
export function exactHit(
  node: SceneNode,
  x: number,
  y: number,
  view: TreeView = liveView(),
): boolean {
  const chain: SceneNode[] = [];
  for (let n: SceneNode | null = node; n; n = view.parentOf(n)) chain.push(n);
  chain.reverse();

  let world = IDENTITY;
  const inverses: Mat2D[] = [];
  for (const current of chain) {
    const local = view.matrixOf(current);
    if (local === undefined) return false;
    world = multiply(world, local);
    const worldInv = invert(world);
    if (worldInv === null) return false;
    inverses.push(worldInv);
  }

  const point = apply(inverses[inverses.length - 1], x, y);
  if (!rectContains(node.localRect, point.x, point.y)) return false;
  for (let i = 0; i < chain.length - 1; i++) {
    const clip = chain[i].clip;
    if (!clip) continue;
    const clipped = apply(inverses[i], x, y);
    if (!clipContains(clip, clipped.x, clipped.y)) return false;
  }
  return true;
}

/**
 * Ranks nodes by hit-test visitation order: rank 0 is the topmost node.
 * Used to pick the topmost exact hit among index candidates.
 */
export function zOrderRanks(
  root: SceneNode,
  view: TreeView = liveView(),
): Map<number, number> {
  const ranks = new Map<number, number>();
  let counter = 0;
  const visit = (node: SceneNode): void => {
    if (view.matrixOf(node) === undefined) return;
    const children = view.childrenOf(node);
    for (let i = children.length - 1; i >= 0; i--) visit(children[i]);
    ranks.set(node.id, counter++);
  };
  visit(root);
  return ranks;
}
