import { IDENTITY, type Mat2D } from "./matrix.ts";
import type { ClipShape, Rect } from "./geometry.ts";

let nextNodeId = 1;

export interface SceneNodeOptions {
  name?: string;
  rect?: Rect;
  matrix?: Mat2D;
  clip?: ClipShape | null;
}

/**
 * A scene-graph node with a local rectangle, a local affine transform
 * (relative to its parent) and an optional clip shape that constrains
 * the hit area of all its descendants.
 *
 * `version` is bumped on every local mutation; a node's position in the
 * ancestor version chain identifies its cached world-space data.
 */
export class SceneNode {
  readonly id: number = nextNodeId++;
  name: string;
  localRect: Rect;
  matrix: Mat2D;
  clip: ClipShape | null;
  parent: SceneNode | null = null;
  readonly children: SceneNode[] = [];
  version = 0;

  constructor(options: SceneNodeOptions = {}) {
    this.name = options.name ?? `node-${this.id}`;
    this.localRect = options.rect ?? { x: 0, y: 0, width: 0, height: 0 };
    this.matrix = options.matrix ?? { ...IDENTITY };
    this.clip = options.clip ?? null;
  }

  addChild(child: SceneNode): this {
    if (child.parent) child.parent.removeChild(child);
    child.parent = this;
    this.children.push(child);
    this.bump();
    return this;
  }

  removeChild(child: SceneNode): this {
    const index = this.children.indexOf(child);
    if (index >= 0) {
      this.children.splice(index, 1);
      child.parent = null;
      this.bump();
    }
    return this;
  }

  setMatrix(matrix: Mat2D): this {
    this.matrix = matrix;
    this.bump();
    return this;
  }

  setLocalRect(rect: Rect): this {
    this.localRect = rect;
    this.bump();
    return this;
  }

  setClip(clip: ClipShape | null): this {
    this.clip = clip;
    this.bump();
    return this;
  }

  private bump(): void {
    this.version++;
  }

  /** Ancestor chain from the root down to (and including) this node. */
  chain(): SceneNode[] {
    const out: SceneNode[] = [];
    let node: SceneNode | null = this;
    while (node) {
      out.push(node);
      node = node.parent;
    }
    return out.reverse();
  }
}
