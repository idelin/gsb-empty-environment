export type BoundaryMode = 'zero' | 'max' | 'clamp' | 'skip';

/** Inclusive-exclusive rectangle: [x0, x1) x [y0, y1). */
export interface Rect {
  x0: number;
  y0: number;
  x1: number;
  y1: number;
}

export const FULL_VALUE = 255;

/** Minimal pixel-addressable surface; both GrayImage and TiledGray satisfy it. */
export interface Raster {
  readonly width: number;
  readonly height: number;
  get(x: number, y: number): number;
  set(x: number, y: number, v: number): void;
}
