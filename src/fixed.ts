/** 16.16 fixed-point screen coordinates. */
export const FP_SHIFT = 16;
export const FP_ONE = 1 << FP_SHIFT; // 65536
export const FP_MIN = -2147483648; // -2^31
export const FP_MAX = 2147483647; //  2^31 - 1

/**
 * Convert a pixel coordinate to 16.16 fixed point.
 * Out-of-range values saturate (overflow is explicit, never wraps).
 * NaN propagates so callers can drop invalid geometry.
 */
export function toFixed(value: number): number {
  const scaled = Math.round(value * FP_ONE);
  if (scaled > FP_MAX) return FP_MAX;
  if (scaled < FP_MIN) return FP_MIN;
  return scaled;
}
