import type { GamutStrategy, Vec3 } from './types.ts';
import type { ConversionPlan } from './graph.ts';

export interface BatchOptions {
  strategy: GamutStrategy;
  /** Pixels per chunk; the batch is executed chunk by chunk. Default 1024. */
  chunkSize?: number;
  /**
   * Clip mask produced by an earlier irreversible pass. Bits are carried
   * forward: a pixel clipped once stays marked, so repeated clipping can
   * never claim to be reversible.
   */
  priorClipMask?: Uint8Array;
  /** Called after each chunk completes: (chunksDone, chunksTotal). */
  onChunk?: (chunksDone: number, chunksTotal: number) => void;
}

export interface BatchResult {
  /** RGBA interleaved, same length as input. Only returned on full success. */
  pixels: Float64Array;
  /** Per-pixel bitmask of clipped channels: R=1, G=2, B=4. */
  clipMask: Uint8Array;
  /** False as soon as any pixel was clipped in this or a prior pass. */
  reversible: boolean;
  roundtrip: { maxError: number; meanError: number };
  /** Version of the plan this batch was bound to. */
  version: string;
  chunks: number;
}

export class BatchError extends Error {
  pixelIndex: number;
  constructor(pixelIndex: number, message: string) {
    super(`pixel ${pixelIndex}: ${message}`);
    this.name = 'BatchError';
    this.pixelIndex = pixelIndex;
  }
}

/**
 * Chunked batch conversion. Results are staged privately and only returned
 * after every chunk succeeded: an invalid pixel aborts the whole batch and
 * no partial (half-published) result ever escapes.
 */
export function processBatch(
  input: ArrayLike<number>,
  plan: ConversionPlan,
  fromId: string,
  toId: string,
  options: BatchOptions,
): BatchResult {
  if (input.length % 4 !== 0) {
    throw new BatchError(-1, `input length ${input.length} is not a multiple of 4 (RGBA)`);
  }
  const total = input.length / 4;
  const chunkSize = Math.max(1, Math.floor(options.chunkSize ?? 1024));
  const chunksTotal = Math.max(1, Math.ceil(total / chunkSize));
  const staging = new Float64Array(input.length);
  const clipMask = new Uint8Array(total);
  if (options.priorClipMask) {
    if (options.priorClipMask.length !== total) {
      throw new BatchError(-1, 'priorClipMask length does not match pixel count');
    }
    clipMask.set(options.priorClipMask);
  }

  let maxError = 0;
  let sumError = 0;
  let chunks = 0;

  for (let start = 0; start < total; start += chunkSize) {
    const end = Math.min(total, start + chunkSize);
    for (let p = start; p < end; p++) {
      const i = p * 4;
      const r = input[i];
      const g = input[i + 1];
      const b = input[i + 2];
      const a = input[i + 3];
      if (!Number.isFinite(r) || !Number.isFinite(g) || !Number.isFinite(b) || !Number.isFinite(a)) {
        throw new BatchError(p, 'non-finite component');
      }
      if (a < 0 || a > 1) {
        throw new BatchError(p, `alpha ${a} out of [0, 1]`);
      }

      const converted = plan.convert(fromId, toId, [r, g, b]);
      for (const v of converted) {
        if (!Number.isFinite(v)) throw new BatchError(p, 'conversion produced a non-finite value');
      }

      let out: Vec3 = converted;
      if (options.strategy === 'clip') {
        let bits = 0;
        const clamped: Vec3 = [out[0], out[1], out[2]];
        for (let c = 0; c < 3; c++) {
          if (clamped[c] < 0) {
            clamped[c] = 0;
            bits |= 1 << c;
          } else if (clamped[c] > 1) {
            clamped[c] = 1;
            bits |= 1 << c;
          }
        }
        out = clamped;
        clipMask[p] |= bits;
      }
      // 'preserve': out-of-gamut values (including negatives) pass through.

      staging[i] = out[0];
      staging[i + 1] = out[1];
      staging[i + 2] = out[2];
      staging[i + 3] = a;

      const back = plan.convert(toId, fromId, out);
      const err = Math.max(
        Math.abs(back[0] - r),
        Math.abs(back[1] - g),
        Math.abs(back[2] - b),
      );
      if (err > maxError) maxError = err;
      sumError += err;
    }
    chunks++;
    options.onChunk?.(chunks, chunksTotal);
  }

  let reversible = true;
  for (let p = 0; p < total; p++) {
    if (clipMask[p] !== 0) {
      reversible = false;
      break;
    }
  }

  return {
    pixels: staging,
    clipMask,
    reversible,
    roundtrip: { maxError, meanError: total > 0 ? sumError / total : 0 },
    version: plan.version,
    chunks,
  };
}
