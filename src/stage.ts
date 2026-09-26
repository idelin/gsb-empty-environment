import { mapIndex, type BoundaryPolicy } from './boundary.ts';
import type { Rect, StageSpec } from './kernels.ts';
import { rectHeight, rectWidth, stageHalo } from './kernels.ts';
import {
  CHANNELS,
  allocBuffer,
  releaseBuffer,
  type MemTracker,
  type Metrics,
} from './metrics.ts';

/**
 * Samples an absolute image coordinate from a buffer that covers a
 * (possibly sub-image) rectangle. Out-of-image coordinates are resolved
 * against the IMAGE dimensions with the given boundary policies — never
 * against the buffer/tile rectangle. In-image coordinates are guaranteed
 * by the caller (halo derivation) to lie inside the buffer; a violation
 * throws instead of silently reading a tile edge as an image edge.
 */
export interface Sampler {
  sample(x: number, y: number, c: number): number;
}

export class CoverageError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'CoverageError';
  }
}

export function makeSampler(
  data: Float32Array,
  originX: number,
  originY: number,
  bufW: number,
  bufH: number,
  imgW: number,
  imgH: number,
  policyH: BoundaryPolicy,
  policyV: BoundaryPolicy,
): Sampler {
  return {
    sample(x: number, y: number, c: number): number {
      let sx = x;
      let sy = y;
      if (sx < 0 || sx >= imgW) sx = mapIndex(sx, imgW, policyH);
      if (sy < 0 || sy >= imgH) sy = mapIndex(sy, imgH, policyV);
      if (sx < 0 || sy < 0) return 0; // 'zero' policy
      const lx = sx - originX;
      const ly = sy - originY;
      if (lx < 0 || lx >= bufW || ly < 0 || ly >= bufH) {
        throw new CoverageError(
          `sample (${sx},${sy}) outside buffer [${originX}..${originX + bufW})x[${originY}..${originY + bufH})`,
        );
      }
      return data[(ly * bufW + lx) * CHANNELS + c];
    },
  };
}

export interface RegionBuffer {
  readonly data: Float32Array;
  readonly rect: Rect;
}

/**
 * Run one separable stage (horizontal pass then vertical pass) producing
 * the output region `out`. Samples come from `sampler`; boundary policies
 * of the stage apply at image edges only.
 */
export function runStage(
  sampler: Sampler,
  st: StageSpec,
  out: Rect,
  metrics?: Metrics,
  tracker?: MemTracker,
): RegionBuffer {
  const ow = rectWidth(out);
  const oh = rectHeight(out);
  if (ow <= 0 || oh <= 0) throw new Error('runStage: empty output region');
  const hk = st.horizontal;
  const vk = st.vertical;
  const halo = stageHalo(st);

  // Horizontal pass covers the output columns and the rows the vertical
  // pass will need (top/bottom halo). Width stays at output width, so the
  // intermediate buffer is minimal.
  const tmpH = oh + halo.top + halo.bottom;
  const tmp = allocBuffer(ow * tmpH, tracker);
  try {
    for (let ty = 0; ty < tmpH; ty++) {
      const y = out.y0 - halo.top + ty;
      for (let x = 0; x < ow; x++) {
        const ax = out.x0 + x;
        for (let c = 0; c < CHANNELS; c++) {
          let acc = 0;
          for (let i = 0; i < hk.taps.length; i++) {
            acc += hk.taps[i] * sampler.sample(ax + i - hk.anchor, y, c);
          }
          if (metrics) metrics.ops += hk.taps.length;
          tmp[(ty * ow + x) * CHANNELS + c] = acc;
        }
      }
    }

    const outBuf = allocBuffer(ow * oh, tracker);
    try {
      for (let y = 0; y < oh; y++) {
        for (let x = 0; x < ow; x++) {
          for (let c = 0; c < CHANNELS; c++) {
            let acc = 0;
            for (let j = 0; j < vk.taps.length; j++) {
              const ty = y + j; // tmp row 0 corresponds to out.y0 - halo.top
              acc += vk.taps[j] * tmp[(ty * ow + x) * CHANNELS + c];
            }
            if (metrics) metrics.ops += vk.taps.length;
            outBuf[(y * ow + x) * CHANNELS + c] = acc;
          }
        }
      }
    } catch (e) {
      releaseBuffer(outBuf, tracker);
      throw e;
    }
    return { data: outBuf, rect: out };
  } finally {
    releaseBuffer(tmp, tracker);
  }
}
