import type { QuantMethod } from "./types.ts";

/**
 * Floyd-Steinberg weights in fixed point (denominator 16):
 *
 *        [ ]  7/16
 *   3/16 5/16 1/16
 *
 * All error is kept scaled by 16 in Int32 so identical integer results are
 * produced on every platform and run (no floating-point summation order).
 */
export const FS_SCALE = 16;
export const W_RIGHT = 7;
export const W_DOWN_LEFT = 3;
export const W_DOWN = 5;
export const W_DOWN_RIGHT = 1;

export interface MaskGrid {
  width: number;
  height: number;
  opaque: Uint8Array;
}

export function inBounds(grid: { width: number; height: number }, x: number, y: number): boolean {
  return x >= 0 && y >= 0 && x < grid.width && y < grid.height;
}

/**
 * Error-diffusion delivery gate implementing "no propagation across a
 * transparent barrier".
 *
 * - Horizontal error only reaches an opaque neighbor.
 * - Vertical error reaches an opaque neighbor directly below.
 * - Diagonal error additionally requires the two orthogonal "bridge" cells
 *   to be opaque. This prevents error from leaking diagonally across a
 *   one-pixel transparent wall (horizontal or vertical), while still
 *   diffusing normally through a fully opaque region.
 */
export function canDeliver(
  grid: MaskGrid,
  fromX: number,
  fromY: number,
  toX: number,
  toY: number
): boolean {
  if (!inBounds(grid, toX, toY)) return false;
  const idx = toY * grid.width + toX;
  if (!grid.opaque[idx]) return false;

  const dx = toX - fromX;
  const dy = toY - fromY;
  if (dy === 0) {
    // Only right-going horizontal diffusion exists.
    return dx === 1;
  }
  if (dx === 0) {
    // Straight down (the inBounds check above guaranteed dy === 1).
    return true;
  }
  // Diagonal: both orthogonal bridge cells must be opaque.
  const side = grid.opaque[toY * grid.width + fromX];
  const below = grid.opaque[fromY * grid.width + toX];
  return side === 1 && below === 1;
}

export function methodScale(method: QuantMethod): number {
  return method === "floyd-steinberg" ? FS_SCALE : 1;
}
