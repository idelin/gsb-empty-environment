import { Color } from "./color";
import { Rational } from "./rational";
import { Rect } from "./rect";

/**
 * Persistent (immutable) scene graph. Children are listed in layer order:
 * index 0 is the bottom layer. Every update returns a new root that shares
 * untouched subtrees with the old one, so old snapshots stay valid.
 */
export interface RectLayer {
  readonly kind: "rect";
  readonly id: string;
  readonly rect: Rect;
  readonly color: Color;
  /** Own opacity in [0, 1]; combined multiplicatively with group opacity. */
  readonly alpha: Rational;
}

export interface LayerGroup {
  readonly kind: "group";
  readonly id: string;
  /** Overall opacity applied to every descendant, in [0, 1]. */
  readonly opacity: Rational;
  readonly children: readonly SceneNode[];
}

export type SceneNode = RectLayer | LayerGroup;

export function rectLayer(
  id: string,
  rect: Rect,
  color: Color,
  alpha: Rational,
): RectLayer {
  return { kind: "rect", id, rect, color, alpha };
}

export function layerGroup(
  id: string,
  opacity: Rational,
  children: readonly SceneNode[],
): LayerGroup {
  return { kind: "group", id, opacity, children };
}

/** A rect layer with its effective (group-combined) alpha, ready to composite. */
export interface FlatLayer {
  readonly id: string;
  readonly rect: Rect;
  readonly color: Color;
  readonly alpha: Rational;
}

/** Flatten the scene into bottom-to-top layers with effective alphas. */
export function flattenScene(root: SceneNode): FlatLayer[] {
  const out: FlatLayer[] = [];
  const walk = (node: SceneNode, opacity: Rational): void => {
    if (node.kind === "rect") {
      out.push({
        id: node.id,
        rect: node.rect,
        color: node.color,
        alpha: opacity.mul(node.alpha),
      });
    } else {
      const next = opacity.mul(node.opacity);
      for (const child of node.children) walk(child, next);
    }
  };
  walk(root, Rational.ONE);
  return out;
}

export function findNode(root: SceneNode, id: string): SceneNode | null {
  if (root.id === id) return root;
  if (root.kind === "group") {
    for (const child of root.children) {
      const hit = findNode(child, id);
      if (hit !== null) return hit;
    }
  }
  return null;
}

export interface RectPatch {
  readonly rect?: Rect;
  readonly color?: Color;
  readonly alpha?: Rational;
}

function rewriteRect(
  node: SceneNode,
  id: string,
  patch: RectPatch,
): [SceneNode, boolean] {
  if (node.kind === "rect") {
    if (node.id !== id) return [node, false];
    return [
      {
        ...node,
        rect: patch.rect ?? node.rect,
        color: patch.color ?? node.color,
        alpha: patch.alpha ?? node.alpha,
      },
      true,
    ];
  }
  let changed = false;
  let found = false;
  const children = node.children.map((child) => {
    const [next, hit] = rewriteRect(child, id, patch);
    if (hit) found = true;
    if (next !== child) changed = true;
    return next;
  });
  return changed ? [{ ...node, children }, true] : [node, found];
}

/** Return a new root with the rect layer `id` patched; old root is untouched. */
export function updateRectLayer(
  root: SceneNode,
  id: string,
  patch: RectPatch,
): SceneNode {
  const [next, found] = rewriteRect(root, id, patch);
  if (!found) throw new Error(`updateRectLayer: unknown rect layer "${id}"`);
  return next;
}

function rewriteGroup(
  node: SceneNode,
  id: string,
  opacity: Rational,
): [SceneNode, boolean] {
  if (node.kind === "group" && node.id === id) {
    return [{ ...node, opacity }, true];
  }
  if (node.kind === "rect") return [node, false];
  let changed = false;
  let found = false;
  const children = node.children.map((child) => {
    const [next, hit] = rewriteGroup(child, id, opacity);
    if (hit) found = true;
    if (next !== child) changed = true;
    return next;
  });
  return changed ? [{ ...node, children }, true] : [node, found];
}

/** Return a new root with group `id`'s overall opacity replaced. */
export function setGroupOpacity(
  root: SceneNode,
  id: string,
  opacity: Rational,
): SceneNode {
  const [next, found] = rewriteGroup(root, id, opacity);
  if (!found) throw new Error(`setGroupOpacity: unknown group "${id}"`);
  return next;
}

/** Return a new root with node `id` (and its subtree) removed. */
export function removeNode(root: SceneNode, id: string): SceneNode {
  const [next, found] = removeInner(root, id);
  if (!found) throw new Error(`removeNode: unknown node "${id}"`);
  return next;
}

function removeInner(node: SceneNode, id: string): [SceneNode, boolean] {
  if (node.kind === "rect") return [node, false];
  let changed = false;
  let found = false;
  const children: SceneNode[] = [];
  for (const child of node.children) {
    if (child.id === id) {
      found = true;
      changed = true;
      continue;
    }
    const [next, hit] = removeInner(child, id);
    if (hit) found = true;
    if (next !== child) changed = true;
    children.push(next);
  }
  return changed ? [{ ...node, children }, true] : [node, found];
}
