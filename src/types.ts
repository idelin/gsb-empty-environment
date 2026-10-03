/** A 2D raster of numbers, indexed [row][col]. */
export type Grid2D = number[][];

/** A 2D raster of booleans, indexed [row][col]. */
export type Mask2D = boolean[][];

/** Integer cell coordinate inside the grid. */
export interface CellCoord {
  row: number;
  col: number;
}

/** Continuous 2D point in grid coordinates (x = col, y = row). */
export interface Point {
  x: number;
  y: number;
}

/** A closed boundary loop of a flooded region. */
export interface Polygon {
  /** Ordered vertices (grid units unless scaled). The loop is implicitly closed. */
  vertices: Point[];
  /** True when this loop encloses a dry hole inside a flooded region. */
  isHole: boolean;
  /** Absolute area in squared grid units. */
  area: number;
}

/** A maximal run of consecutive blocked cells along one road. */
export interface BreakpointSegment {
  roadId: string;
  tick: number;
  /** All blocked cells of the segment, in road order. */
  cells: CellCoord[];
  /** Maximum water depth (mm) observed along the segment. */
  maxDepth: number;
}

export function createGrid(rows: number, cols: number, fill = 0): Grid2D {
  return Array.from({ length: rows }, () => new Array<number>(cols).fill(fill));
}

export function cloneGrid(grid: Grid2D): Grid2D {
  return grid.map((row) => row.slice());
}

export function gridShape(grid: Grid2D): { rows: number; cols: number } {
  return { rows: grid.length, cols: grid.length === 0 ? 0 : grid[0].length };
}
