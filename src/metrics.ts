/** Compute + memory accounting. All buffers are tracked deterministically:
 *  every allocation is paired with an explicit release, so the peak is exact. */
export class Metrics {
  ops = 0; // multiply-accumulate count
}

export class BudgetExceededError extends Error {
  readonly requiredBytes: number;
  readonly budgetBytes: number;

  constructor(
    message: string,
    requiredBytes: number,
    budgetBytes: number,
  ) {
    super(message);
    this.name = 'BudgetExceededError';
    this.requiredBytes = requiredBytes;
    this.budgetBytes = budgetBytes;
  }
}

export class MemTracker {
  current = 0;
  peak = 0;
  readonly hardLimitBytes?: number;

  constructor(hardLimitBytes?: number) {
    this.hardLimitBytes = hardLimitBytes;
  }

  alloc(bytes: number): void {
    this.current += bytes;
    if (this.current > this.peak) this.peak = this.current;
    if (this.hardLimitBytes !== undefined && this.current > this.hardLimitBytes) {
      throw new BudgetExceededError(
        `working memory ${this.current} B exceeds budget ${this.hardLimitBytes} B`,
        this.current,
        this.hardLimitBytes,
      );
    }
  }

  release(bytes: number): void {
    this.current -= bytes;
    if (this.current < 0) throw new Error('MemTracker: negative balance');
  }
}

export const CHANNELS = 4; // RGBA
export const BYTES_PER_PIXEL = CHANNELS * Float32Array.BYTES_PER_ELEMENT;

export function bufferBytes(pixels: number): number {
  return pixels * BYTES_PER_PIXEL;
}

export function allocBuffer(pixels: number, tracker?: MemTracker): Float32Array {
  tracker?.alloc(bufferBytes(pixels));
  return new Float32Array(pixels * CHANNELS);
}

export function releaseBuffer(buf: Float32Array, tracker?: MemTracker): void {
  tracker?.release(buf.byteLength);
}
