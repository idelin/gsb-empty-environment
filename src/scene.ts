import type { Mat } from './matrix.ts';
import { identity } from './matrix.ts';
import type { ClipShape, Rect } from './geometry.ts';

export interface NodeProps {
  matrix?: Mat;
  /** Local hit rectangle; null means the node itself is not hittable (children may still be). */
  rect?: Rect | null;
  /** Clip shape in this node's local space; applies to the node itself and its whole subtree. */
  clip?: ClipShape | null;
  children?: readonly SceneNode[];
}

/**
 * Immutable scene-graph node. Later children render on top of earlier ones,
 * and children render on top of their parent.
 *
 * Nodes are persistent: `with*` helpers return new nodes and share unchanged
 * subtrees, so an old root reference keeps the entire old transform chain.
 */
export class SceneNode {
  readonly matrix: Mat;
  readonly rect: Rect | null;
  readonly clip: ClipShape | null;
  readonly children: readonly SceneNode[];

  constructor(props: NodeProps = {}) {
    this.matrix = props.matrix ?? identity();
    this.rect = props.rect ?? null;
    this.clip = props.clip ?? null;
    this.children = props.children ?? [];
  }

  withMatrix(matrix: Mat): SceneNode {
    return new SceneNode({ matrix, rect: this.rect, clip: this.clip, children: this.children });
  }

  withRect(rect: Rect | null): SceneNode {
    return new SceneNode({ matrix: this.matrix, rect, clip: this.clip, children: this.children });
  }

  withClip(clip: ClipShape | null): SceneNode {
    return new SceneNode({ matrix: this.matrix, rect: this.rect, clip, children: this.children });
  }

  withChildren(children: readonly SceneNode[]): SceneNode {
    return new SceneNode({ matrix: this.matrix, rect: this.rect, clip: this.clip, children });
  }
}

export function node(props: NodeProps = {}): SceneNode {
  return new SceneNode(props);
}

/**
 * Path-copying update: returns a new root where `target` (found by identity)
 * is replaced with `fn(target)`. The old root and all untouched subtrees are
 * reused unchanged, so previously taken snapshots keep their old chains.
 */
export function update(root: SceneNode, target: SceneNode, fn: (n: SceneNode) => SceneNode): SceneNode {
  if (root === target) return fn(root);
  let changed = false;
  const children = root.children.map((child) => {
    const next = update(child, target, fn);
    if (next !== child) changed = true;
    return next;
  });
  return changed ? root.withChildren(children) : root;
}

/** Depth-first pre-order traversal yielding [node, pathFromRoot] pairs. */
export function* walk(
  root: SceneNode,
): Generator<{ node: SceneNode; path: readonly SceneNode[] }> {
  const stack: Array<{ node: SceneNode; path: readonly SceneNode[] }> = [
    { node: root, path: [root] },
  ];
  while (stack.length > 0) {
    const { node: current, path } = stack.pop()!;
    yield { node: current, path };
    for (let i = current.children.length - 1; i >= 0; i--) {
      const child = current.children[i];
      stack.push({ node: child, path: [...path, child] });
    }
  }
}
