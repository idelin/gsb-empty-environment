/**
 * Self-built RGBA test image (4x4, float64, no Canvas / image libraries).
 * Covers: black, white, primaries, secondaries, grays, a transparent pixel,
 * a semi-transparent pixel, near-black/white, and one out-of-range pixel
 * (1.2 / -0.2) to exercise clipping and negative linear components.
 */
export interface TestImage {
  width: number;
  height: number;
  data: Float64Array;
}

export function makeTestImage(): TestImage {
  const px = [
    0, 0, 0, 1,
    1, 1, 1, 1,
    1, 0, 0, 1,
    0, 1, 0, 1,
    0, 0, 1, 1,
    0.5, 0.5, 0.5, 1,
    0.25, 0.75, 0.5, 1,
    1, 1, 0, 1,
    0, 1, 1, 1,
    1, 0, 1, 1,
    0.5, 0.5, 0.5, 0,
    1, 1, 1, 0.5,
    1.2, 0.5, -0.2, 1,
    0.9, 0.1, 0.3, 1,
    0.02, 0.02, 0.02, 1,
    0.98, 0.97, 0.96, 1,
  ];
  return { width: 4, height: 4, data: Float64Array.from(px) };
}
