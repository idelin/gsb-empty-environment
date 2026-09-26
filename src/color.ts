/** RGBA pixel, 4 channels, 0-255 each. Self-built, no Canvas / image libs. */
export type RGBA = readonly [number, number, number, number];

/**
 * Color difference between two RGBA pixels: Euclidean distance in RGBA space.
 * Range: 0 .. sqrt(4 * 255^2) = 510.
 */
export function colorDistance(a: RGBA, b: RGBA): number {
  const dr = a[0] - b[0];
  const dg = a[1] - b[1];
  const db = a[2] - b[2];
  const da = a[3] - b[3];
  return Math.sqrt(dr * dr + dg * dg + db * db + da * da);
}

/** Maximum possible color distance, useful as "infinite-ish" sentinel in tests. */
export const MAX_COLOR_DISTANCE = Math.sqrt(4 * 255 * 255);
