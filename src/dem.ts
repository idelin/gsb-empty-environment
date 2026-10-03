import { createGrid, gridShape, type Grid2D } from "./types.ts";

/**
 * Digital Elevation Model: a regular raster of terrain elevations (meters).
 */
export class Dem {
  readonly rows: number;
  readonly cols: number;
  /** Real-world size of one cell edge (meters). */
  readonly cellSize: number;
  /** Elevation per cell in meters. */
  readonly elevation: Grid2D;

  constructor(elevation: Grid2D, cellSize = 1) {
    const { rows, cols } = gridShape(elevation);
    if (rows === 0 || cols === 0) {
      throw new Error("DEM must be a non-empty rectangular grid");
    }
    for (const row of elevation) {
      if (row.length !== cols) {
        throw new Error("DEM rows must all have the same length");
      }
    }
    this.rows = rows;
    this.cols = cols;
    this.cellSize = cellSize;
    this.elevation = elevation.map((row) => row.slice());
  }

  static constant(rows: number, cols: number, elevation: number, cellSize = 1): Dem {
    return new Dem(createGrid(rows, cols, elevation), cellSize);
  }

  contains(row: number, col: number): boolean {
    return row >= 0 && row < this.rows && col >= 0 && col < this.cols;
  }

  elevationAt(row: number, col: number): number {
    return this.elevation[row][col];
  }
}
