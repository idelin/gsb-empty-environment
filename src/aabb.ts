/**
 * 3D 轴对齐包围盒（AABB）基础类型与运算。
 */
export interface AABB {
  minX: number;
  minY: number;
  minZ: number;
  maxX: number;
  maxY: number;
  maxZ: number;
}

/** 两个 AABB 是否相交（边界相触视为相交）。 */
export function overlaps(a: AABB, b: AABB): boolean {
  return (
    a.minX <= b.maxX && a.maxX >= b.minX &&
    a.minY <= b.maxY && a.maxY >= b.minY &&
    a.minZ <= b.maxZ && a.maxZ >= b.minZ
  );
}

/** outer 是否完整包含 inner。 */
export function contains(outer: AABB, inner: AABB): boolean {
  return (
    outer.minX <= inner.minX && outer.maxX >= inner.maxX &&
    outer.minY <= inner.minY && outer.maxY >= inner.maxY &&
    outer.minZ <= inner.minZ && outer.maxZ >= inner.maxZ
  );
}

/** 返回 a 与 b 的最小包围盒（新对象）。 */
export function union(a: AABB, b: AABB): AABB {
  return {
    minX: Math.min(a.minX, b.minX),
    minY: Math.min(a.minY, b.minY),
    minZ: Math.min(a.minZ, b.minZ),
    maxX: Math.max(a.maxX, b.maxX),
    maxY: Math.max(a.maxY, b.maxY),
    maxZ: Math.max(a.maxZ, b.maxZ),
  };
}

/** 各轴向四周扩张 margin 后的新 AABB。 */
export function expanded(a: AABB, margin: number): AABB {
  return {
    minX: a.minX - margin,
    minY: a.minY - margin,
    minZ: a.minZ - margin,
    maxX: a.maxX + margin,
    maxY: a.maxY + margin,
    maxZ: a.maxZ + margin,
  };
}

/** 表面积（SAH 启发式代价函数使用）。 */
export function surfaceArea(a: AABB): number {
  const dx = a.maxX - a.minX;
  const dy = a.maxY - a.minY;
  const dz = a.maxZ - a.minZ;
  return 2 * (dx * dy + dy * dz + dz * dx);
}

/** a 与 b 联合包围盒的表面积（不分配新对象，用于热路径代价计算）。 */
export function unionSurfaceArea(a: AABB, b: AABB): number {
  const dx = Math.max(a.maxX, b.maxX) - Math.min(a.minX, b.minX);
  const dy = Math.max(a.maxY, b.maxY) - Math.min(a.minY, b.minY);
  const dz = Math.max(a.maxZ, b.maxZ) - Math.min(a.minZ, b.minZ);
  return 2 * (dx * dy + dy * dz + dz * dx);
}

/** 将 a 与 b 的联合包围盒写入 out（不分配新对象）。 */
export function unionInto(out: AABB, a: AABB, b: AABB): void {
  out.minX = Math.min(a.minX, b.minX);
  out.minY = Math.min(a.minY, b.minY);
  out.minZ = Math.min(a.minZ, b.minZ);
  out.maxX = Math.max(a.maxX, b.maxX);
  out.maxY = Math.max(a.maxY, b.maxY);
  out.maxZ = Math.max(a.maxZ, b.maxZ);
}

/** 深拷贝。 */
export function cloneAabb(a: AABB): AABB {
  return { minX: a.minX, minY: a.minY, minZ: a.minZ, maxX: a.maxX, maxY: a.maxY, maxZ: a.maxZ };
}
