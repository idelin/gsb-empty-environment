import { Dem } from "./dem.ts";
import { RainfallSeries } from "./rainfall.ts";
import { cloneGrid, createGrid, gridShape, type Grid2D, type Mask2D } from "./types.ts";

export interface SimulatorOptions {
  /**
   * Fraction of the water-surface difference exchanged between two adjacent
   * cells per tick, in (0, 0.5]. Larger values spread water faster.
   */
  flowCoeff?: number;
  /** Depth (mm) at or above which a cell counts as flooded. */
  floodThreshold?: number;
  /**
   * When true, cells on the grid boundary drain a `flowCoeff` fraction of
   * their ponded water off the domain each tick (open catchment outlet).
   * Defaults to false (closed basin).
   */
  openBoundary?: boolean;
}

const NEIGHBORS: ReadonlyArray<readonly [number, number]> = [
  [-1, 0],
  [1, 0],
  [0, -1],
  [0, 1],
];

/**
 * Spatiotemporal flood-spread simulation kernel over a 2D raster.
 *
 * Per discrete tick the kernel applies, in order:
 *   1. rainfall input for that tick (mm, time-varying, optionally spatial),
 *   2. infiltration loss (mm/tick per cell, from soil permeability),
 *   3. slope-driven overland flow: water moves between 4-neighbours
 *      proportionally to the difference of water-surface elevation
 *      (terrain elevation + ponded depth), using a stable Jacobi update.
 *
 * Every tick's depth field is snapshotted, so any historical tick can be
 * queried for depths, flooded masks, regions and road breakpoints.
 */
export class FloodSimulator {
  readonly dem: Dem;
  readonly rainfall: RainfallSeries;
  /** Infiltration rate per cell in mm/tick. */
  readonly infiltration: Grid2D;
  readonly flowCoeff: number;
  readonly floodThreshold: number;
  readonly openBoundary: boolean;

  private readonly history: Grid2D[] = [];
  /** DEM elevations converted to millimetres to match depth units. */
  private readonly elevationMm: Grid2D;

  constructor(
    dem: Dem,
    rainfall: RainfallSeries,
    infiltration: Grid2D,
    options: SimulatorOptions = {},
  ) {
    const shape = gridShape(infiltration);
    if (shape.rows !== dem.rows || shape.cols !== dem.cols) {
      throw new Error("Infiltration grid shape must match the DEM");
    }
    if (rainfall.rows !== dem.rows || rainfall.cols !== dem.cols) {
      throw new Error("Rainfall grid shape must match the DEM");
    }
    this.dem = dem;
    this.rainfall = rainfall;
    this.infiltration = infiltration;
    this.flowCoeff = options.flowCoeff ?? 0.25;
    this.floodThreshold = options.floodThreshold ?? 1;
    this.openBoundary = options.openBoundary ?? false;
    if (this.flowCoeff <= 0 || this.flowCoeff > 0.5) {
      throw new Error("flowCoeff must be in (0, 0.5]");
    }
    this.elevationMm = dem.elevation.map((row) => row.map((e) => e * 1000));
    this.history.push(createGrid(dem.rows, dem.cols, 0));
  }

  /** Current tick = number of completed steps. */
  get tick(): number {
    return this.history.length - 1;
  }

  /** Advance the simulation by one tick. */
  step(): void {
    const { rows, cols } = this.dem;
    const depth = cloneGrid(this.history[this.history.length - 1]);
    const rain = this.rainfall.intensityAt(this.tick);

    for (let r = 0; r < rows; r++) {
      for (let c = 0; c < cols; c++) {
        depth[r][c] = Math.max(0, depth[r][c] + rain[r][c] - this.infiltration[r][c]);
      }
    }

    // Slope-driven overland flow. Transfers are computed from the pre-flow
    // snapshot (Jacobi update). Each pairwise transfer is capped at half the
    // surface difference (no oscillation); afterwards every cell's outgoing
    // transfers are scaled down proportionally when their sum exceeds the
    // ponded depth, so a cell can never give away water it does not have
    // and total volume is conserved exactly.
    const transfers: Array<{
      fromR: number;
      fromC: number;
      toR: number;
      toC: number;
      amount: number;
    }> = [];
    const outflow = createGrid(rows, cols, 0);
    for (let r = 0; r < rows; r++) {
      for (let c = 0; c < cols; c++) {
        if (depth[r][c] <= 0) continue;
        const surfaceHere = this.elevationMm[r][c] + depth[r][c];
        for (const [dr, dc] of NEIGHBORS) {
          const nr = r + dr;
          const nc = c + dc;
          if (!this.dem.contains(nr, nc)) continue;
          const surfaceThere = this.elevationMm[nr][nc] + depth[nr][nc];
          const diff = surfaceHere - surfaceThere;
          if (diff <= 0) continue;
          const amount = Math.min(this.flowCoeff * diff, diff / 2);
          transfers.push({ fromR: r, fromC: c, toR: nr, toC: nc, amount });
          outflow[r][c] += amount;
        }
      }
    }
    const scale = createGrid(rows, cols, 1);
    for (let r = 0; r < rows; r++) {
      for (let c = 0; c < cols; c++) {
        if (outflow[r][c] > depth[r][c]) {
          scale[r][c] = depth[r][c] / outflow[r][c];
        }
      }
    }
    for (const t of transfers) {
      const amount = t.amount * scale[t.fromR][t.fromC];
      depth[t.fromR][t.fromC] -= amount;
      depth[t.toR][t.toC] += amount;
    }

    if (this.openBoundary) {
      for (let r = 0; r < rows; r++) {
        for (let c = 0; c < cols; c++) {
          if (r === 0 || r === rows - 1 || c === 0 || c === cols - 1) {
            depth[r][c] *= 1 - this.flowCoeff;
          }
        }
      }
    }

    this.history.push(depth);
  }

  /** Advance the simulation by `ticks` ticks. */
  run(ticks: number): void {
    for (let i = 0; i < ticks; i++) this.step();
  }

  /** Water depth field (mm) at any simulated historical tick. */
  depthAt(tick: number): Grid2D {
    if (tick < 0 || tick > this.tick) {
      throw new RangeError(`tick ${tick} outside simulated range 0..${this.tick}`);
    }
    return cloneGrid(this.history[tick]);
  }

  /** Boolean flooded mask at a historical tick. */
  floodedMaskAt(tick: number, threshold = this.floodThreshold): Mask2D {
    const depth = this.history[tick];
    if (depth === undefined) {
      throw new RangeError(`tick ${tick} outside simulated range 0..${this.tick}`);
    }
    return depth.map((row) => row.map((d) => d >= threshold));
  }

  /** Total ponded water volume (mm * cells) at a historical tick. */
  totalDepthAt(tick: number): number {
    const depth = this.history[tick];
    if (depth === undefined) {
      throw new RangeError(`tick ${tick} outside simulated range 0..${this.tick}`);
    }
    let total = 0;
    for (const row of depth) for (const d of row) total += d;
    return total;
  }
}
