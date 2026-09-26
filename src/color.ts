import type { Rgb } from "./types.ts";

/**
 * All quantization math happens in linear sRGB-component space (0..255),
 * matching COLOR_SPACE_VERSION "srgb-rgb-fs-v1". No perceptual transform or
 * external imaging library is used.
 */

export function squaredDistance(a: ArrayLike<number>, b: ArrayLike<number>): number {
  const dr = a[0]! - b[0]!;
  const dg = a[1]! - b[1]!;
  const db = a[2]! - b[2]!;
  return dr * dr + dg * dg + db * db;
}

/** Deterministic nearest-palette index; ties resolve to the smallest index. */
export function nearestIndex(r: number, g: number, b: number, palette: readonly Rgb[]): number {
  let best = 0;
  let bestDist = Infinity;
  for (let i = 0; i < palette.length; i++) {
    const p = palette[i]!;
    const dr = r - p[0];
    const dg = g - p[1];
    const db = b - p[2];
    const d = dr * dr + dg * dg + db * db;
    if (d < bestDist) {
      bestDist = d;
      best = i;
    }
  }
  return best;
}

export function clampChannel(v: number): number {
  if (v < 0) return 0;
  if (v > 255) return 255;
  return v;
}

/**
 * Deterministic rounded division for possibly negative numerators:
 * floor((n + sign(n) * d/2) / d).
 */
export function roundDiv(n: number, d: number): number {
  return n >= 0 ? Math.floor((n + d / 2) / d) : -Math.floor((-n + d / 2) / d);
}

/**
 * Weighted integer centroid of a color set, each component rounded half up.
 * Weights are positive pixel counts.
 */
export function weightedCentroid(
  colors: readonly (readonly [number, number, number])[],
  weights: readonly number[]
): Rgb {
  let wr = 0;
  let wg = 0;
  let wb = 0;
  let w = 0;
  for (let i = 0; i < colors.length; i++) {
    const c = colors[i]!;
    const weight = weights[i]!;
    wr += c[0] * weight;
    wg += c[1] * weight;
    wb += c[2] * weight;
    w += weight;
  }
  return [Math.floor(wr / w + 0.5), Math.floor(wg / w + 0.5), Math.floor(wb / w + 0.5)];
}

export function paletteFingerprint(palette: readonly Rgb[]): string {
  return palette.map((c) => `${c[0]}-${c[1]}-${c[2]}`).join("_");
}
