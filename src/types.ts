/** A single-channel binary/grey plane (values 0..255, morphology treats >0 as foreground). */
export interface Plane {
  width: number;
  height: number;
  data: Uint8Array;
}

export function makePlane(width: number, height: number, fill = 0): Plane {
  return { width, height, data: new Uint8Array(width * height).fill(fill) };
}

export function clonePlane(p: Plane): Plane {
  return { width: p.width, height: p.height, data: new Uint8Array(p.data) };
}

export function planesEqual(a: Plane, b: Plane): boolean {
  if (a.width !== b.width || a.height !== b.height) return false;
  const n = a.data.length;
  for (let i = 0; i < n; i++) if ((a.data[i] > 0) !== (b.data[i] > 0)) return false;
  return true;
}

/** How out-of-bounds samples are resolved. Each stage may pick its own. */
export type BoundaryMode = 'zero' | 'one' | 'clamp' | 'ignore';

/** Per-stage label mask: only pixels whose label passes are written; others pass through. */
export type LabelMask = ReadonlySet<number>;

export function labelPasses(mask: LabelMask | undefined, label: number): boolean {
  return mask === undefined || mask.has(label);
}

/** A pixel edit on a plane. */
export interface PixelEdit {
  x: number;
  y: number;
  value: number;
}

/** A recorded change (old -> new) used for incremental propagation. */
export interface PixelChange {
  x: number;
  y: number;
  oldValue: number;
  newValue: number;
}
