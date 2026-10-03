/** 预置围栏构造器：星型外环 + 居中岛屿孔洞。 */
import type { GeoFence, LngLat, Ring } from './geofence.ts';

export interface StarFenceOptions {
  id?: string;
  weight?: number;
  /** 星角数量，默认 5。 */
  points?: number;
  /** 星尖外半径（度）。 */
  outerRadius: number;
  /** 星凹口内半径（度）。 */
  innerRadius: number;
  /** 居中方形岛屿孔洞的半边长（度）。 */
  holeHalfSize: number;
}

/**
 * 构造星型围栏：外环为 points 角星型（2*points 个顶点的简单凹多边形），
 * 中心镂空一个轴对齐方形岛屿。内半径须大于 holeHalfSize * sqrt(2)，
 * 保证岛屿完整落在实体区域内。
 */
export function buildStarFence(center: LngLat, opts: StarFenceOptions): GeoFence {
  const points = opts.points ?? 5;
  if (points < 2) throw new Error('星角数量至少为 2');
  if (!(opts.innerRadius > opts.holeHalfSize * Math.SQRT2)) {
    throw new Error('岛屿孔洞超出星型内凹区域，无法保证孔洞被实体包围');
  }
  const [cx, cy] = center;
  const outer: Ring = [];
  for (let k = 0; k < 2 * points; k++) {
    const radius = k % 2 === 0 ? opts.outerRadius : opts.innerRadius;
    const angle = Math.PI / 2 + (k * Math.PI) / points; // 从正上方起，逆时针
    outer.push([cx + radius * Math.cos(angle), cy + radius * Math.sin(angle)]);
  }
  const h = opts.holeHalfSize;
  const hole: Ring = [
    [cx - h, cy - h],
    [cx + h, cy - h],
    [cx + h, cy + h],
    [cx - h, cy + h],
  ];
  return {
    id: opts.id ?? 'star-fence',
    weight: opts.weight ?? 1,
    outer,
    holes: [hole],
    metadata: { center, ...opts },
  };
}
