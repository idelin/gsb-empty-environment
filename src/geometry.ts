/**
 * 几何基础类型。坐标一律采用「像素边界」约定：
 * 像素 (i, j) 覆盖半开区间 [i, i+1) × [j, j+1)，不存在 ±0.5 的中心偏移。
 *
 * - Vec2：连续点坐标，永不在中间过程取整。
 * - Rect：半开区间矩形 [x0, x1) × [y0, y1)，边界本身可以是分数。
 */
export interface Vec2 {
  readonly x: number;
  readonly y: number;
}

export interface Rect {
  readonly x0: number;
  readonly y0: number;
  readonly x1: number;
  readonly y1: number;
}

/** 构造规范化矩形（自动交换端点保证 x0<=x1, y0<=y1）。 */
export function makeRect(x0: number, y0: number, x1: number, y1: number): Rect {
  return {
    x0: Math.min(x0, x1),
    y0: Math.min(y0, y1),
    x1: Math.max(x0, x1),
    y1: Math.max(y0, y1),
  };
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

/** 半开区间下，宽或高为 0 即为空（不覆盖任何像素面积）。 */
export function isEmptyRect(r: Rect): boolean {
  return r.x1 <= r.x0 || r.y1 <= r.y0;
}

/** 半开区间求交；无交集时返回 null。注意不能做端点交换规范化：
 * x0>x1 正是「不相交」的信号，交换会把空集误变成有效矩形。 */
export function intersectRect(a: Rect, b: Rect): Rect | null {
  const r: Rect = {
    x0: Math.max(a.x0, b.x0),
    y0: Math.max(a.y0, b.y0),
    x1: Math.min(a.x1, b.x1),
    y1: Math.min(a.y1, b.y1),
  };
  return isEmptyRect(r) ? null : r;
}

/**
 * 矩形差集 outer \ inner（inner 会先裁剪到 outer 内）。
 * 返回 0~4 个互不重叠的半开矩形，其并与 outer\inner 相同。
 */
export function subtractRect(outer: Rect, inner: Rect): Rect[] {
  const clip = intersectRect(outer, inner);
  if (clip === null) return isEmptyRect(outer) ? [] : [outer];
  const parts: Rect[] = [
    // 左条
    makeRect(outer.x0, outer.y0, clip.x0, outer.y1),
    // 右条
    makeRect(clip.x1, outer.y0, outer.x1, outer.y1),
    // 上条（仅限中间列）
    makeRect(clip.x0, outer.y0, clip.x1, clip.y0),
    // 下条（仅限中间列）
    makeRect(clip.x0, clip.y1, clip.x1, outer.y1),
  ];
  return parts.filter((p) => !isEmptyRect(p));
}

export function rectsAlmostEqual(a: Rect, b: Rect, eps = 1e-9): boolean {
  return (
    Math.abs(a.x0 - b.x0) <= eps &&
    Math.abs(a.y0 - b.y0) <= eps &&
    Math.abs(a.x1 - b.x1) <= eps &&
    Math.abs(a.y1 - b.y1) <= eps
  );
}
