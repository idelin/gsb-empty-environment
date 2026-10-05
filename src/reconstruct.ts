import { GrayImage } from './image.ts';
import type { StructuringElement } from './se.ts';
import type { Raster } from './types.ts';

export interface ReconstructOptions {
  /** Neighborhood used for propagation (e.g. CROSS or SQUARE3). */
  connectivity: StructuringElement;
  /** Maximum number of relaxation sweeps before giving up. Default: unbounded. */
  budget?: number;
  /** Per-pixel update gate: 0 = pixel stays at min(marker, mask). */
  label?: Uint8Array | null;
  /** Per-pixel region restriction: only pixels with region!=0 are updated. */
  region?: Uint8Array | null;
  /** Starting raster (raised to >= marker, clamped to <= mask); default marker. */
  seed?: Raster | null;
  /** Destination raster; defaults to a freshly allocated GrayImage. May be a
   *  TiledGray so the fixed-point sweep runs through the bounded tile cache. */
  out?: Raster | null;
}

export interface ReconstructResult {
  out: Raster;
  /** True when a fixed point was reached within the budget. */
  converged: boolean;
  /** Number of relaxation sweeps performed. */
  iterations: number;
  /** Pixels whose final value differs from the initial seed. */
  changed: number;
}

/**
 * Geodesic reconstruction by dilation: the greatest fixed point of
 *   f -> min(dilate(f, connectivity), mask)
 * starting at the marker (or an explicit seed). The result never exceeds the
 * mask. Propagation is sweep-based and restricted only by `region`/`label`,
 * never by any kernel radius, so it can cross the whole image.
 */
export function reconstructDilation(
  marker: Raster,
  mask: Raster,
  opts: ReconstructOptions,
): ReconstructResult {
  const { width, height } = marker;
  if (mask.width !== width || mask.height !== height) {
    throw new Error('marker/mask size mismatch');
  }
  const budget = opts.budget ?? Number.POSITIVE_INFINITY;
  const out: Raster = opts.out ?? new GrayImage(width, height);
  const n = width * height;

  const initial = new Uint8Array(n);
  for (let y = 0; y < height; y++) {
    for (let x = 0; x < width; x++) {
      const i = y * width + x;
      let v = opts.seed ? opts.seed.get(x, y) : marker.get(x, y);
      const mk = marker.get(x, y);
      if (mk > v) v = mk;
      const mv = mask.get(x, y);
      if (v > mv) v = mv;
      out.set(x, y, v);
      initial[i] = v;
    }
  }

  const updatable = new Uint8Array(n);
  for (let i = 0; i < n; i++) {
    updatable[i] = (opts.label && !opts.label[i]) || (opts.region && !opts.region[i]) ? 0 : 1;
  }

  const offsets = opts.connectivity.offsets;
  let iterations = 0;
  for (;;) {
    let changedThisSweep = false;
    for (let y = 0; y < height; y++) {
      for (let x = 0; x < width; x++) {
        const i = y * width + x;
        if (!updatable[i]) continue;
        const cur = out.get(x, y);
        let v = cur;
        for (const [dx, dy] of offsets) {
          const nx = x + dx;
          const ny = y + dy;
          if (nx < 0 || ny < 0 || nx >= width || ny >= height) continue;
          const nv = out.get(nx, ny);
          if (nv > v) v = nv;
        }
        const mv = mask.get(x, y);
        if (v > mv) v = mv;
        if (v > cur) {
          out.set(x, y, v);
          changedThisSweep = true;
        }
      }
    }
    if (!changedThisSweep) {
      let changed = 0;
      for (let y = 0; y < height; y++) {
        for (let x = 0; x < width; x++) {
          if (out.get(x, y) !== initial[y * width + x]) changed++;
        }
      }
      return { out, converged: true, iterations, changed };
    }
    iterations++;
    if (iterations >= budget) {
      let changed = 0;
      for (let y = 0; y < height; y++) {
        for (let x = 0; x < width; x++) {
          if (out.get(x, y) !== initial[y * width + x]) changed++;
        }
      }
      return { out, converged: false, iterations, changed };
    }
  }
}
