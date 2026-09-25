import { multiply, IDENTITY, type Mat } from "./matrix.ts";
import type { Rect, Shape } from "./geometry.ts";
import {
  Mask,
  rasterizeShape,
  union,
  intersect,
  subtract,
  invert,
} from "./mask.ts";

export type BoolOp = "union" | "intersect" | "subtract";

export type SceneNode =
  | { type: "shape"; shape: Shape; transform?: Mat }
  | { type: "group"; op: BoolOp; transform?: Mat; children: SceneNode[] }
  | { type: "invert"; child: SceneNode; region?: Rect };

export function shapeNode(shape: Shape, transform?: Mat): SceneNode {
  return { type: "shape", shape, transform };
}

export function group(op: BoolOp, children: SceneNode[], transform?: Mat): SceneNode {
  return { type: "group", op, transform, children };
}

export function invertNode(child: SceneNode, region?: Rect): SceneNode {
  return { type: "invert", child, region };
}

/**
 * Renders a scene graph into a coverage mask. Nested transforms compose
 * parent-first (child local coordinates are transformed by parent * local).
 * Group ops fold children left-to-right; "subtract" is first-minus-rest.
 */
export function renderScene(
  node: SceneNode,
  width: number,
  height: number,
  ss = 4,
): Mask {
  return renderNode(node, IDENTITY, width, height, ss);
}

function renderNode(
  node: SceneNode,
  parent: Mat,
  width: number,
  height: number,
  ss: number,
): Mask {
  const local = node.transform ? multiply(parent, node.transform) : parent;
  switch (node.type) {
    case "shape":
      return rasterizeShape(node.shape, width, height, local, ss);
    case "invert":
      return invert(renderNode(node.child, parent, width, height, ss), node.region);
    case "group": {
      if (node.children.length === 0) return new Mask(width, height);
      let acc = renderNode(node.children[0], local, width, height, ss);
      for (let i = 1; i < node.children.length; i++) {
        const next = renderNode(node.children[i], local, width, height, ss);
        acc =
          node.op === "union"
            ? union(acc, next)
            : node.op === "intersect"
              ? intersect(acc, next)
              : subtract(acc, next);
      }
      return acc;
    }
  }
}
