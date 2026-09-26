import { colorKey, distinctOpaqueColors, type RgbaImage } from "./image.ts";
import type { PaletteResult, Rgb } from "./types.ts";
import { nearestIndex, paletteFingerprint, squaredDistance, weightedCentroid } from "./color.ts";

/**
 * Choose a fixed-size opaque palette.
 *
 * - Transparent pixels never contribute and never consume a palette slot.
 * - When the number of distinct opaque colors is small (<= maxExactColors)
 *   the provably minimal SSE is computed exactly by weighted set-partition
 *   enumeration; this is the benchmark.
 * - Otherwise a deterministic median-cut heuristic is used. When the exact
 *   benchmark is also affordable it is computed and the relative gap is
 *   reported; the result is never claimed optimal.
 *
 * colorCount must be >= 1; with one color the result is a single weighted
 * mean color with a fully defined behavior.
 */
export interface BuildPaletteOptions {
  colorCount: number;
  /** Distinct-color ceiling for exact solving. Default 10. */
  maxExactColors?: number;
  /**
   * Force the heuristic even when exact solving is affordable. The exact
   * benchmark is still computed for gap reporting.
   */
  forceHeuristic?: boolean;
}

export function buildPalette(image: RgbaImage, options: BuildPaletteOptions): PaletteResult {
  const colorCount = options.colorCount;
  if (!Number.isInteger(colorCount) || colorCount < 1) {
    throw new Error(`colorCount must be an integer >= 1, got ${String(colorCount)}`);
  }
  const maxExact = options.maxExactColors ?? 10;
  const entries = distinctOpaqueColors(image);
  const m = entries.length;
  const colors = entries.map((e) => e.color);
  const weights = entries.map((e) => e.weight);

  const fillToCount = (palette: Rgb[]): Rgb[] => padPalette(palette, colorCount);

  if (m === 0) {
    // No opaque pixels: opaque budget exists but is unused.
    const palette = fillToCount([]);
    return {
      palette,
      sse: 0,
      method: "exact",
      certifiedBest: true,
      gapVsExact: null,
      exactSse: 0,
      distinctOpaqueColors: 0,
      fingerprint: paletteFingerprint(palette),
    };
  }

  if (m <= colorCount) {
    // Every distinct color keeps its own slot: zero error, trivially optimal.
    const palette = fillToCount([...colors].sort((a, b) => colorKey(a) - colorKey(b)));
    return {
      palette,
      sse: 0,
      method: "exact",
      certifiedBest: true,
      gapVsExact: null,
      exactSse: 0,
      distinctOpaqueColors: m,
      fingerprint: paletteFingerprint(palette),
    };
  }

  const canExact = m <= maxExact;
  let exact: { palette: Rgb[]; sse: number } | null = null;
  if (canExact) {
    exact = solveExact(colors, weights, colorCount);
  }

  if (canExact && !options.forceHeuristic) {
    const palette = fillToCount(exact!.palette);
    return {
      palette,
      sse: exact!.sse,
      method: "exact",
      certifiedBest: true,
      gapVsExact: null,
      exactSse: exact!.sse,
      distinctOpaqueColors: m,
      fingerprint: paletteFingerprint(palette),
    };
  }

  const heuristic = medianCut(colors, weights, colorCount);
  const palette = fillToCount(heuristic);
  const sse = paletteSse(image, palette);
  return {
    palette,
    sse,
    method: "median-cut",
    certifiedBest: false,
    gapVsExact: exact ? (sse - exact.sse) / (exact.sse || 1) : null,
    exactSse: exact ? exact.sse : null,
    distinctOpaqueColors: m,
    fingerprint: paletteFingerprint(palette),
  };
}

/**
 * Deterministic median cut: split the box with the largest weighted channel
 * range along its widest channel at the weighted median; repeat until the
 * requested number of boxes exists, then take weighted centroids.
 */
export function medianCut(
  colors: readonly Rgb[],
  weights: readonly number[],
  colorCount: number
): Rgb[] {
  const indexed = colors.map((c, i) => ({ c, w: weights[i]!, i }));
  let boxes: typeof indexed[] = [indexed];

  while (boxes.length < colorCount) {
    // Choose the box with the greatest channel range; tie -> earliest box.
    let chosen = -1;
    let chosenRange = -1;
    let chosenChannel = -1;
    for (let bi = 0; bi < boxes.length; bi++) {
      const box = boxes[bi]!;
      if (box.length < 2) continue;
      for (let ch = 0; ch < 3; ch++) {
        let lo = 255;
        let hi = 0;
        for (const item of box) {
          const v = item.c[ch];
          if (v < lo) lo = v;
          if (v > hi) hi = v;
        }
        const range = hi - lo;
        if (range > chosenRange) {
          chosenRange = range;
          chosenChannel = ch;
          chosen = bi;
        }
      }
    }
    if (chosen < 0) break; // no box is splittable

    const ch = chosenChannel;
    const box = boxes[chosen]!;
    const sorted = [...box].sort((a, b) => {
      const d = a.c[ch] - b.c[ch];
      return d !== 0 ? d : colorKey(a.c) - colorKey(b.c);
    });
    const totalWeight = sorted.reduce((s, item) => s + item.w, 0);
    const half = Math.floor(totalWeight / 2);
    let acc = 0;
    let splitAt = 1;
    for (let i = 0; i < sorted.length - 1; i++) {
      acc += sorted[i]!.w;
      if (acc <= half) {
        splitAt = i + 1;
      } else {
        break;
      }
    }
    if (splitAt < 1) splitAt = 1;
    if (splitAt >= sorted.length) splitAt = sorted.length - 1;

    const left = sorted.slice(0, splitAt);
    const right = sorted.slice(splitAt);
    boxes = boxes.filter((_, bi) => bi !== chosen);
    boxes.push(left, right);
  }

  const centroids = boxes.map((box) =>
    weightedCentroid(
      box.map((item) => item.c),
      box.map((item) => item.w)
    )
  );

  // Deterministic de-duplication of coincident centroids.
  const unique: Rgb[] = [];
  for (const c of centroids.sort((a, b) => colorKey(a) - colorKey(b))) {
    if (!unique.some((u) => u[0] === c[0] && u[1] === c[1] && u[2] === c[2])) {
      unique.push(c);
    }
  }
  return unique;
}

/**
 * Exact weighted k-means SSE over integer palette colors, found by
 * enumerating set partitions (restricted-growth strings) of the distinct
 * opaque colors. For each block the optimal representative over candidate
 * integer centers (every distinct source color plus every rounded subset
 * mean, precomputed per subset) is tested exhaustively.
 */
export function solveExact(
  colors: readonly Rgb[],
  weights: readonly number[],
  colorCount: number
): { palette: Rgb[]; sse: number } {
  const m = colors.length;
  const k = Math.min(colorCount, m);
  const size = 1 << m;

  // Per-subset total weight and weighted sums.
  const subWeight = new Int32Array(size);
  const sumR = new Float64Array(size);
  const sumG = new Float64Array(size);
  const sumB = new Float64Array(size);
  for (let mask = 1; mask < size; mask++) {
    const bit = 31 - Math.clz32(mask & -mask);
    const rest = mask & (mask - 1);
    subWeight[mask] = subWeight[rest] + weights[bit]!;
    sumR[mask] = sumR[rest] + colors[bit]![0] * weights[bit]!;
    sumG[mask] = sumG[rest] + colors[bit]![1] * weights[bit]!;
    sumB[mask] = sumB[rest] + colors[bit]![2] * weights[bit]!;
  }

  // Candidate centers: all source colors and all rounded subset means.
  const candidates: Rgb[] = [];
  const candKey = new Set<number>();
  const addCandidate = (c: Rgb) => {
    const key = colorKey(c);
    if (!candKey.has(key)) {
      candKey.add(key);
      candidates.push(c);
    }
  };
  for (const c of colors) addCandidate(c);
  for (let mask = 1; mask < size; mask++) {
    const w = subWeight[mask]!;
    addCandidate([
      Math.floor(sumR[mask]! / w + 0.5),
      Math.floor(sumG[mask]! / w + 0.5),
      Math.floor(sumB[mask]! / w + 0.5),
    ]);
  }

  // Best SSE and best center for every non-empty subset.
  const subCost = new Float64Array(size);
  const subCenter: Rgb[] = new Array(size);
  for (let mask = 1; mask < size; mask++) {
    let best = Infinity;
    let bestCenter: Rgb = candidates[0]!;
    for (const cand of candidates) {
      let sse = 0;
      for (let i = 0; i < m; i++) {
        if (mask & (1 << i)) {
          sse += weights[i]! * squaredDistance(colors[i]!, cand);
        }
      }
      if (sse < best) {
        best = sse;
        bestCenter = cand;
      }
    }
    subCost[mask] = best;
    subCenter[mask] = bestCenter;
  }

  // Enumerate partitions into exactly k non-empty blocks (restricted-growth
  // strings), tracking each block's best SSE incrementally with pruning.
  const labels = new Int32Array(m);
  let bestSse = Infinity;
  let bestLabels = new Int32Array(m);
  const blockCosts = new Float64Array(k);
  const blockMasks = new Int32Array(k);
  const enumerateTracked = (pos: number, maxLabel: number, usedBlocks: number) => {
    if (pos === m) {
      if (usedBlocks === k) {
        let total = 0;
        for (let b = 0; b < k; b++) total += blockCosts[b]!;
        if (total < bestSse) {
          bestSse = total;
          bestLabels = Int32Array.from(labels);
        }
      }
      return;
    }
    const remaining = m - pos;
    const upper = Math.min(maxLabel + 1, k - 1);
    for (let label = 0; label <= upper; label++) {
      const newBlocks = usedBlocks + (label > maxLabel ? 1 : 0);
      if (newBlocks > k) continue;
      if (newBlocks + remaining - 1 < k) continue;
      labels[pos] = label;
      const prevMask = blockMasks[label]!;
      const newMask = prevMask | (1 << pos);
      const prevCost = blockCosts[label]!;
      blockMasks[label] = newMask;
      blockCosts[label] = subCost[newMask]!;
      let total = 0;
      for (let b = 0; b < k; b++) total += blockCosts[b]!;
      if (total < bestSse) {
        enumerateTracked(pos + 1, Math.max(maxLabel, label), newBlocks);
      }
      blockMasks[label] = prevMask;
      blockCosts[label] = prevCost;
    }
  };
  enumerateTracked(0, -1, 0);

  const palette: Rgb[] = [];
  for (let b = 0; b < k; b++) {
    let mask = 0;
    for (let i = 0; i < m; i++) {
      if (bestLabels[i] === b) mask |= 1 << i;
    }
    palette.push(subCenter[mask]!);
  }
  palette.sort((a, b) => colorKey(a) - colorKey(b));
  return { palette, sse: bestSse };
}

export function paletteSse(image: RgbaImage, palette: readonly Rgb[]): number {
  let sse = 0;
  const n = image.width * image.height;
  for (let i = 0; i < n; i++) {
    const o = i * 4;
    if (image.data[o + 3]! <= 128) continue;
    const idx = nearestIndex(image.data[o]!, image.data[o + 1]!, image.data[o + 2]!, palette);
    sse += squaredDistance(
      [image.data[o]!, image.data[o + 1]!, image.data[o + 2]!],
      palette[idx]!
    );
  }
  return sse;
}

/**
 * Extend a palette to exactly colorCount entries by repeating the last color
 * deterministically. Behavior for colorCount = 1 with no opaque source is a
 * fixed black.
 */
function padPalette(palette: Rgb[], colorCount: number): Rgb[] {
  const sorted = [...palette].sort((a, b) => colorKey(a) - colorKey(b));
  const out: Rgb[] = [];
  for (let i = 0; i < colorCount; i++) {
    if (i < sorted.length) {
      out.push(sorted[i]!);
    } else if (sorted.length > 0) {
      out.push(sorted[sorted.length - 1]!);
    } else {
      out.push([0, 0, 0]);
    }
  }
  return out;
}
