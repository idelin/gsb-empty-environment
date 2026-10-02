import type { ItemSpec } from './types.ts';

export interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

/** Half of the spacing charged to each side of an item (rounded up). */
export function spacingPad(spacing: number): number {
  return Math.ceil(spacing / 2);
}

export function itemMargin(item: ItemSpec): number {
  return item.margin ?? 0;
}

export function itemRotatable(item: ItemSpec): boolean {
  return item.rotatable ?? true;
}

/**
 * Footprint size of an item in "packing space": the sprite plus its own
 * margin and its half of the inter-item spacing on every side. Capacity is
 * accounted in this space, so spacing and margins are always charged.
 */
export function footprintSize(
  item: ItemSpec,
  spacing: number,
): { fw: number; fh: number } {
  const grow = itemMargin(item) + spacingPad(spacing);
  return { fw: item.width + 2 * grow, fh: item.height + 2 * grow };
}

/** Top-left of the actual sprite given the footprint origin. */
export function spriteOrigin(
  fx: number,
  fy: number,
  item: ItemSpec,
  spacing: number,
): { x: number; y: number } {
  const off = itemMargin(item) + spacingPad(spacing);
  return { x: fx + off, y: fy + off };
}

export function rectsIntersect(a: Rect, b: Rect): boolean {
  return a.x < b.x + b.w && a.y < b.y + b.h && b.x < a.x + a.w && b.y < a.y + a.h;
}

export function rectContainsRect(outer: Rect, inner: Rect): boolean {
  return (
    inner.x >= outer.x &&
    inner.y >= outer.y &&
    inner.x + inner.w <= outer.x + outer.w &&
    inner.y + inner.h <= outer.y + outer.h
  );
}

/** Deterministic total order used everywhere ties must be broken. */
export function compareIds(a: string, b: string): number {
  return a < b ? -1 : a > b ? 1 : 0;
}

/** Deterministic packing order: largest max-side, then area, then id. */
export function packingOrder(items: ItemSpec[], spacing: number): ItemSpec[] {
  const decorated = items.map((item) => {
    const { fw, fh } = footprintSize(item, spacing);
    return { item, fw, fh };
  });
  decorated.sort((a, b) => {
    const maxA = Math.max(a.fw, a.fh);
    const maxB = Math.max(b.fw, b.fh);
    if (maxA !== maxB) return maxB - maxA;
    const areaA = a.fw * a.fh;
    const areaB = b.fw * b.fh;
    if (areaA !== areaB) return areaB - areaA;
    return compareIds(a.item.id, b.item.id);
  });
  return decorated.map((d) => d.item);
}
