import { makePlane, type BoundaryMode, type LabelMask, type Plane, labelPasses } from './types.ts';
import { StructuringElement } from './se.ts';

/**
 * A resolved sampler returns the foreground value (0/1) at any coordinate,
 * applying the boundary rule. It returns null only for 'ignore' out-of-bounds
 * samples, meaning "no constraint".
 */
export type Sampler = (x: number, y: number) => number | null;

export function makeResolvedSampler(
  get: (x: number, y: number) => number,
  width: number,
  height: number,
  boundary: BoundaryMode,
): Sampler {
  const inBounds = (x: number, y: number) => x >= 0 && y >= 0 && x < width && y < height;
  switch (boundary) {
    case 'zero':
      return (x, y) => (inBounds(x, y) ? (get(x, y) > 0 ? 1 : 0) : 0);
    case 'one':
      return (x, y) => (inBounds(x, y) ? (get(x, y) > 0 ? 1 : 0) : 1);
    case 'clamp':
      return (x, y) => {
        const cx = Math.min(width - 1, Math.max(0, x));
        const cy = Math.min(height - 1, Math.max(0, y));
        return get(cx, cy) > 0 ? 1 : 0;
      };
    case 'ignore':
      return (x, y) => (inBounds(x, y) ? (get(x, y) > 0 ? 1 : 0) : null);
  }
}

/** Erosion sample position for output pixel (x,y): offset relative to the SE origin. */
function erodeSampleAt(se: StructuringElement, i: number, x: number, y: number): [number, number] {
  const off = se.offsets[i];
  return [x + off[0] - se.origin[0], y + off[1] - se.origin[1]];
}

/**
 * Dilation stamps the SE (anchored at its origin) onto every foreground
 * pixel, so output p is foreground iff some input foreground pixel sits at
 * p - (offset - origin). With an asymmetric SE the stamp extends along the
 * SE's own direction (e.g. offsets (0,0),(1,0),(2,0) grow towards +x).
 */
export function dilatePoint(sample: Sampler, x: number, y: number, se: StructuringElement): number {
  for (let i = 0; i < se.offsets.length; i++) {
    const off = se.offsets[i];
    if (sample(x - off[0] + se.origin[0], y - off[1] + se.origin[1]) === 1) return 1;
  }
  return 0;
}

export function erodePoint(sample: Sampler, x: number, y: number, se: StructuringElement): number {
  let sawAny = false;
  for (let i = 0; i < se.offsets.length; i++) {
    const [sx, sy] = erodeSampleAt(se, i, x, y);
    const v = sample(sx, sy);
    if (v === null) continue; // 'ignore': missing neighbours do not constrain
    sawAny = true;
    if (v === 0) return 0;
  }
  return sawAny ? 1 : 1; // vacuous window (all ignored) erodes to foreground
}

/** open = dilate(erode(src)); intermediate plane is virtual, sampled on demand. */
export function openPoint(
  sample: Sampler,
  x: number,
  y: number,
  se: StructuringElement,
  width: number,
  height: number,
  boundary: BoundaryMode,
): number {
  const eroded = makeResolvedSampler(
    (qx, qy) => erodePoint(sample, qx, qy, se),
    width,
    height,
    boundary,
  );
  return dilatePoint(eroded, x, y, se);
}

/** close = erode(dilate(src)). */
export function closePoint(
  sample: Sampler,
  x: number,
  y: number,
  se: StructuringElement,
  width: number,
  height: number,
  boundary: BoundaryMode,
): number {
  const dilated = makeResolvedSampler(
    (qx, qy) => dilatePoint(sample, qx, qy, se),
    width,
    height,
    boundary,
  );
  return erodePoint(dilated, x, y, se);
}

export type LocalOp = 'dilate' | 'erode' | 'open' | 'close';

export function localOpPoint(
  op: LocalOp,
  sample: Sampler,
  x: number,
  y: number,
  se: StructuringElement,
  width: number,
  height: number,
  boundary: BoundaryMode,
): number {
  switch (op) {
    case 'dilate':
      return dilatePoint(sample, x, y, se);
    case 'erode':
      return erodePoint(sample, x, y, se);
    case 'open':
      return openPoint(sample, x, y, se, width, height, boundary);
    case 'close':
      return closePoint(sample, x, y, se, width, height, boundary);
  }
}

export interface LabelContext {
  plane?: Plane; // label per pixel
  mask?: LabelMask; // labels this stage writes; others pass the input through
}

/** Reference whole-plane local op (naive, exact). Used by tests and full runs. */
export function localOpPlane(
  op: LocalOp,
  src: Plane,
  se: StructuringElement,
  boundary: BoundaryMode = 'zero',
  labels: LabelContext = {},
): Plane {
  const out = makePlane(src.width, src.height);
  const sample = makeResolvedSampler(
    (x, y) => src.data[y * src.width + x],
    src.width,
    src.height,
    boundary,
  );
  for (let y = 0; y < src.height; y++) {
    for (let x = 0; x < src.width; x++) {
      const i = y * src.width + x;
      if (labels.plane && !labelPasses(labels.mask, labels.plane.data[i])) {
        out.data[i] = src.data[i] > 0 ? 1 : 0; // label masked out: pass through
        continue;
      }
      out.data[i] = localOpPoint(op, sample, x, y, se, src.width, src.height, boundary);
    }
  }
  return out;
}

/** Influence radius of a local op: how far an input change can move the output. */
export function localOpReach(op: LocalOp, se: StructuringElement): { rx: number; ry: number } {
  const r = { rx: se.reachX, ry: se.reachY };
  if (op === 'open' || op === 'close') return { rx: r.rx * 2, ry: r.ry * 2 };
  return r;
}
