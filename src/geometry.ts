import { apply, type Mat2D } from "./matrix.ts";

/** Inclusive-containment tolerance, absorbing float round-trip error. */
export const EPS = 1e-9;

export interface Rect {
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface BBox {
  minX: number;
  minY: number;
  maxX: number;
  maxY: number;
}

/** Clip shape expressed in the local space of the node that owns it. */
export type ClipShape =
  | { kind: "rect"; rect: Rect }
  | { kind: "circle"; cx: number; cy: number; radius: number };

export function rectClip(x: number, y: number, width: number, height: number): ClipShape {
  return { kind: "rect", rect: { x, y, width, height } };
}

export function circleClip(cx: number, cy: number, radius: number): ClipShape {
  return { kind: "circle", cx, cy, radius };
}

/** Boundary-inclusive rectangle containment. */
export function rectContains(rect: Rect, x: number, y: number): boolean {
  return (
    x >= rect.x - EPS &&
    x <= rect.x + rect.width + EPS &&
    y >= rect.y - EPS &&
    y <= rect.y + rect.height + EPS
  );
}

/** Boundary-inclusive clip containment in the clip owner's local space. */
export function clipContains(clip: ClipShape, x: number, y: number): boolean {
  if (clip.kind === "rect") return rectContains(clip.rect, x, y);
  const dx = x - clip.cx;
  const dy = y - clip.cy;
  const r = clip.radius + EPS;
  return dx * dx + dy * dy <= r * r;
}

export function rectBBox(rect: Rect): BBox {
  return {
    minX: rect.x,
    minY: rect.y,
    maxX: rect.x + rect.width,
    maxY: rect.y + rect.height,
  };
}

/** Local-space bounding box of a clip shape. */
export function clipBBox(clip: ClipShape): BBox {
  if (clip.kind === "rect") return rectBBox(clip.rect);
  return {
    minX: clip.cx - clip.radius,
    minY: clip.cy - clip.radius,
    maxX: clip.cx + clip.radius,
    maxY: clip.cy + clip.radius,
  };
}

/** Conservative bbox of a bbox transformed by a matrix (bbox of the 4 corners). */
export function transformBBox(bbox: BBox, m: Mat2D): BBox {
  const p1 = apply(m, bbox.minX, bbox.minY);
  const p2 = apply(m, bbox.maxX, bbox.minY);
  const p3 = apply(m, bbox.minX, bbox.maxY);
  const p4 = apply(m, bbox.maxX, bbox.maxY);
  return {
    minX: Math.min(p1.x, p2.x, p3.x, p4.x),
    minY: Math.min(p1.y, p2.y, p3.y, p4.y),
    maxX: Math.max(p1.x, p2.x, p3.x, p4.x),
    maxY: Math.max(p1.y, p2.y, p3.y, p4.y),
  };
}

/** Boundary-inclusive bbox containment. */
export function bboxContains(bbox: BBox, x: number, y: number): boolean {
  return (
    x >= bbox.minX - EPS &&
    x <= bbox.maxX + EPS &&
    y >= bbox.minY - EPS &&
    y <= bbox.maxY + EPS
  );
}

/** Intersection of two bboxes; `null` when disjoint. Touching edges count. */
export function intersectBBox(a: BBox, b: BBox): BBox | null {
  const minX = Math.max(a.minX, b.minX);
  const minY = Math.max(a.minY, b.minY);
  const maxX = Math.min(a.maxX, b.maxX);
  const maxY = Math.min(a.maxY, b.maxY);
  if (minX > maxX || minY > maxY) return null;
  return { minX, minY, maxX, maxY };
}
