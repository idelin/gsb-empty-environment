/** 3D axis-aligned bounding box. */
export interface AABB {
  minX: number;
  minY: number;
  minZ: number;
  maxX: number;
  maxY: number;
  maxZ: number;
}

export function makeAABB(
  minX: number, minY: number, minZ: number,
  maxX: number, maxY: number, maxZ: number,
): AABB {
  return { minX, minY, minZ, maxX, maxY, maxZ };
}

export function cloneAABB(a: AABB): AABB {
  return { minX: a.minX, minY: a.minY, minZ: a.minZ, maxX: a.maxX, maxY: a.maxY, maxZ: a.maxZ };
}

export function overlaps(a: AABB, b: AABB): boolean {
  return (
    a.minX <= b.maxX && a.maxX >= b.minX &&
    a.minY <= b.maxY && a.maxY >= b.minY &&
    a.minZ <= b.maxZ && a.maxZ >= b.minZ
  );
}

export function contains(outer: AABB, inner: AABB): boolean {
  return (
    outer.minX <= inner.minX && outer.minY <= inner.minY && outer.minZ <= inner.minZ &&
    outer.maxX >= inner.maxX && outer.maxY >= inner.maxY && outer.maxZ >= inner.maxZ
  );
}

/** Surface area heuristic cost of an AABB (surface area in 3D). */
export function surfaceArea(a: AABB): number {
  const dx = a.maxX - a.minX;
  const dy = a.maxY - a.minY;
  const dz = a.maxZ - a.minZ;
  return 2 * (dx * dy + dy * dz + dz * dx);
}

export function perimeter(a: AABB): number {
  const dx = a.maxX - a.minX;
  const dy = a.maxY - a.minY;
  const dz = a.maxZ - a.minZ;
  return 2 * (dx + dy + dz);
}

export function unionInto(a: AABB, b: AABB): AABB {
  a.minX = Math.min(a.minX, b.minX);
  a.minY = Math.min(a.minY, b.minY);
  a.minZ = Math.min(a.minZ, b.minZ);
  a.maxX = Math.max(a.maxX, b.maxX);
  a.maxY = Math.max(a.maxY, b.maxY);
  a.maxZ = Math.max(a.maxZ, b.maxZ);
  return a;
}

export function unionOf(a: AABB, b: AABB): AABB {
  return {
    minX: Math.min(a.minX, b.minX),
    minY: Math.min(a.minY, b.minY),
    minZ: Math.min(a.minZ, b.minZ),
    maxX: Math.max(a.maxX, b.maxX),
    maxY: Math.max(a.maxY, b.maxY),
    maxZ: Math.max(a.maxZ, b.maxZ),
  };
}

/** Fatten an AABB by an absolute margin on every side. */
export function fatten(a: AABB, margin: number): AABB {
  return {
    minX: a.minX - margin,
    minY: a.minY - margin,
    minZ: a.minZ - margin,
    maxX: a.maxX + margin,
    maxY: a.maxY + margin,
    maxZ: a.maxZ + margin,
  };
}
