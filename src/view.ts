import type { Mat2D } from "./matrix.ts";
import type { SceneNode } from "./node.ts";
import type { SceneSnapshot } from "./scene.ts";

/**
 * A read-only projection of the scene graph. The live view reflects the
 * current tree; a snapshot view replays the captured transform chain and
 * topology. Nodes absent from a view are excluded from traversal.
 */
export interface TreeView {
  matrixOf(node: SceneNode): Mat2D | undefined;
  childrenOf(node: SceneNode): readonly SceneNode[];
  parentOf(node: SceneNode): SceneNode | null;
  versionOf(node: SceneNode): number;
}

export function liveView(): TreeView {
  return {
    matrixOf: (node) => node.matrix,
    childrenOf: (node) => node.children,
    parentOf: (node) => node.parent,
    versionOf: (node) => node.version,
  };
}

export function snapshotView(snapshot: SceneSnapshot): TreeView {
  return {
    matrixOf: (node) => snapshot.entry(node)?.matrix,
    childrenOf: (node) => snapshot.entry(node)?.children ?? [],
    parentOf: (node) => snapshot.entry(node)?.parent ?? null,
    versionOf: (node) => snapshot.entry(node)?.version ?? 0,
  };
}
