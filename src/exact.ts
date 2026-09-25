/**
 * Exact minimum-quantization-error baseline for small color domains.
 *
 * For a fixed partition of the weighted color points into clusters, the
 * squared-error-optimal palette colors are the cluster centroids. So the
 * true global optimum is found by enumerating every set partition of the
 * distinct opaque colors into at most K nonempty clusters (restricted
 * growth strings, lexicographic order) and scoring each by its weighted
 * within-cluster sum of squared distances in the given color space.
 *
 * The resulting value is a genuine lower bound for ANY palette whose
 * colors are represented in the same space (centroids are allowed to be
 * fractional; rounding them to u8 can only increase error).
 *
 * Enumeration cost is (sum of Stirling numbers S(N,j), j=1..K) * N * K;
 * the caller bounds it via `maxPartitions`. If the domain is too large,
 * `null` is returned and no baseline claim is made at all.
 */

import type { ColorSpace } from "./colorspace.ts";
import type { Palette, RGB, RGBAImage } from "./types.ts";

export interface ExactBaseline {
  /** Minimum total weighted squared error over all <=K-cluster partitions. */
  error: number;
  /** u8-rounded centroids of the optimal clusters. */
  palette: Palette;
  distinctColors: number;
  /** Number of set partitions actually enumerated. */
  partitions: number;
}

interface WeightedColor {
  rgb: RGB;
  weight: number;
  point: [number, number, number];
}

/** Distinct opaque colors with pixel counts, in first-occurrence order. */
export function distinctOpaqueColors(image: RGBAImage, space: ColorSpace): WeightedColor[] {
  const map = new Map<number, WeightedColor>();
  const { data } = image;
  for (let i = 0; i < data.length; i += 4) {
    if (data[i + 3] === 0) continue;
    const key = (data[i] << 16) | (data[i + 1] << 8) | data[i + 2];
    const hit = map.get(key);
    if (hit) {
      hit.weight += 1;
    } else {
      const rgb: RGB = [data[i], data[i + 1], data[i + 2]];
      map.set(key, { rgb, weight: 1, point: space.toSpace(rgb[0], rgb[1], rgb[2]) });
    }
  }
  return [...map.values()];
}

/** Number of set partitions of n elements into at most k nonempty blocks. */
function partitionCount(n: number, k: number, limit: number): number {
  // Stirling numbers of the second kind S(n, j); cap at Infinity.
  let prev = new Float64Array(n + 1);
  let cur = new Float64Array(n + 1);
  prev[1] = 1; // S(1, 1)
  let total = 1;
  for (let i = 2; i <= n; i++) {
    cur.fill(0);
    for (let j = 1; j <= Math.min(i, k); j++) {
      cur[j] = prev[j - 1] + j * prev[j];
      if (cur[j] > limit) cur[j] = Infinity;
    }
    [prev, cur] = [cur, prev];
  }
  for (let j = 1; j <= Math.min(n, k); j++) total += prev[j] - (j === 1 ? 1 : 0);
  return total;
}

export function exactBaseline(
  image: RGBAImage,
  colorCount: number,
  space: ColorSpace,
  maxPartitions = 100_000,
): ExactBaseline | null {
  const distinct = distinctOpaqueColors(image, space);
  const n = distinct.length;
  if (n === 0) {
    return { error: 0, palette: { colors: [] }, distinctColors: 0, partitions: 0 };
  }
  const k = Math.min(colorCount, n);
  if (k >= n) {
    return {
      error: 0,
      palette: { colors: distinct.map((d) => d.rgb.slice() as RGB) },
      distinctColors: n,
      partitions: 1,
    };
  }
  // Exact u8 centroids are only guaranteed in the native u8 space. For any
  // other space the continuous centroid would need an approximate mapping
  // back to u8, so no (exact) baseline is reported.
  if (space.version !== "srgb-u8-v1") {
    return null;
  }
  const count = partitionCount(n, k, maxPartitions);
  if (count > maxPartitions) {
    return null; // Domain too large: no baseline, no claim.
  }

  // Per-cluster accumulators over the restricted-growth-string walk.
  const assign = new Int32Array(n);
  const wSum = new Float64Array(k);
  const sx = new Float64Array(k);
  const sy = new Float64Array(k);
  const sz = new Float64Array(k);
  const ssq = new Float64Array(k); // sum of w_i * ||x_i||^2

  let bestError = Infinity;
  let bestAssign: Int32Array | null = null;
  let usedClusters = 1;

  const addPoint = (i: number, c: number): void => {
    const w = distinct[i].weight;
    const p = distinct[i].point;
    wSum[c] += w;
    sx[c] += w * p[0];
    sy[c] += w * p[1];
    sz[c] += w * p[2];
    ssq[c] += w * space.dist2(p, [0, 0, 0]);
  };

  const removePoint = (i: number, c: number): void => {
    const w = distinct[i].weight;
    const p = distinct[i].point;
    wSum[c] -= w;
    sx[c] -= w * p[0];
    sy[c] -= w * p[1];
    sz[c] -= w * p[2];
    ssq[c] -= w * space.dist2(p, [0, 0, 0]);
  };

  const score = (): number => {
    let total = 0;
    for (let c = 0; c < usedClusters; c++) {
      total += ssq[c] - (sx[c] * sx[c] + sy[c] * sy[c] + sz[c] * sz[c]) / wSum[c];
    }
    return total;
  };

  const walk = (i: number): void => {
    if (i === n) {
      const e = score();
      if (e < bestError - 1e-9) {
        bestError = e;
        bestAssign = Int32Array.from(assign);
      }
      return;
    }
    // Point i may join an existing cluster or open the next new one.
    const maxExisting = Math.min(usedClusters, k);
    for (let c = 0; c < maxExisting; c++) {
      assign[i] = c;
      addPoint(i, c);
      walk(i + 1);
      removePoint(i, c);
    }
    if (usedClusters < k) {
      const c = usedClusters;
      assign[i] = c;
      usedClusters++;
      addPoint(i, c);
      walk(i + 1);
      removePoint(i, c);
      usedClusters--;
    }
  };

  addPoint(0, 0);
  walk(1);

  // Rebuild the winning centroids as u8 colors.
  const sums = new Array<{ w: number; x: number; y: number; z: number }>(k);
  for (let c = 0; c < k; c++) sums[c] = { w: 0, x: 0, y: 0, z: 0 };
  for (let i = 0; i < n; i++) {
    const c = bestAssign![i];
    const w = distinct[i].weight;
    const p = distinct[i].point;
    sums[c].w += w;
    sums[c].x += w * p[0];
    sums[c].y += w * p[1];
    sums[c].z += w * p[2];
  }

  // In SRGB_U8 the working coordinates ARE the u8 coordinates; in other
  // spaces the optimal centroid lives in the working space and is mapped
  // to the nearest u8 color representable at the 8-bit level.
  const colors: RGB[] = [];
  for (let c = 0; c < k; c++) {
    if (sums[c].w === 0) continue;
    colors.push([
      Math.round(sums[c].x / sums[c].w),
      Math.round(sums[c].y / sums[c].w),
      Math.round(sums[c].z / sums[c].w),
    ]);
  }

  return {
    error: Math.max(0, bestError),
    palette: { colors },
    distinctColors: n,
    partitions: count,
  };
}
