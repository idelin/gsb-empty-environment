import type { BoundaryPolicy } from './boundary.ts';

export interface Kernel1D {
  /** Tap weights, applied as out(x) = sum_i taps[i] * in(x + i - anchor). */
  readonly taps: readonly number[];
  /** Index of the tap aligned with the output pixel. 0 <= anchor < taps.length. */
  readonly anchor: number;
}

export interface StageSpec {
  readonly horizontal: Kernel1D;
  readonly vertical: Kernel1D;
  readonly boundaryH: BoundaryPolicy;
  readonly boundaryV: BoundaryPolicy;
}

/** An immutable, versioned kernel chain. Hot-swapping creates a new version. */
export interface KernelChain {
  readonly version: number;
  readonly stages: readonly StageSpec[];
}

export interface Rect {
  readonly x0: number;
  readonly y0: number;
  readonly x1: number; // exclusive
  readonly y1: number; // exclusive
}

export interface Halo {
  readonly left: number;
  readonly right: number;
  readonly top: number;
  readonly bottom: number;
}

export function kernel(taps: readonly number[], anchor?: number): Kernel1D {
  if (taps.length === 0) throw new Error('kernel: empty taps');
  const a = anchor ?? Math.floor(taps.length / 2);
  if (!Number.isInteger(a) || a < 0 || a >= taps.length) {
    throw new Error(`kernel: anchor ${a} out of range for ${taps.length} taps`);
  }
  return { taps: [...taps], anchor: a };
}

export function stage(
  horizontal: Kernel1D,
  vertical: Kernel1D,
  boundaryH: BoundaryPolicy,
  boundaryV: BoundaryPolicy,
): StageSpec {
  return { horizontal, vertical, boundaryH, boundaryV };
}

export function makeChain(version: number, stages: readonly StageSpec[]): KernelChain {
  if (stages.length === 0) throw new Error('chain: needs at least one stage');
  return { version, stages: [...stages] };
}

/** Samples a stage consumes to the left/right/top/bottom of each output pixel. */
export function stageHalo(s: StageSpec): Halo {
  return {
    left: s.horizontal.anchor,
    right: s.horizontal.taps.length - 1 - s.horizontal.anchor,
    top: s.vertical.anchor,
    bottom: s.vertical.taps.length - 1 - s.vertical.anchor,
  };
}

/** Total halo of a fused chain: the sum of every stage's halo. */
export function chainHalo(chain: KernelChain): Halo {
  let left = 0;
  let right = 0;
  let top = 0;
  let bottom = 0;
  for (const s of chain.stages) {
    const h = stageHalo(s);
    left += h.left;
    right += h.right;
    top += h.top;
    bottom += h.bottom;
  }
  return { left, right, top, bottom };
}

export function expandRect(r: Rect, h: Halo): Rect {
  return { x0: r.x0 - h.left, y0: r.y0 - h.top, x1: r.x1 + h.right, y1: r.y1 + h.bottom };
}

export function clipRect(r: Rect, w: number, h: number): Rect {
  return {
    x0: Math.max(0, r.x0),
    y0: Math.max(0, r.y0),
    x1: Math.min(w, r.x1),
    y1: Math.min(h, r.y1),
  };
}

export function rectWidth(r: Rect): number {
  return r.x1 - r.x0;
}

export function rectHeight(r: Rect): number {
  return r.y1 - r.y0;
}

export function rectsIntersect(a: Rect, b: Rect): boolean {
  return a.x0 < b.x1 && b.x0 < a.x1 && a.y0 < b.y1 && b.y0 < a.y1;
}

/**
 * Exact input region a stage needs to produce `out`, expressed in absolute
 * image coordinates and clipped to the image.
 *
 * Beyond the plain halo expansion this accounts for boundary policies that
 * can redirect out-of-range samples to distant in-image pixels:
 *  - 'wrap' connects both edges, so the whole dimension is required;
 *  - 'mirror'/'reflect101' reflect out-of-range samples back inside by up to
 *    the out-of-range distance, which can exceed the plain halo for narrow
 *    output regions with far-off-centre anchors.
 */
export function requiredInputRect(
  out: Rect,
  s: StageSpec,
  imgW: number,
  imgH: number,
): Rect {
  const h = stageHalo(s);
  let x0 = out.x0 - h.left;
  let x1 = out.x1 + h.right;
  let y0 = out.y0 - h.top;
  let y1 = out.y1 + h.bottom;

  if (s.boundaryH === 'wrap') {
    x0 = 0;
    x1 = imgW;
  } else {
    if (x0 < 0) {
      x1 = Math.max(x1, Math.min(imgW, -x0));
      x0 = 0;
    }
    if (x1 > imgW) {
      x0 = Math.min(x0, Math.max(0, 2 * imgW - x1));
      x1 = imgW;
    }
  }
  if (s.boundaryV === 'wrap') {
    y0 = 0;
    y1 = imgH;
  } else {
    if (y0 < 0) {
      y1 = Math.max(y1, Math.min(imgH, -y0));
      y0 = 0;
    }
    if (y1 > imgH) {
      y0 = Math.min(y0, Math.max(0, 2 * imgH - y1));
      y1 = imgH;
    }
  }
  return { x0, y0, x1, y1 };
}
