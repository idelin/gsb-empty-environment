import { Rect, containsPoint } from "../src/rect";
import { PremulColor, TRANSPARENT, over, scale, toPremul } from "../src/color";
import { SceneNode } from "../src/scene";

/**
 * Pixel-grid oracles. Unit pixel (px, py) is sampled at its center
 * (px + 0.5, py + 0.5); with integer rectangle coordinates the sample point
 * never lies on an edge, so half-open semantics are unambiguous.
 */

/** Index of the topmost opaque layer covering the pixel, or -1. */
export function topmostAt(layers: readonly Rect[], px: number, py: number): number {
  const x = px + 0.5;
  const y = py + 0.5;
  for (let i = layers.length - 1; i >= 0; i--) {
    if (containsPoint(layers[i]!, x, y)) return i;
  }
  return -1;
}

/**
 * Independent point oracle for translucent scenes: evaluate the composited
 * premultiplied color at a point by walking the scene tree directly
 * (no plane partition involved).
 */
export function evalSceneAtPoint(node: SceneNode, x: number, y: number): PremulColor {
  if (node.kind === "rect") {
    return containsPoint(node.rect, x, y) ? toPremul(node.color) : TRANSPARENT;
  }
  if (node.opacity.isZero) return TRANSPARENT;
  let acc: PremulColor = TRANSPARENT;
  for (let i = node.children.length - 1; i >= 0; i--) {
    acc = over(acc, evalSceneAtPoint(node.children[i]!, x, y));
    if (acc.a.isOne) break;
  }
  return scale(acc, node.opacity);
}

/** Does any leaf rect of the scene cover the point? */
export function sceneCoversPoint(node: SceneNode, x: number, y: number): boolean {
  if (node.kind === "rect") return containsPoint(node.rect, x, y);
  return node.children.some((c) => sceneCoversPoint(c, x, y));
}

export interface Box {
  x0: number;
  y0: number;
  x1: number;
  y1: number;
}

export function boundingBox(rects: readonly Rect[]): Box {
  let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity;
  for (const r of rects) {
    x0 = Math.min(x0, r.x0); y0 = Math.min(y0, r.y0);
    x1 = Math.max(x1, r.x1); y1 = Math.max(y1, r.y1);
  }
  return { x0, y0, x1, y1 };
}

export function sceneRects(node: SceneNode, out: Rect[] = []): Rect[] {
  if (node.kind === "rect") out.push(node.rect);
  else for (const c of node.children) sceneRects(c, out);
  return out;
}
