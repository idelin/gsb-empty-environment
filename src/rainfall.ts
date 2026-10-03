import { createGrid, type Grid2D } from "./types.ts";

/**
 * Temporal rainfall series. Each frame holds the rainfall intensity (mm per
 * tick) for every cell during one tick. Frames may be spatially uniform or
 * fully distributed; the series is stepped (frame k covers tick k).
 */
export class RainfallSeries {
  readonly frames: Grid2D[];
  readonly rows: number;
  readonly cols: number;

  constructor(frames: Grid2D[], rows: number, cols: number) {
    for (const frame of frames) {
      if (frame.length !== rows || frame.some((row) => row.length !== cols)) {
        throw new Error("Rainfall frame shape does not match grid shape");
      }
    }
    this.frames = frames.map((frame) => frame.map((row) => row.slice()));
    this.rows = rows;
    this.cols = cols;
  }

  /** Uniform rainfall: same intensity everywhere, varying per tick. */
  static uniform(intensities: number[], rows: number, cols: number): RainfallSeries {
    const frames = intensities.map((mm) => createGrid(rows, cols, mm));
    return new RainfallSeries(frames, rows, cols);
  }

  /** Number of ticks covered by the series. */
  get length(): number {
    return this.frames.length;
  }

  /**
   * Rainfall grid applied during `tick`. Ticks beyond the series return zero
   * rainfall (the storm has passed but water keeps moving and infiltrating).
   */
  intensityAt(tick: number): Grid2D {
    if (tick < 0 || tick >= this.frames.length) {
      return createGrid(this.rows, this.cols, 0);
    }
    return this.frames[tick];
  }
}
