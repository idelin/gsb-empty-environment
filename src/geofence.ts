/**
 * 带权重地理围栏（Geo-Fence）多边形包含判定内核。
 *
 * 判定内核：
 *  - 射线法（Ray Casting，even-odd 规则）
 *  - 风向数法（Winding Number，non-zero 规则）
 *
 * 拓扑定义（严格）：
 *  - 坐标为平面 [lng, lat] 二元组，多边形环为闭合顶点序列（首尾自动闭合）。
 *  - 'boundary'：点精确落在任一环的某条闭边线段上（含与顶点重合）。
 *    默认 eps = 0，即叉积严格等于 0 且落在边的包围盒内；可通过 eps 放宽。
 *  - 'inside'   ：严格内部（不含边界），按指定规则（even-odd / non-zero）判定。
 *  - 'outside'  ：其余情形。孔洞（hole）的内部对围栏而言是 outside，
 *    孔洞的边属于围栏边界。
 *  - 自相交退化多边形：even-odd 与 non-zero 两种规则均可判定，
 *    二者在环绕次数 >= 2 的区域会给出不同结果，均属定义的合法输出。
 */

export type LngLat = readonly [lng: number, lat: number];
export type Ring = readonly LngLat[];

export type PointLocation = 'inside' | 'outside' | 'boundary';
export type ContainmentRule = 'even-odd' | 'non-zero';

export interface LocateOptions {
  /** 包含规则：射线法 even-odd（默认）或风向数法 non-zero。 */
  rule?: ContainmentRule;
  /** 边界容差（叉积绝对值，单位：度^2）。默认 0 = 严格精确。 */
  eps?: number;
}

/** 二维叉积 (b - a) x (p - a)。>0 表示 p 在有向边 a->b 左侧。 */
export function cross(a: LngLat, b: LngLat, p: LngLat): number {
  return (b[0] - a[0]) * (p[1] - a[1]) - (b[1] - a[1]) * (p[0] - a[0]);
}

/** 点是否在线段 [a, b] 上（含端点/顶点重合）。eps 默认 0 = 严格。 */
export function pointOnSegment(p: LngLat, a: LngLat, b: LngLat, eps = 0): boolean {
  if (Math.abs(cross(a, b, p)) > eps) return false;
  return (
    p[0] >= Math.min(a[0], b[0]) - eps &&
    p[0] <= Math.max(a[0], b[0]) + eps &&
    p[1] >= Math.min(a[1], b[1]) - eps &&
    p[1] <= Math.max(a[1], b[1]) + eps
  );
}

function assertRing(ring: Ring): void {
  if (!Array.isArray(ring) || ring.length < 3) {
    throw new Error(`环至少需要 3 个顶点，实际 ${ring?.length ?? 0}`);
  }
}

/** 射线法：自点向 +lng 方向发射线，奇数次穿越为内部（even-odd）。 */
export function rayCastingInside(ring: Ring, p: LngLat): boolean {
  let inside = false;
  for (let i = 0, j = ring.length - 1; i < ring.length; j = i++) {
    const a = ring[i];
    const b = ring[j];
    // 半开区间规则：恰好穿过顶点的射线只计一次，顶点重合已被边界检测拦截。
    if (a[1] > p[1] !== b[1] > p[1]) {
      const xInt = a[0] + ((p[1] - a[1]) * (b[0] - a[0])) / (b[1] - a[1]);
      if (xInt > p[0]) inside = !inside;
    }
  }
  return inside;
}

/** 风向数：结果为非零即内部（non-zero 规则）。返回值即环绕次数。 */
export function windingNumber(ring: Ring, p: LngLat): number {
  let wn = 0;
  for (let i = 0, j = ring.length - 1; i < ring.length; j = i++) {
    const a = ring[i];
    const b = ring[j];
    if (a[1] <= p[1]) {
      if (b[1] > p[1] && cross(a, b, p) > 0) wn += 1; // 向上穿越，点在左侧
    } else {
      if (b[1] <= p[1] && cross(a, b, p) < 0) wn -= 1; // 向下穿越，点在右侧
    }
  }
  return wn;
}

/** 单环三点判定：boundary 优先，其次按规则判定 inside / outside。 */
export function locatePointInRing(ring: Ring, p: LngLat, opts: LocateOptions = {}): PointLocation {
  assertRing(ring);
  const eps = opts.eps ?? 0;
  for (let i = 0, j = ring.length - 1; i < ring.length; j = i++) {
    if (pointOnSegment(p, ring[i], ring[j], eps)) return 'boundary';
  }
  const rule = opts.rule ?? 'even-odd';
  const inside = rule === 'even-odd' ? rayCastingInside(ring, p) : windingNumber(ring, p) !== 0;
  return inside ? 'inside' : 'outside';
}

/** 带权重的地理围栏：外环 + 若干孔洞。 */
export interface GeoFence {
  id: string;
  outer: Ring;
  holes?: readonly Ring[];
  /** 权重，默认 1。多围栏命中时权重高者优先。 */
  weight?: number;
  metadata?: Record<string, unknown>;
}

/**
 * 围栏三点判定：
 *  - 落在外环边界或任一孔洞边界 -> 'boundary'
 *  - 落在外环内部且不在任何孔洞内部 -> 'inside'
 *  - 落在孔洞内部或外环之外 -> 'outside'
 */
export function locatePointInFence(fence: GeoFence, p: LngLat, opts: LocateOptions = {}): PointLocation {
  const outerLoc = locatePointInRing(fence.outer, p, opts);
  if (outerLoc === 'boundary') return 'boundary';
  if (outerLoc === 'outside') return 'outside';
  for (const hole of fence.holes ?? []) {
    const holeLoc = locatePointInRing(hole, p, opts);
    if (holeLoc === 'boundary') return 'boundary';
    if (holeLoc === 'inside') return 'outside';
  }
  return 'inside';
}

/** 一次围栏命中记录。 */
export interface FenceMatch {
  fenceId: string;
  weight: number;
  location: 'inside' | 'boundary';
}

/** 评估一组带权围栏：返回全部命中（inside / boundary），按权重降序。 */
export function evaluateFences(fences: readonly GeoFence[], p: LngLat, opts: LocateOptions = {}): FenceMatch[] {
  const matches: FenceMatch[] = [];
  for (const fence of fences) {
    const location = locatePointInFence(fence, p, opts);
    if (location !== 'outside') {
      matches.push({ fenceId: fence.id, weight: fence.weight ?? 1, location });
    }
  }
  return matches.sort((a, b) => b.weight - a.weight);
}

/** 主导围栏：权重最高的命中；无命中返回 null。 */
export function resolveDominantFence(fences: readonly GeoFence[], p: LngLat, opts: LocateOptions = {}): FenceMatch | null {
  const matches = evaluateFences(fences, p, opts);
  return matches.length > 0 ? matches[0] : null;
}
