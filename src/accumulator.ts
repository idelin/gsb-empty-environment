import { TileGrid, type GridSpec } from './grid.ts';
import { SpatioTemporalCube } from './cube.ts';
import { topK } from './topk.ts';

export const MS_PER_HOUR = 3_600_000;

export interface Hotspot {
  rank: number;
  cell: number;
  lat: number;
  lng: number;
  weight: number;
}

export interface AnalyzerOptions {
  grid: GridSpec;
  /** Maximum number of hourly layers retained (memory bound). */
  capacityHours?: number;
}

/**
 * Streaming spatio-temporal density analyzer.
 *
 * Ingests trajectory points in O(1), accumulates weights into an hourly
 * data cube over a tile grid, and answers Top-K hotspot queries for any
 * retained time window in O(hoursInWindow * cells + cells * log k).
 */
export class DensityAnalyzer {
  readonly grid: TileGrid;
  readonly cube: SpatioTemporalCube;
  private readonly scratch: Float64Array;
  /** Points dropped because they fell outside the grid bbox. */
  dropped = 0;

  constructor(options: AnalyzerOptions) {
    this.grid = new TileGrid(options.grid);
    const capacityHours = options.capacityHours ?? 24 * 31; // ~1 month
    this.cube = new SpatioTemporalCube(this.grid.cellCount, capacityHours);
    this.scratch = new Float64Array(this.grid.cellCount);
  }

  /** Ingest one trajectory point. Returns false when outside the bbox. */
  addPoint(lat: number, lng: number, timestampMs: number, weight = 1): boolean {
    const cell = this.grid.cellOf(lat, lng);
    if (cell < 0) {
      this.dropped++;
      return false;
    }
    const hourBucket = Math.floor(timestampMs / MS_PER_HOUR);
    this.cube.add(cell, hourBucket, weight);
    return true;
  }

  /**
   * Top-K densest tile centers within [startMs, endMs).
   * Results are sorted by weight descending.
   */
  queryTopK(startMs: number, endMs: number, k: number): Hotspot[] {
    const startHour = Math.floor(startMs / MS_PER_HOUR);
    const endHourExclusive = Math.floor((endMs - 1) / MS_PER_HOUR) + 1;
    this.cube.windowSums(startHour, endHourExclusive, this.scratch);
    return topK(this.scratch, k).map((entry, i) => {
      const { lat, lng } = this.grid.centerOf(entry.cell);
      return { rank: i + 1, cell: entry.cell, lat, lng, weight: entry.weight };
    });
  }

  stats(): {
    pointsAdded: number;
    dropped: number;
    hoursStored: number;
    evictions: number;
    cubeMemoryBytes: number;
    cells: number;
  } {
    return {
      pointsAdded: this.cube.pointsAdded,
      dropped: this.dropped,
      hoursStored: this.cube.hoursStored,
      evictions: this.cube.evictions,
      cubeMemoryBytes: this.cube.memoryBytes,
      cells: this.grid.cellCount,
    };
  }
}
