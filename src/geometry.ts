import type { Rect, RelativeDirection } from './types.ts';

export interface GeometryResult {
  direction: RelativeDirection;
  /** Edge-to-edge gap on the x axis (0 when the x ranges intersect). */
  gapX: number;
  /** Edge-to-edge gap on the y axis (0 when the y ranges intersect). */
  gapY: number;
  /** Center-to-center delta on x, signed (control - label). */
  deltaX: number;
  /** Center-to-center delta on y, signed (control - label). */
  deltaY: number;
  rawDistance: number;
}

export function centerX(r: Rect): number {
  return r.x + r.width / 2;
}

export function centerY(r: Rect): number {
  return r.y + r.height / 2;
}

/** Strict interval intersection: touching edges do not count as overlap. */
function rangesOverlap(aStart: number, aEnd: number, bStart: number, bEnd: number): boolean {
  return aStart < bEnd && bStart < aEnd;
}

function axisGap(
  aStart: number,
  aEnd: number,
  bStart: number,
  bEnd: number,
): number {
  if (rangesOverlap(aStart, aEnd, bStart, bEnd)) {
    return 0;
  }
  return bStart >= aEnd ? bStart - aEnd : aStart - bEnd;
}

/**
 * Describe where `control` sits relative to `label` and compute the raw
 * directional distance used by the cost function.
 */
export function relativeGeometry(label: Rect, control: Rect): GeometryResult {
  const overlapX = rangesOverlap(
    label.x,
    label.x + label.width,
    control.x,
    control.x + control.width,
  );
  const overlapY = rangesOverlap(
    label.y,
    label.y + label.height,
    control.y,
    control.y + control.height,
  );
  const gapX = axisGap(
    label.x,
    label.x + label.width,
    control.x,
    control.x + control.width,
  );
  const gapY = axisGap(
    label.y,
    label.y + label.height,
    control.y,
    control.y + control.height,
  );
  const deltaX = centerX(control) - centerX(label);
  const deltaY = centerY(control) - centerY(label);

  let direction: RelativeDirection;
  let rawDistance: number;

  if (overlapX && overlapY) {
    direction = 'overlap';
    rawDistance = Math.hypot(deltaX, deltaY);
  } else if (overlapX) {
    direction = deltaY < 0 ? 'above' : 'below';
    rawDistance = gapY + Math.abs(deltaX);
  } else if (overlapY) {
    direction = deltaX < 0 ? 'left' : 'right';
    rawDistance = gapX + Math.abs(deltaY);
  } else {
    // Diagonal placement: classify by the dominant separation axis.
    if (gapX >= gapY) {
      direction = deltaX < 0 ? 'left' : 'right';
    } else {
      direction = deltaY < 0 ? 'above' : 'below';
    }
    rawDistance = Math.hypot(gapX, gapY);
  }

  return { direction, gapX, gapY, deltaX, deltaY, rawDistance };
}
