/**
 * Tolerance-aware comparison of two RGBA images at a given integer offset.
 *
 * Semantics (all explicit, nothing is cropped away):
 *
 * - Canvas: image A sits at (0,0), image B sits at (dx,dy). The comparison
 *   canvas is the *union* of both footprints, so size differences show up as
 *   uncovered pixels instead of being silently clipped.
 * - Cleaning: a transparent pixel (alpha 0) compares as (0,0,0,0); its RGB
 *   bytes are meaningless. Two transparent pixels are always equal.
 * - Per-pixel error: on cleaned pixels, rgbDiff = max |channel delta| over
 *   R,G,B and alphaDiff = |alpha delta|. Excesses are the amounts by which
 *   these exceed the explicit tolerances; error = max(rgbExcess, alphaExcess).
 * - Uncovered pixels (only one image covers the canvas cell) are a separate
 *   diff kind ('a-only' / 'b-only') and cost `uncoveredPenalty` each.
 */

import type { RGBA, RGBAImage } from './rgba.ts';
import { getPixel } from './rgba.ts';

export interface Tolerance {
  /** max allowed per-channel RGB delta (0..255) */
  readonly rgb: number;
  /** max allowed alpha delta (0..255) */
  readonly alpha: number;
}

export interface Offset {
  readonly dx: number;
  readonly dy: number;
}

export interface Rect {
  readonly x: number;
  readonly y: number;
  readonly width: number;
  readonly height: number;
}

export const DEFAULT_UNCOVERED_PENALTY = 255;

export interface CompareOptions {
  /** placement of B relative to A; defaults to (0,0) */
  readonly offset?: Offset;
  /** explicit color tolerance (required — no hidden default) */
  readonly tolerance: Tolerance;
  /** cost of one uncovered pixel; defaults to 255 (>= any pixel mismatch) */
  readonly uncoveredPenalty?: number;
}

function checkTolerance(tol: Tolerance): void {
  for (const [name, v] of [['rgb', tol.rgb], ['alpha', tol.alpha]] as const) {
    if (!Number.isInteger(v) || v < 0 || v > 255) {
      throw new Error(`tolerance.${name} must be an integer in 0..255, got ${v}`);
    }
  }
}

/** Transparent pixels compare as (0,0,0,0). */
export function cleanPixel(px: RGBA): RGBA {
  return px[3] === 0 ? [0, 0, 0, 0] : px;
}

export interface PixelError {
  /** raw max-channel RGB delta after cleaning, 0..255 */
  readonly rgbDiff: number;
  /** raw alpha delta after cleaning, 0..255 */
  readonly alphaDiff: number;
  /** max(0, rgbDiff - tolerance.rgb) */
  readonly rgbExcess: number;
  /** max(0, alphaDiff - tolerance.alpha) */
  readonly alphaExcess: number;
  /** max(rgbExcess, alphaExcess) */
  readonly error: number;
}

export function pixelError(p: RGBA, q: RGBA, tol: Tolerance): PixelError {
  const cp = cleanPixel(p);
  const cq = cleanPixel(q);
  const rgbDiff = Math.max(
    Math.abs(cp[0] - cq[0]),
    Math.abs(cp[1] - cq[1]),
    Math.abs(cp[2] - cq[2]),
  );
  const alphaDiff = Math.abs(cp[3] - cq[3]);
  const rgbExcess = Math.max(0, rgbDiff - tol.rgb);
  const alphaExcess = Math.max(0, alphaDiff - tol.alpha);
  return { rgbDiff, alphaDiff, rgbExcess, alphaExcess, error: Math.max(rgbExcess, alphaExcess) };
}

export type PixelDiffKind =
  | 'match' // equal within tolerance
  | 'transparent-match' // both transparent (RGB ignored)
  | 'rgb' // only RGB exceeds tolerance
  | 'alpha' // only alpha exceeds tolerance
  | 'rgb+alpha' // both exceed tolerance
  | 'a-only' // covered by A only (B missing)
  | 'b-only'; // covered by B only (A missing)

export function classifyPixel(p: RGBA, q: RGBA, tol: Tolerance): PixelDiffKind {
  if (p[3] === 0 && q[3] === 0) return 'transparent-match';
  const e = pixelError(p, q, tol);
  if (e.rgbExcess > 0 && e.alphaExcess > 0) return 'rgb+alpha';
  if (e.rgbExcess > 0) return 'rgb';
  if (e.alphaExcess > 0) return 'alpha';
  return 'match';
}

export interface DiffPixel {
  /** canvas-plane coordinates (A at origin, B at offset); may be negative */
  readonly canvas: { readonly x: number; readonly y: number };
  /** coordinates in image A, or null when A does not cover this cell */
  readonly a: { readonly x: number; readonly y: number } | null;
  /** coordinates in image B, or null when B does not cover this cell */
  readonly b: { readonly x: number; readonly y: number } | null;
  readonly kind: PixelDiffKind;
  /** cost contribution: pixel error, or uncoveredPenalty for a-only/b-only */
  readonly error: number;
  readonly rgbDiff: number;
  readonly alphaDiff: number;
  /** missing evidence: the actual pixel values (null on the missing side) */
  readonly aValue: RGBA | null;
  readonly bValue: RGBA | null;
}

export interface DiffRegion {
  readonly id: number;
  readonly bboxCanvas: Rect;
  /** bbox mapped back to image A coordinates, null if the region misses A */
  readonly bboxA: Rect | null;
  /** bbox mapped back to image B coordinates, null if the region misses B */
  readonly bboxB: Rect | null;
  readonly pixelCount: number;
  /** pixels whose RGB exceeds tolerance (kind 'rgb' or 'rgb+alpha') */
  readonly rgbPixels: number;
  /** pixels whose alpha exceeds tolerance (kind 'alpha' or 'rgb+alpha') */
  readonly alphaPixels: number;
  readonly aOnlyPixels: number;
  readonly bOnlyPixels: number;
  readonly maxError: number;
  readonly pixels: readonly DiffPixel[];
}

export interface CompareResult {
  readonly offset: Offset;
  /** union canvas rect in plane coordinates (origin may be negative) */
  readonly canvas: Rect;
  readonly equal: boolean;
  /** total cost: sum of pixel errors + uncoveredPenalty * uncovered pixels */
  readonly cost: number;
  /** max per-pixel error over diff pixels (uncovered counts as penalty) */
  readonly maxError: number;
  /** max raw RGB delta over covered pixel pairs */
  readonly maxRgbDiff: number;
  /** max raw alpha delta over covered pixel pairs */
  readonly maxAlphaDiff: number;
  readonly diffPixels: number;
  readonly rgbPixels: number;
  readonly alphaPixels: number;
  readonly aOnlyPixels: number;
  readonly bOnlyPixels: number;
  readonly transparentMatches: number;
  readonly regions: readonly DiffRegion[];
}

export function compareImages(a: RGBAImage, b: RGBAImage, options: CompareOptions): CompareResult {
  const tol = options.tolerance;
  checkTolerance(tol);
  const offset = options.offset ?? { dx: 0, dy: 0 };
  const { dx, dy } = offset;
  const penalty = options.uncoveredPenalty ?? DEFAULT_UNCOVERED_PENALTY;

  const canvasX = Math.min(0, dx);
  const canvasY = Math.min(0, dy);
  const canvasW = Math.max(a.width, dx + b.width) - canvasX;
  const canvasH = Math.max(a.height, dy + b.height) - canvasY;

  const diffAt = new Map<number, DiffPixel>();
  let cost = 0;
  let maxError = 0;
  let maxRgbDiff = 0;
  let maxAlphaDiff = 0;
  let rgbPixels = 0;
  let alphaPixels = 0;
  let aOnlyPixels = 0;
  let bOnlyPixels = 0;
  let transparentMatches = 0;

  for (let cy = 0; cy < canvasH; cy++) {
    for (let cx = 0; cx < canvasW; cx++) {
      const px = canvasX + cx; // plane coordinates
      const py = canvasY + cy;
      const inA = px >= 0 && px < a.width && py >= 0 && py < a.height;
      const inB = px - dx >= 0 && px - dx < b.width && py - dy >= 0 && py - dy < b.height;
      const aPos = inA ? { x: px, y: py } : null;
      const bPos = inB ? { x: px - dx, y: py - dy } : null;
      const aValue = inA ? getPixel(a, px, py) : null;
      const bValue = inB ? getPixel(b, px - dx, py - dy) : null;

      if (!inA && !inB) continue; // gap cell inside the bounding box: no evidence either way
      let kind: PixelDiffKind;
      let error = 0;
      let rgbDiff = 0;
      let alphaDiff = 0;
      if (inA && inB) {
        const e = pixelError(aValue as RGBA, bValue as RGBA, tol);
        kind = classifyPixel(aValue as RGBA, bValue as RGBA, tol);
        error = e.error;
        rgbDiff = e.rgbDiff;
        alphaDiff = e.alphaDiff;
        if (rgbDiff > maxRgbDiff) maxRgbDiff = rgbDiff;
        if (alphaDiff > maxAlphaDiff) maxAlphaDiff = alphaDiff;
        if (kind === 'transparent-match') transparentMatches++;
      } else if (inA) {
        kind = 'a-only';
        error = penalty;
        aOnlyPixels++;
      } else {
        kind = 'b-only';
        error = penalty;
        bOnlyPixels++;
      }

      const isDiff =
        kind === 'rgb' || kind === 'alpha' || kind === 'rgb+alpha' || kind === 'a-only' || kind === 'b-only';
      cost += error;
      if (!isDiff) continue;
      if (error > maxError) maxError = error;
      if (kind === 'rgb' || kind === 'rgb+alpha') rgbPixels++;
      if (kind === 'alpha' || kind === 'rgb+alpha') alphaPixels++;
      diffAt.set(cy * canvasW + cx, {
        canvas: { x: px, y: py },
        a: aPos,
        b: bPos,
        kind,
        error,
        rgbDiff,
        alphaDiff,
        aValue,
        bValue,
      });
    }
  }

  // Connected components (4-connectivity) over the diff mask.
  const visited = new Uint8Array(canvasW * canvasH);
  const regions: DiffRegion[] = [];
  const stack: number[] = [];
  for (const [start] of diffAt) {
    const si = start;
    if (visited[si]) continue;
    visited[si] = 1;
    stack.push(si);
    const members: DiffPixel[] = [];
    while (stack.length > 0) {
      const i = stack.pop() as number;
      const pix = diffAt.get(i);
      if (!pix) continue;
      members.push(pix);
      const cx = i % canvasW;
      const cy = (i - cx) / canvasW;
      const neighbors = [
        cx > 0 ? i - 1 : -1,
        cx < canvasW - 1 ? i + 1 : -1,
        cy > 0 ? i - canvasW : -1,
        cy < canvasH - 1 ? i + canvasW : -1,
      ];
      for (const n of neighbors) {
        if (n >= 0 && !visited[n] && diffAt.has(n)) {
          visited[n] = 1;
          stack.push(n);
        }
      }
    }
    members.sort((p, q) => p.canvas.y - q.canvas.y || p.canvas.x - q.canvas.x);
    regions.push(buildRegion(regions.length, members));
  }
  regions.sort(
    (r, s) => r.bboxCanvas.y - s.bboxCanvas.y || r.bboxCanvas.x - s.bboxCanvas.x || r.id - s.id,
  );

  return {
    offset,
    canvas: { x: canvasX, y: canvasY, width: canvasW, height: canvasH },
    equal: diffAt.size === 0,
    cost,
    maxError,
    maxRgbDiff,
    maxAlphaDiff,
    diffPixels: diffAt.size,
    rgbPixels,
    alphaPixels,
    aOnlyPixels,
    bOnlyPixels,
    transparentMatches,
    regions,
  };
}

function bboxOf(points: readonly { x: number; y: number }[]): Rect {
  let x0 = Infinity;
  let y0 = Infinity;
  let x1 = -Infinity;
  let y1 = -Infinity;
  for (const p of points) {
    if (p.x < x0) x0 = p.x;
    if (p.y < y0) y0 = p.y;
    if (p.x > x1) x1 = p.x;
    if (p.y > y1) y1 = p.y;
  }
  return { x: x0, y: y0, width: x1 - x0 + 1, height: y1 - y0 + 1 };
}

function buildRegion(id: number, members: DiffPixel[]): DiffRegion {
  const aPoints = members.filter((p) => p.a !== null).map((p) => p.a as { x: number; y: number });
  const bPoints = members.filter((p) => p.b !== null).map((p) => p.b as { x: number; y: number });
  return {
    id,
    bboxCanvas: bboxOf(members.map((p) => p.canvas)),
    bboxA: aPoints.length > 0 ? bboxOf(aPoints) : null,
    bboxB: bPoints.length > 0 ? bboxOf(bPoints) : null,
    pixelCount: members.length,
    rgbPixels: members.filter((p) => p.kind === 'rgb' || p.kind === 'rgb+alpha').length,
    alphaPixels: members.filter((p) => p.kind === 'alpha' || p.kind === 'rgb+alpha').length,
    aOnlyPixels: members.filter((p) => p.kind === 'a-only').length,
    bOnlyPixels: members.filter((p) => p.kind === 'b-only').length,
    maxError: members.reduce((m, p) => Math.max(m, p.error), 0),
    pixels: members,
  };
}
