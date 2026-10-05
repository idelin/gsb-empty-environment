/**
 * Structuring element: a set of sample offsets relative to its origin.
 *
 * Semantics (grayscale, foreground = high values):
 *   dilation  out(x) = max over (dx,dy) in offsets of in(x - dx, y - dy)
 *   erosion   out(x) = min over (dx,dy) in offsets of in(x + dx, y + dy)
 *
 * i.e. dilation "stamps" the drawn shape (as given, relative to the origin)
 * onto every foreground pixel; erosion keeps exactly the positions where the
 * stamped shape fits entirely inside the foreground. Direction of
 * non-symmetric shapes is therefore significant and preserved.
 */
export interface StructuringElement {
  readonly offsets: ReadonlyArray<readonly [number, number]>;
}

export function makeSE(
  points: ReadonlyArray<readonly [number, number]>,
  origin: readonly [number, number] = [0, 0],
): StructuringElement {
  if (points.length === 0) throw new Error('structuring element must be non-empty');
  const offsets = points.map(([px, py]) => [px - origin[0], py - origin[1]] as const);
  return { offsets };
}

/** 3x3 square, origin at center. */
export const SQUARE3: StructuringElement = makeSE([
  [-1, -1], [0, -1], [1, -1],
  [-1, 0], [0, 0], [1, 0],
  [-1, 1], [0, 1], [1, 1],
]);

/** 4-connectivity cross, origin at center. */
export const CROSS: StructuringElement = makeSE([
  [0, -1], [-1, 0], [0, 0], [1, 0], [0, 1],
]);

/** Horizontal 3-tap line, origin at the left end (non-symmetric). */
export const LINE3_RIGHT: StructuringElement = makeSE([[0, 0], [1, 0], [2, 0]]);

/** Chebyshev radius of the offsets, used for dirty-region expansion. */
export function seRadius(se: StructuringElement): { rx: number; ry: number } {
  let rx = 0;
  let ry = 0;
  for (const [dx, dy] of se.offsets) {
    if (Math.abs(dx) > rx) rx = Math.abs(dx);
    if (Math.abs(dy) > ry) ry = Math.abs(dy);
  }
  return { rx, ry };
}
