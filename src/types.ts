export type BoundaryPolicy = 'clamp' | 'mirror' | 'wrap' | 'zero';

export interface SeparableKernel {
  readonly id: string;
  readonly hx: Float64Array;
  readonly hy: Float64Array;
  readonly anchorX: number;
  readonly anchorY: number;
  readonly boundaryX: BoundaryPolicy;
  readonly boundaryY: BoundaryPolicy;
}

export interface Halo {
  left: number;
  right: number;
  top: number;
  bottom: number;
}

export interface Region {
  x0: number;
  y0: number;
  w: number;
  h: number;
}

export const CHANNELS = 4;
export const BYTES_PER_PIXEL = CHANNELS * 4;
