import { applyToPoint, invertMatrix, IDENTITY, type Mat } from "./matrix.ts";
import { pointInShape, type Rect, type Shape } from "./geometry.ts";

/**
 * Grayscale coverage mask over a fixed canvas. Values are always in [0, 1];
 * anything outside the canvas is treated as 0 (in particular, inversion only
 * complements within the canvas / a given sub-region).
 */
export class Mask {
  readonly width: number;
  readonly height: number;
  readonly data: Float32Array;

  constructor(width: number, height: number, data?: Float32Array) {
    this.width = width;
    this.height = height;
    this.data = data ?? new Float32Array(width * height);
  }

  static filled(width: number, height: number, value: number): Mask {
    return new Mask(width, height, new Float32Array(width * height).fill(clamp01(value)));
  }

  at(x: number, y: number): number {
    return this.data[y * this.width + x];
  }
}

export function clamp01(v: number): number {
  return v < 0 ? 0 : v > 1 ? 1 : v;
}

/**
 * Supersampled rasterization of one shape; ss = subpixels per axis.
 * `transform` maps shape-local coordinates to canvas coordinates
 * (canvas sample points are mapped back through its inverse).
 */
export function rasterizeShape(
  shape: Shape,
  width: number,
  height: number,
  transform: Mat = IDENTITY,
  ss = 4,
): Mask {
  const mask = new Mask(width, height);
  const inv = ss * ss;
  const back = invertMatrix(transform);
  for (let py = 0; py < height; py++) {
    for (let px = 0; px < width; px++) {
      let hits = 0;
      for (let sy = 0; sy < ss; sy++) {
        for (let sx = 0; sx < ss; sx++) {
          const x = px + (sx + 0.5) / ss;
          const y = py + (sy + 0.5) / ss;
          const [lx, ly] = applyToPoint(back, x, y);
          if (pointInShape(lx, ly, shape)) hits++;
        }
      }
      mask.data[py * width + px] = hits / inv;
    }
  }
  return mask;
}

/** Boolean union of fractional coverage: 1 - (1-a)(1-b). Always in [0,1]. */
export function union(a: Mask, b: Mask): Mask {
  return combine(a, b, (x, y) => 1 - (1 - x) * (1 - y));
}

/** Boolean intersection of fractional coverage: a * b. Always in [0,1]. */
export function intersect(a: Mask, b: Mask): Mask {
  return combine(a, b, (x, y) => x * y);
}

/** Boolean difference of fractional coverage: a * (1-b). Always in [0,1]. */
export function subtract(a: Mask, b: Mask): Mask {
  return combine(a, b, (x, y) => x * (1 - y));
}

/**
 * Complement restricted to `region` (defaults to the whole canvas).
 * Inside the region: 1 - m. Outside: 0. The complement never leaks
 * beyond the canvas/region bounds.
 */
export function invert(m: Mask, region?: Rect): Mask {
  const r = region ?? { x: 0, y: 0, w: m.width, h: m.height };
  const x0 = Math.max(0, Math.ceil(r.x));
  const y0 = Math.max(0, Math.ceil(r.y));
  const x1 = Math.min(m.width, Math.floor(r.x + r.w));
  const y1 = Math.min(m.height, Math.floor(r.y + r.h));
  const out = new Mask(m.width, m.height);
  for (let y = y0; y < y1; y++) {
    for (let x = x0; x < x1; x++) {
      out.data[y * m.width + x] = 1 - m.data[y * m.width + x];
    }
  }
  return out;
}

function combine(a: Mask, b: Mask, f: (x: number, y: number) => number): Mask {
  if (a.width !== b.width || a.height !== b.height) {
    throw new Error("mask size mismatch");
  }
  const out = new Mask(a.width, a.height);
  for (let i = 0; i < a.data.length; i++) {
    out.data[i] = clamp01(f(a.data[i], b.data[i]));
  }
  return out;
}

/** Convenience: rasterize several shapes with one transform and union them. */
export function rasterizeUnion(
  shapes: Shape[],
  width: number,
  height: number,
  transform: Mat = IDENTITY,
  ss = 4,
): Mask {
  let acc = new Mask(width, height);
  for (const s of shapes) {
    acc = union(acc, rasterizeShape(s, width, height, transform, ss));
  }
  return acc;
}
