export interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

export type FillRule = "nonzero" | "evenodd";

export interface PathShape {
  kind: "path";
  /** Subpaths; each is a flat [x0, y0, x1, y1, ...] list, implicitly closed. */
  subpaths: number[][];
  fillRule: FillRule;
}

export interface RectShape {
  kind: "rect";
  rect: Rect;
}

export type Shape = PathShape | RectShape;

export function rectShape(x: number, y: number, w: number, h: number): RectShape {
  return { kind: "rect", rect: { x, y, w, h } };
}

export function pathShape(subpaths: number[][], fillRule: FillRule = "nonzero"): PathShape {
  return { kind: "path", subpaths, fillRule };
}

export function pointInRect(x: number, y: number, r: Rect): boolean {
  return x >= r.x && x < r.x + r.w && y >= r.y && y < r.y + r.h;
}

/** Ray casting with a half-open edge rule so results are deterministic on boundaries. */
export function pointInPolygon(x: number, y: number, pts: number[]): boolean {
  let inside = false;
  const n = pts.length / 2;
  for (let i = 0, j = n - 1; i < n; j = i++) {
    const xi = pts[2 * i];
    const yi = pts[2 * i + 1];
    const xj = pts[2 * j];
    const yj = pts[2 * j + 1];
    if (yi > y !== yj > y && x < ((xj - xi) * (y - yi)) / (yj - yi) + xi) {
      inside = !inside;
    }
  }
  return inside;
}

function windingNumber(x: number, y: number, pts: number[]): number {
  let wn = 0;
  const n = pts.length / 2;
  for (let i = 0, j = n - 1; i < n; j = i++) {
    const xi = pts[2 * i];
    const yi = pts[2 * i + 1];
    const xj = pts[2 * j];
    const yj = pts[2 * j + 1];
    if (yi <= y) {
      if (yj > y && (xj - xi) * (y - yi) - (x - xi) * (yj - yi) > 0) wn++;
    } else if (yj <= y && (xj - xi) * (y - yi) - (x - xi) * (yj - yi) < 0) {
      wn--;
    }
  }
  return wn;
}

export function pointInShape(x: number, y: number, shape: Shape): boolean {
  if (shape.kind === "rect") return pointInRect(x, y, shape.rect);
  if (shape.fillRule === "evenodd") {
    let inside = false;
    for (const sp of shape.subpaths) {
      if (pointInPolygon(x, y, sp)) inside = !inside;
    }
    return inside;
  }
  let wn = 0;
  for (const sp of shape.subpaths) wn += windingNumber(x, y, sp);
  return wn !== 0;
}
