/**
 * 带权重地理围栏（Geo-Fence）多边形包含判定内核。
 *
 * 双引擎：
 *  - 射线法（Ray Casting，even-odd 规则）
 *  - 风向数法（Winding Number，non-zero 规则）
 *
 * 支持：凹多边形、带孔洞多边形、自相交退化多边形。
 *
 * 边界严格定义：
 *  - 多边形边界 = 外环全部边 ∪ 全部孔洞环的边（符合 OGC 拓扑定义，
 *    孔洞边缘属于多边形边界）。
 *  - 点 P 在边上 <=> 存在边 (A,B)，使得叉积 (B-A)x(P-A) 的绝对值
 *    不超过 boundaryTolerance（默认 0，即严格精确判定），且 P 落在
 *    边的包围盒内（含端点）。顶点重合是该定义的特例：P 与某顶点
 *    坐标完全相等时，叉积为 0 且位于包围盒内，判定为 boundary。
 *  - 半开区间约定：射线法与风向数法均采用 "yi > y" 半开区间统计
 *    穿越，避免射线恰好穿过顶点时的重复计数。
 */

export interface LngLat {
  lng: number;
  lat: number;
}

export type Ring = ReadonlyArray<LngLat>;

export interface GeoFencePolygon {
  /** 外环（可凹、可自相交），首尾顶点无需重复 */
  outer: Ring;
  /** 孔洞环列表（岛屿镂空） */
  holes?: ReadonlyArray<Ring>;
}

/** evenodd = 射线法奇偶规则；nonzero = 风向数非零规则 */
export type FillRule = 'evenodd' | 'nonzero';

export type Classification = 'inside' | 'outside' | 'boundary';

export interface ClassifyOptions {
  /** 填充规则，默认 'evenodd'（射线法） */
  rule?: FillRule;
  /**
   * 边界判定的叉积绝对值容差，默认 0（严格）。
   * 仅在需要容忍浮点噪声时调大。
   */
  boundaryTolerance?: number;
}

export interface ContainsOptions extends ClassifyOptions {
  /** 边界点是否视为在围栏内，默认 true */
  includeBoundary?: boolean;
}

/** 二维叉积 (B-A) x (P-A)。>0 表示 P 在有向边 A->B 左侧 */
function cross(a: LngLat, b: LngLat, p: LngLat): number {
  return (b.lng - a.lng) * (p.lat - a.lat) - (b.lat - a.lat) * (p.lng - a.lng);
}

/** 遍历环的边，自动闭合，并跳过零长度退化边 */
function* edges(ring: Ring): Generator<[LngLat, LngLat]> {
  const n = ring.length;
  for (let i = 0; i < n; i++) {
    const a = ring[i];
    const b = ring[(i + 1) % n];
    if (a.lng === b.lng && a.lat === b.lat) continue;
    yield [a, b];
  }
}

/**
 * 点是否在线段上（含端点）。
 * eps 为叉积绝对值容差，默认 0 表示严格共线判定。
 */
export function isOnSegment(p: LngLat, a: LngLat, b: LngLat, eps = 0): boolean {
  if (Math.abs(cross(a, b, p)) > eps) return false;
  return (
    p.lng >= Math.min(a.lng, b.lng) - eps &&
    p.lng <= Math.max(a.lng, b.lng) + eps &&
    p.lat >= Math.min(a.lat, b.lat) - eps &&
    p.lat <= Math.max(a.lat, b.lat) + eps
  );
}

/** 射线法：返回点是否在环内（even-odd 规则），不含边界判定 */
export function rayCastContains(p: LngLat, ring: Ring): boolean {
  let inside = false;
  for (const [a, b] of edges(ring)) {
    // 半开区间：恰好一条边的上端点被计入，避免顶点重复计数
    if (a.lat > p.lat !== b.lat > p.lat) {
      const lngAtLat =
        a.lng + ((p.lat - a.lat) * (b.lng - a.lng)) / (b.lat - a.lat);
      if (lngAtLat > p.lng) inside = !inside;
    }
  }
  return inside;
}

/** 风向数法：返回点相对环的环绕次数（non-zero 规则），不含边界判定 */
export function windingNumber(p: LngLat, ring: Ring): number {
  let wn = 0;
  for (const [a, b] of edges(ring)) {
    if (a.lat <= p.lat) {
      // 向上穿越且 P 在边左侧
      if (b.lat > p.lat && cross(a, b, p) > 0) wn++;
    } else if (b.lat <= p.lat && cross(a, b, p) < 0) {
      // 向下穿越且 P 在边右侧
      wn--;
    }
  }
  return wn;
}

/** 单环分类：inside / outside / boundary */
export function classifyPointInRing(
  p: LngLat,
  ring: Ring,
  rule: FillRule = 'evenodd',
  boundaryTolerance = 0,
): Classification {
  for (const [a, b] of edges(ring)) {
    if (isOnSegment(p, a, b, boundaryTolerance)) return 'boundary';
  }
  if (rule === 'nonzero') {
    return windingNumber(p, ring) !== 0 ? 'inside' : 'outside';
  }
  return rayCastContains(p, ring) ? 'inside' : 'outside';
}

/**
 * 带孔洞多边形分类。
 * 拓扑定义：
 *  - 位于任一环的边上          -> boundary
 *  - 在外环内且不在任何孔洞内  -> inside（实体部分）
 *  - 在外环外，或在某孔洞内部  -> outside（含中空孔洞部分）
 */
export function classifyPoint(
  p: LngLat,
  polygon: GeoFencePolygon,
  options: ClassifyOptions = {},
): Classification {
  const rule = options.rule ?? 'evenodd';
  const eps = options.boundaryTolerance ?? 0;

  const outerResult = classifyPointInRing(p, polygon.outer, rule, eps);
  if (outerResult !== 'inside') return outerResult;

  for (const hole of polygon.holes ?? []) {
    const holeResult = classifyPointInRing(p, hole, rule, eps);
    if (holeResult === 'boundary') return 'boundary';
    if (holeResult === 'inside') return 'outside';
  }
  return 'inside';
}

/** 便捷布尔判定：点是否处于围栏生效区域（实体部分，可选含边界） */
export function contains(
  p: LngLat,
  polygon: GeoFencePolygon,
  options: ContainsOptions = {},
): boolean {
  const result = classifyPoint(p, polygon, options);
  if (result === 'inside') return true;
  if (result === 'boundary') return options.includeBoundary ?? true;
  return false;
}
