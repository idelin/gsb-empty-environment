/**
 * 几何基础类型。
 *
 * 坐标约定：像素边界坐标（连续实数）。
 * - 点 (x, y) 表示边界网格上的一个位置，不包含面积；
 * - 矩形为半开区间 [x0, x1) × [y0, y1)，x1/y1 不属于矩形；
 * - 像素 (i, j) 覆盖区域 [i, i+1) × [j, j+1)。
 *
 * 全流程使用 float64，整数取整只允许发生在最终栅格化（见 rasterize.ts）。
 */

export interface Point {
  x: number;
  y: number;
}

/** 半开区间矩形：[x0, x1) × [y0, y1)，要求 x0 <= x1, y0 <= y1 */
export interface Rect {
  x0: number;
  y0: number;
  x1: number;
  y1: number;
}

export function point(x: number, y: number): Point {
  return { x, y };
}

export function rect(x0: number, y0: number, x1: number, y1: number): Rect {
  if (!(x0 <= x1) || !(y0 <= y1)) {
    throw new RangeError(`非法矩形（要求 x0<=x1 且 y0<=y1）：[${x0}, ${y0}, ${x1}, ${y1})`);
  }
  return { x0, y0, x1, y1 };
}

/** 以左上角为原点、给定宽高的矩形 */
export function rectFromSize(width: number, height: number): Rect {
  return rect(0, 0, width, height);
}

export function rectWidth(r: Rect): number {
  return r.x1 - r.x0;
}

export function rectHeight(r: Rect): number {
  return r.y1 - r.y0;
}

export function rectArea(r: Rect): number {
  return rectWidth(r) * rectHeight(r);
}

export function isEmptyRect(r: Rect): boolean {
  return r.x1 <= r.x0 || r.y1 <= r.y0;
}

/**
 * 点包含判断：闭区间。
 * 点是边界位置，恰好落在矩形右/下边缘上的点仍是合法位置
 * （例如裁剪窗口右边缘映射为输出图的右边缘）。
 */
export function rectContainsPoint(r: Rect, p: Point): boolean {
  return p.x >= r.x0 && p.x <= r.x1 && p.y >= r.y0 && p.y <= r.y1;
}

/**
 * 矩形包含判断：半开区间语义。
 * inner 完全落在 outer 内（含边界重合）时返回 true。
 */
export function rectContainsRect(outer: Rect, inner: Rect): boolean {
  return (
    inner.x0 >= outer.x0 &&
    inner.x1 <= outer.x1 &&
    inner.y0 >= outer.y0 &&
    inner.y1 <= outer.y1
  );
}

/** 半开区间求交；无交集（含仅边相接的零面积交集）时返回 null */
export function intersectRect(a: Rect, b: Rect): Rect | null {
  const x0 = Math.max(a.x0, b.x0);
  const y0 = Math.max(a.y0, b.y0);
  const x1 = Math.min(a.x1, b.x1);
  const y1 = Math.min(a.y1, b.y1);
  if (x1 <= x0 || y1 <= y0) return null;
  return { x0, y0, x1, y1 };
}

/**
 * 矩形相减：outer \ inner，返回 0~4 个互不重叠的矩形。
 * inner 不要求是 outer 的子集（内部先求交）。
 */
export function subtractRect(outer: Rect, inner: Rect): Rect[] {
  const hit = intersectRect(outer, inner);
  if (hit === null) return [outer];
  const parts: Rect[] = [];
  // 上条带
  if (hit.y0 > outer.y0) {
    parts.push({ x0: outer.x0, y0: outer.y0, x1: outer.x1, y1: hit.y0 });
  }
  // 下条带
  if (hit.y1 < outer.y1) {
    parts.push({ x0: outer.x0, y0: hit.y1, x1: outer.x1, y1: outer.y1 });
  }
  // 左条带（仅中间高度段）
  if (hit.x0 > outer.x0) {
    parts.push({ x0: outer.x0, y0: hit.y0, x1: hit.x0, y1: hit.y1 });
  }
  // 右条带（仅中间高度段）
  if (hit.x1 < outer.x1) {
    parts.push({ x0: hit.x1, y0: hit.y0, x1: outer.x1, y1: hit.y1 });
  }
  return parts;
}

export function pointsAlmostEqual(a: Point, b: Point, tol = 1e-9): boolean {
  return Math.abs(a.x - b.x) <= tol && Math.abs(a.y - b.y) <= tol;
}

export function rectsAlmostEqual(a: Rect, b: Rect, tol = 1e-9): boolean {
  return (
    Math.abs(a.x0 - b.x0) <= tol &&
    Math.abs(a.y0 - b.y0) <= tol &&
    Math.abs(a.x1 - b.x1) <= tol &&
    Math.abs(a.y1 - b.y1) <= tol
  );
}
