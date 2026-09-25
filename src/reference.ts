import { multiply, applyToPoint, invertMatrix, IDENTITY, type Mat } from "./matrix.ts";
import { pointInShape, pointInRect, type Rect } from "./geometry.ts";
import { Mask } from "./mask.ts";
import type { SceneNode } from "./scene.ts";

/**
 * Independent brute-force reference: evaluates the boolean expression of the
 * scene directly per sub-sample (no coverage arithmetic), with high
 * supersampling. Used to validate the fast path pixel by pixel.
 */
export function referenceCoverage(
  node: SceneNode,
  width: number,
  height: number,
  ss = 48,
): Mask {
  const mask = new Mask(width, height);
  const inv = ss * ss;
  for (let py = 0; py < height; py++) {
    for (let px = 0; px < width; px++) {
      let hits = 0;
      for (let sy = 0; sy < ss; sy++) {
        for (let sx = 0; sx < ss; sx++) {
          const x = px + (sx + 0.5) / ss;
          const y = py + (sy + 0.5) / ss;
          if (evalNode(node, IDENTITY, x, y)) hits++;
        }
      }
      mask.data[py * width + px] = hits / inv;
    }
  }
  return mask;
}

function evalNode(node: SceneNode, parent: Mat, x: number, y: number): boolean {
  switch (node.type) {
    case "shape": {
      const m = node.transform ? multiply(parent, node.transform) : parent;
      const [lx, ly] = applyToPoint(invertMatrix(m), x, y);
      return pointInShape(lx, ly, node.shape);
    }
    case "invert": {
      const region: Rect = node.region ?? { x: 0, y: 0, w: Infinity, h: Infinity };
      if (!pointInRect(x, y, region)) return false;
      return !evalNode(node.child, parent, x, y);
    }
    case "group": {
      const m = node.transform ? multiply(parent, node.transform) : parent;
      const kids = node.children;
      if (kids.length === 0) return false;
      if (node.op === "union") return kids.some((k) => evalNode(k, m, x, y));
      if (node.op === "intersect") return kids.every((k) => evalNode(k, m, x, y));
      return evalNode(kids[0], m, x, y) && !kids.slice(1).some((k) => evalNode(k, m, x, y));
    }
  }
}
