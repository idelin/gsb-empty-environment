import type { Pipeline } from './pipeline.ts';

export interface BatchOptions {
  /** Pixels per chunk; defaults to 4096. */
  chunkSize?: number;
  /** Measure round-trip error through the reverse pipeline; default true. */
  measureRoundTrip?: boolean;
  /** Reject non-finite input components; default true. */
  strict?: boolean;
  /**
   * Clip mask accumulated by earlier passes. Clipping is irreversible, so
   * any pixel marked here stays marked and the batch can never report
   * itself reversible — repeated clipping must not claim reversibility.
   */
  priorMask?: Uint8Array | null;
  /** Tolerance for declaring a round trip reversible; default 1e-6. */
  roundTripTolerance?: number;
}

export interface RoundTripStats {
  maxError: number;
  meanError: number;
}

export interface BatchResult {
  /** RGBA float output in [0,1]-ish encoded target space (may exceed [0,1] with 'preserve'). */
  output: Float32Array;
  /** 1 per pixel that was irreversibly clipped (includes priorMask bits). */
  clipMask: Uint8Array;
  clippedPixels: number;
  /** False whenever any pixel was clipped in this or an earlier pass. */
  reversible: boolean;
  roundTrip: RoundTripStats | null;
  version: string;
  chunks: number;
}

export class BatchError extends Error {
  readonly failedChunk: number;

  constructor(message: string, failedChunk: number) {
    super(message);
    this.name = 'BatchError';
    this.failedChunk = failedChunk;
  }
}

/**
 * Processes an RGBA pixel buffer through a pipeline in chunks.
 * All-or-nothing: if any chunk fails, a BatchError is thrown and no
 * partially written output is ever published.
 * Alpha is passed through untouched; fully transparent pixels are still
 * converted (and must stay finite) but keep their exact alpha value.
 */
export function processBatch(
  pipeline: Pipeline,
  input: Uint8ClampedArray | Float32Array,
  options: BatchOptions = {},
): BatchResult {
  if (input.length % 4 !== 0) {
    throw new BatchError(`input length ${input.length} is not a multiple of 4 (RGBA)`, -1);
  }
  const pixelCount = input.length / 4;
  const chunkSize = options.chunkSize ?? 4096;
  if (!(chunkSize > 0)) throw new BatchError(`invalid chunkSize ${chunkSize}`, -1);
  const strict = options.strict ?? true;
  const measureRoundTrip = options.measureRoundTrip ?? true;
  const tolerance = options.roundTripTolerance ?? 1e-6;
  const isBytes = input instanceof Uint8ClampedArray;
  const priorMask = options.priorMask ?? null;
  if (priorMask !== null && priorMask.length !== pixelCount) {
    throw new BatchError(`priorMask length ${priorMask.length} does not match pixel count ${pixelCount}`, -1);
  }

  // Private scratch buffers: nothing is published until every chunk passes.
  const output = new Float32Array(input.length);
  const clipMask = new Uint8Array(pixelCount);
  let clippedPixels = 0;
  const chunks = pixelCount === 0 ? 0 : Math.ceil(pixelCount / chunkSize);

  for (let chunk = 0; chunk < chunks; chunk++) {
    const start = chunk * chunkSize;
    const end = Math.min(start + chunkSize, pixelCount);
    try {
      for (let p = start; p < end; p++) {
        const o = p * 4;
        const r = isBytes ? input[o] / 255 : input[o];
        const g = isBytes ? input[o + 1] / 255 : input[o + 1];
        const b = isBytes ? input[o + 2] / 255 : input[o + 2];
        const a = isBytes ? input[o + 3] / 255 : input[o + 3];
        if (strict && (!Number.isFinite(r) || !Number.isFinite(g) || !Number.isFinite(b) || !Number.isFinite(a))) {
          throw new BatchError(`non-finite component at pixel ${p}`, chunk);
        }
        const { rgb, clipped } = pipeline.convertPixel(r, g, b);
        output[o] = rgb[0];
        output[o + 1] = rgb[1];
        output[o + 2] = rgb[2];
        output[o + 3] = a;
        if (clipped) {
          clipMask[p] = 1;
          clippedPixels++;
        }
      }
    } catch (err) {
      if (err instanceof BatchError) throw err;
      const reason = err instanceof Error ? err.message : String(err);
      throw new BatchError(`chunk ${chunk} failed: ${reason}`, chunk);
    }
  }

  // Merge prior clip history: irreversibility is cumulative.
  if (priorMask !== null) {
    for (let p = 0; p < pixelCount; p++) {
      if (priorMask[p] !== 0 && clipMask[p] === 0) {
        clipMask[p] = 1;
        clippedPixels++;
      }
    }
  }

  let roundTrip: RoundTripStats | null = null;
  if (measureRoundTrip && pixelCount > 0) {
    const reverse = pipeline.reverse();
    let maxError = 0;
    let sumError = 0;
    for (let p = 0; p < pixelCount; p++) {
      const o = p * 4;
      const back = reverse.convertPixel(output[o], output[o + 1], output[o + 2]).rgb;
      for (let c = 0; c < 3; c++) {
        const original = isBytes ? input[o + c] / 255 : input[o + c];
        const err = Math.abs(back[c] - original);
        if (err > maxError) maxError = err;
        sumError += err;
      }
    }
    roundTrip = { maxError, meanError: sumError / (pixelCount * 3) };
  }

  const roundTripOk = roundTrip === null || roundTrip.maxError <= tolerance;
  const reversible = clippedPixels === 0 && roundTripOk;

  return {
    output,
    clipMask,
    clippedPixels,
    reversible,
    roundTrip,
    version: pipeline.version,
    chunks,
  };
}
