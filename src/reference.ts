import { makeSampler } from './stage.ts';
import { runStage } from './stage.ts';
import type { KernelChain } from './kernels.ts';
import { CHANNELS, type Metrics } from './metrics.ts';

export interface Raster {
  readonly width: number;
  readonly height: number;
  /** RGBA float32, row-major, length = width * height * 4. */
  readonly data: Float32Array;
}

export function makeRaster(width: number, height: number, data?: Float32Array): Raster {
  const expected = width * height * CHANNELS;
  const d = data ?? new Float32Array(expected);
  if (d.length !== expected) throw new Error('raster: bad data length');
  return { width, height, data: d };
}

/**
 * Untiled per-pixel reference: each stage runs over the whole image in one
 * piece (horizontal pass, then vertical pass). This is the ground truth the
 * tiled convolver is validated against.
 */
export function convolveReference(src: Raster, chain: KernelChain, metrics?: Metrics): Raster {
  let current: Raster = src;
  for (const st of chain.stages) {
    const sampler = makeSampler(
      current.data,
      0,
      0,
      current.width,
      current.height,
      current.width,
      current.height,
      st.boundaryH,
      st.boundaryV,
    );
    const out = runStage(
      sampler,
      st,
      { x0: 0, y0: 0, x1: current.width, y1: current.height },
      metrics,
    );
    current = { width: src.width, height: src.height, data: out.data };
  }
  return current;
}
