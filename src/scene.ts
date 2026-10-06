import { Rect } from "./rect";
import { Fraction, frac } from "./fraction";
import { StraightColor } from "./color";

/**
 * Persistent (immutable) layered scene graph.
 *
 * A scene is a tree of groups and rectangle leaves. Groups composite their
 * children (index 0 = bottom) into an isolated buffer, then blend that
 * buffer over the backdrop with the group's overall opacity (SVG-style
 * isolated group semantics).
 *
 * All update operations return a new scene and share unchanged subtrees with
 * the old one, so previously held snapshots keep rendering exactly as before
 * (local updates preserve old snapshots).
 */
export type NodeId = string;

export interface RectNode {
  readonly kind: "rect";
  readonly id: NodeId;
  readonly rect: Rect;
  /** Straight RGBA, alpha in [0,1]; alpha = 1 is fully opaque. */
  readonly color: StraightColor;
}

export interface GroupNode {
  readonly kind: "group";
  readonly id: NodeId;
  /** Overall group opacity in [0,1], applied to the composited children. */
  readonly opacity: Fraction;
  /** Bottom-to-top child order. */
  readonly children: readonly SceneNode[];
}

export type SceneNode = RectNode | GroupNode;

export interface Scene {
  readonly root: GroupNode;
}

export function rectNode(
  id: NodeId,
  rect: Rect,
  color: StraightColor,
): RectNode {
  return { kind: "rect", id, rect, color };
}

export function groupNode(
  id: NodeId,
  opacity: Fraction,
  children: readonly SceneNode[],
): GroupNode {
  return { kind: "group", id, opacity, children };
}

export function scene(rootChildren: readonly SceneNode[]): Scene {
  return { root: groupNode("root", frac(1), rootChildren) };
}

export function findNode(node: SceneNode, id: NodeId): SceneNode | undefined {
  if (node.id === id) return node;
  if (node.kind === "group") {
    for (const c of node.children) {
      const hit = findNode(c, id);
      if (hit) return hit;
    }
  }
  return undefined;
}

/**
 * Replace the node with the given id. Returns a new scene; every ancestor of
 * the replaced node is rebuilt, all other subtrees are shared by reference.
 * Throws if the id does not exist.
 */
export function replaceNode(
  sc: Scene,
  id: NodeId,
  replacement: SceneNode,
): Scene {
  const [root, found] = replaceIn(sc.root, id, replacement);
  if (!found) throw new Error(`replaceNode: unknown id "${id}"`);
  return { root: root as GroupNode };
}

function replaceIn(
  node: SceneNode,
  id: NodeId,
  replacement: SceneNode,
): [SceneNode, boolean] {
  if (node.id === id) return [replacement, true];
  if (node.kind === "group") {
    let changed = false;
    const children = node.children.map((c) => {
      if (changed) return c;
      const [next, hit] = replaceIn(c, id, replacement);
      if (hit) changed = true;
      return hit ? next : c;
    });
    if (!changed) return [node, false];
    return [{ ...node, children }, true];
  }
  return [node, false];
}

/** Remove the node with the given id (root cannot be removed). */
export function removeNode(sc: Scene, id: NodeId): Scene {
  const [root, found] = removeIn(sc.root, id);
  if (!found) throw new Error(`removeNode: unknown id "${id}"`);
  return { root: root as GroupNode };
}

function removeIn(node: SceneNode, id: NodeId): [SceneNode, boolean] {
  if (node.kind !== "group") return [node, false];
  let changed = false;
  const children: SceneNode[] = [];
  for (const c of node.children) {
    if (c.id === id) { changed = true; continue; }
    const [next, hit] = removeIn(c, id);
    if (hit) changed = true;
    children.push(hit ? next : c);
  }
  if (!changed) return [node, false];
  return [{ ...node, children }, true];
}

/** Insert `node` as a child of the group `parentId` at `index` (0 = bottom). */
export function insertChild(
  sc: Scene,
  parentId: NodeId,
  index: number,
  node: SceneNode,
): Scene {
  const [root, found] = insertIn(sc.root, parentId, index, node);
  if (!found) throw new Error(`insertChild: unknown group id "${parentId}"`);
  return { root: root as GroupNode };
}

function insertIn(
  node: SceneNode,
  parentId: NodeId,
  index: number,
  child: SceneNode,
): [SceneNode, boolean] {
  if (node.kind !== "group") return [node, false];
  if (node.id === parentId) {
    const children = [...node.children];
    children.splice(index, 0, child);
    return [{ ...node, children }, true];
  }
  let changed = false;
  const children = node.children.map((c) => {
    if (changed) return c;
    const [next, hit] = insertIn(c, parentId, index, child);
    if (hit) changed = true;
    return hit ? next : c;
  });
  if (!changed) return [node, false];
  return [{ ...node, children }, true];
}

/** Convenience: patch a node's color/opacity/rect via a mapping function. */
export function updateNode(
  sc: Scene,
  id: NodeId,
  patch: (node: SceneNode) => SceneNode,
): Scene {
  const target = findNode(sc.root, id);
  if (!target) throw new Error(`updateNode: unknown id "${id}"`);
  return replaceNode(sc, id, patch(target));
}
