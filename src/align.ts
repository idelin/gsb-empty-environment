/**
 * Exact integer-offset alignment search with multi-resolution lower-bound
 * pruning.
 *
 * Cost model (shared with diff.ts): for offset o, cost(o) =
 *   sum of per-pixel errors over the overlap + uncoveredPenalty * uncovered,
 * where uncovered = |A| + |B| - 2*|overlap| (exact, O(1), because both
 * images are rectangles).
 *
 * Lower bound (admissible — never exceeds the true cost):
 *   Each image is cleaned (transparent -> (0,0,0,0)) and collapsed into a
 *   pyramid of per-channel [min,max] intervals over 2^level x 2^level cells.
 *   For a fine offset o = s*q + r (s = 2^level, 0 <= r < s per axis), every
 *   fine pixel pair maps to a coarse cell pair at offset -q or -q-1 per axis
 *   (B is placed at o, so A cell c pairs with B cell c-q or c-q-1).
 *   Grouping fine pairs by that combo gives
 *     LB(o) = penalty*uncovered + sum_{u in {0,1}^2} w_u * intervalErr(c, c-q-u)
 *   where w_u is the exact number of fine pairs in cell c with combo u.
 *   Interval distance <= any point distance, so LB(o) <= cost(o) for every o.
 *   At level 0 the bound is exact.
 *
 * The pruned search evaluates offsets in ascending coarse-LB order, refines
 * through finer levels, and only computes the exact cost when no level can
 * rule the offset out. It returns *all* offsets tied at the minimum cost;
 * ties are never pruned because pruning requires LB > best (strict).
 */

import type { RGBAImage } from './rgba.ts';
import { getPixel } from './rgba.ts';
import type { Offset, Tolerance } from './diff.ts';
import { compareImages, DEFAULT_UNCOVERED_PENALTY, pixelError } from './diff.ts';

export interface SearchRange {
  readonly minDX: number;
  readonly maxDX: number;
  readonly minDY: number;
  readonly maxDY: number;
}

export interface AlignOptions {
  readonly range: SearchRange;
  readonly tolerance: Tolerance;
  readonly uncoveredPenalty?: number;
  /** pyramid depth for pruning; 0 means the bound is computed at pixel level (exact). Default 2. */
  readonly levels?: number;
}

/**
 * Lean exact cost — independent of region building and of the pyramid.
 * Convention (shared with compareImages): B is placed at (dx, dy), so A
 * pixel (x, y) pairs with B pixel (x - dx, y - dy).
 */
export function exactCost(
  a: RGBAImage,
  b: RGBAImage,
  offset: Offset,
  tol: Tolerance,
  penalty: number = DEFAULT_UNCOVERED_PENALTY,
): number {
  const { dx, dy } = offset;
  const x0 = Math.max(0, dx);
  const y0 = Math.max(0, dy);
  const x1 = Math.min(a.width, b.width + dx);
  const y1 = Math.min(a.height, b.height + dy);
  const ow = Math.max(0, x1 - x0);
  const oh = Math.max(0, y1 - y0);
  let cost = penalty * (a.width * a.height + b.width * b.height - 2 * ow * oh);
  for (let y = y0; y < y1; y++) {
    for (let x = x0; x < x1; x++) {
      cost += pixelError(getPixel(a, x, y), getPixel(b, x - dx, y - dy), tol).error;
    }
  }
  return cost;
}

export interface IntervalLevel {
  readonly scale: number; // 2^level
  readonly width: number; // cells
  readonly height: number; // cells
  readonly min: Uint8Array; // width*height*4, per-channel min over the cell
  readonly max: Uint8Array;
}

/** Pyramid of per-channel [min,max] intervals over cleaned pixels. */
export function buildIntervalPyramid(img: RGBAImage, levels: number): IntervalLevel[] {
  const n = img.width * img.height;
  const min = new Uint8Array(n * 4);
  const max = new Uint8Array(n * 4);
  for (let i = 0; i < n; i++) {
    const alpha = img.data[i * 4 + 3];
    if (alpha !== 0) {
      for (let c = 0; c < 4; c++) {
        min[i * 4 + c] = img.data[i * 4 + c];
        max[i * 4 + c] = img.data[i * 4 + c];
      }
    } // transparent -> cleaned (0,0,0,0), already zero-filled
  }
  const pyramid: IntervalLevel[] = [
    { scale: 1, width: img.width, height: img.height, min, max },
  ];
  for (let level = 1; level <= levels; level++) {
    const prev = pyramid[level - 1];
    const w = Math.ceil(prev.width / 2);
    const h = Math.ceil(prev.height / 2);
    const mn = new Uint8Array(w * h * 4).fill(255);
    const mx = new Uint8Array(w * h * 4);
    for (let cy = 0; cy < h; cy++) {
      for (let cx = 0; cx < w; cx++) {
        const dst = (cy * w + cx) * 4;
        for (let sy = 0; sy < 2; sy++) {
          for (let sx = 0; sx < 2; sx++) {
            const px = cx * 2 + sx;
            const py = cy * 2 + sy;
            if (px >= prev.width || py >= prev.height) continue;
            const src = (py * prev.width + px) * 4;
            for (let c = 0; c < 4; c++) {
              if (prev.min[src + c] < mn[dst + c]) mn[dst + c] = prev.min[src + c];
              if (prev.max[src + c] > mx[dst + c]) mx[dst + c] = prev.max[src + c];
            }
          }
        }
      }
    }
    pyramid.push({ scale: 1 << level, width: w, height: h, min: mn, max: mx });
  }
  return pyramid;
}

function intervalError(
  la: IntervalLevel,
  ax: number,
  ay: number,
  lb: IntervalLevel,
  bx: number,
  by: number,
  tol: Tolerance,
): number {
  const ia = (ay * la.width + ax) * 4;
  const ib = (by * lb.width + bx) * 4;
  let rgbDist = 0;
  for (let c = 0; c < 3; c++) {
    const d = Math.max(0, la.min[ia + c] - lb.max[ib + c], lb.min[ib + c] - la.max[ia + c]);
    if (d > rgbDist) rgbDist = d;
  }
  const alphaDist = Math.max(0, la.min[ia + 3] - lb.max[ib + 3], lb.min[ib + 3] - la.max[ia + 3]);
  return Math.max(Math.max(0, rgbDist - tol.rgb), Math.max(0, alphaDist - tol.alpha));
}

/**
 * Admissible lower bound on exactCost at `offset`, computed at pyramid
 * `level`. Never exceeds the true cost; equals it at level 0.
 */
export function lowerBoundCost(
  pyrA: readonly IntervalLevel[],
  pyrB: readonly IntervalLevel[],
  a: RGBAImage,
  b: RGBAImage,
  offset: Offset,
  tol: Tolerance,
  penalty: number,
  level: number,
): number {
  const { dx, dy } = offset;
  const la = pyrA[level];
  const lb = pyrB[level];
  const s = la.scale;
  const qx = Math.floor(dx / s);
  const rx = dx - qx * s; // 0 <= rx < s, also for negative dx
  const qy = Math.floor(dy / s);
  const ry = dy - qy * s;

  const x0 = Math.max(0, dx);
  const y0 = Math.max(0, dy);
  const x1 = Math.min(a.width, b.width + dx);
  const y1 = Math.min(a.height, b.height + dy);
  const ow = Math.max(0, x1 - x0);
  const oh = Math.max(0, y1 - y0);
  let bound = penalty * (a.width * a.height + b.width * b.height - 2 * ow * oh);
  if (ow === 0 || oh === 0) return bound;

  const cx0 = Math.floor(x0 / s);
  const cx1 = Math.floor((x1 - 1) / s);
  const cy0 = Math.floor(y0 / s);
  const cy1 = Math.floor((y1 - 1) / s);
  for (let cy = cy0; cy <= cy1; cy++) {
    const fy0 = cy * s;
    const fy1 = Math.min(fy0 + s, a.height);
    const iy0 = Math.max(fy0, y0);
    const iy1 = Math.min(fy1, y1);
    const splitY = fy0 + ry; // local y where the B cell index decrements
    for (let cx = cx0; cx <= cx1; cx++) {
      const fx0 = cx * s;
      const fx1 = Math.min(fx0 + s, a.width);
      const ix0 = Math.max(fx0, x0);
      const ix1 = Math.min(fx1, x1);
      const splitX = fx0 + rx;
      for (let uy = 0; uy <= 1; uy++) {
        // uy === 0: local t >= ry -> B cell cy-qy; uy === 1: t < ry -> cy-qy-1
        const sy0 = uy === 0 ? Math.max(iy0, splitY) : iy0;
        const sy1 = uy === 0 ? iy1 : Math.min(iy1, splitY);
        const hh = sy1 - sy0;
        if (hh <= 0) continue;
        for (let ux = 0; ux <= 1; ux++) {
          const sx0 = ux === 0 ? Math.max(ix0, splitX) : ix0;
          const sx1 = ux === 0 ? ix1 : Math.min(ix1, splitX);
          const ww = sx1 - sx0;
          if (ww <= 0) continue;
          bound += ww * hh * intervalError(la, cx, cy, lb, cx - qx - ux, cy - qy - uy, tol);
        }
      }
    }
  }
  return bound;
}

export interface AlignmentDiagnostics {
  readonly range: SearchRange;
  readonly totalOffsets: number;
  /** offsets whose exact cost was computed */
  readonly evaluated: number;
  /** offsets ruled out by a lower bound (evaluated + pruned === totalOffsets) */
  readonly pruned: number;
  readonly levels: number;
}

export interface AlignmentResult {
  readonly bestCost: number;
  /** every offset tied at bestCost, sorted by (dy, dx) */
  readonly optima: readonly Offset[];
  readonly diagnostics: AlignmentDiagnostics;
}

export function searchAlignment(a: RGBAImage, b: RGBAImage, options: AlignOptions): AlignmentResult {
  const tol = options.tolerance;
  const penalty = options.uncoveredPenalty ?? DEFAULT_UNCOVERED_PENALTY;
  const maxDim = Math.max(a.width, a.height, b.width, b.height);
  const levels = Math.max(
    0,
    Math.min(options.levels ?? 2, Math.floor(Math.log2(Math.max(1, maxDim)))),
  );
  const pyrA = buildIntervalPyramid(a, levels);
  const pyrB = buildIntervalPyramid(b, levels);
  const { minDX, maxDX, minDY, maxDY } = options.range;

  interface Candidate {
    dx: number;
    dy: number;
    lb: number;
  }
  const candidates: Candidate[] = [];
  for (let dy = minDY; dy <= maxDY; dy++) {
    for (let dx = minDX; dx <= maxDX; dx++) {
      const offset = { dx, dy };
      candidates.push({ dx, dy, lb: lowerBoundCost(pyrA, pyrB, a, b, offset, tol, penalty, levels) });
    }
  }
  candidates.sort((p, q) => p.lb - q.lb || p.dy - q.dy || p.dx - q.dx);

  let best = Infinity;
  const optima: Offset[] = [];
  let evaluated = 0;
  let pruned = 0;
  for (let i = 0; i < candidates.length; i++) {
    const c = candidates[i];
    if (c.lb > best) {
      pruned += candidates.length - i; // sorted by LB: the rest cannot beat best
      break;
    }
    let viable = true;
    for (let level = levels - 1; level >= 1; level--) {
      const lb = lowerBoundCost(pyrA, pyrB, a, b, c, tol, penalty, level);
      if (lb > best) {
        viable = false;
        break;
      }
    }
    if (!viable) {
      pruned++;
      continue;
    }
    const cost = exactCost(a, b, c, tol, penalty);
    evaluated++;
    if (cost < best) {
      best = cost;
      optima.length = 0;
      optima.push({ dx: c.dx, dy: c.dy });
    } else if (cost === best) {
      optima.push({ dx: c.dx, dy: c.dy });
    }
  }
  optima.sort((p, q) => p.dy - q.dy || p.dx - q.dx);
  return {
    bestCost: best,
    optima,
    diagnostics: {
      range: options.range,
      totalOffsets: candidates.length,
      evaluated,
      pruned,
      levels,
    },
  };
}

export interface OffsetCost {
  readonly dx: number;
  readonly dy: number;
  readonly cost: number;
}

export interface BruteForceResult {
  readonly bestCost: number;
  readonly optima: readonly Offset[];
  readonly costs: readonly OffsetCost[];
}

/**
 * Independent per-offset reference: evaluates every offset through the full
 * compareImages path (no pyramid, no lower bounds, no shared cost code).
 */
export function bruteForceAlignment(
  a: RGBAImage,
  b: RGBAImage,
  options: AlignOptions,
): BruteForceResult {
  const tol = options.tolerance;
  const penalty = options.uncoveredPenalty ?? DEFAULT_UNCOVERED_PENALTY;
  const { minDX, maxDX, minDY, maxDY } = options.range;
  const costs: OffsetCost[] = [];
  let best = Infinity;
  for (let dy = minDY; dy <= maxDY; dy++) {
    for (let dx = minDX; dx <= maxDX; dx++) {
      const cost = compareImages(a, b, { offset: { dx, dy }, tolerance: tol, uncoveredPenalty: penalty }).cost;
      costs.push({ dx, dy, cost });
      if (cost < best) best = cost;
    }
  }
  const optima = costs
    .filter((c) => c.cost === best)
    .map(({ dx, dy }) => ({ dx, dy }));
  return { bestCost: best, optima, costs };
}
