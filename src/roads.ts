import type { BreakpointSegment, CellCoord, Grid2D, Point } from "./types.ts";

/**
 * Supercover line rasterization: every grid cell the segment from `a` to `b`
 * passes through, in travel order. Coordinates are continuous grid units
 * (x = col, y = row); cell (row, col) covers [col, col+1) x [row, row+1).
 */
export function rasterizeSegment(a: Point, b: Point): CellCoord[] {
  const cells: CellCoord[] = [];
  const dx = b.x - a.x;
  const dy = b.y - a.y;

  let col = Math.floor(a.x);
  let row = Math.floor(a.y);
  const endCol = Math.floor(b.x);
  const endRow = Math.floor(b.y);
  cells.push({ row, col });

  const stepX = dx > 0 ? 1 : -1;
  const stepY = dy > 0 ? 1 : -1;
  const tDeltaX = dx === 0 ? Infinity : Math.abs(1 / dx);
  const tDeltaY = dy === 0 ? Infinity : Math.abs(1 / dy);
  const boundaryX = dx > 0 ? col + 1 : col;
  const boundaryY = dy > 0 ? row + 1 : row;
  let tMaxX = dx === 0 ? Infinity : (boundaryX - a.x) / dx;
  let tMaxY = dy === 0 ? Infinity : (boundaryY - a.y) / dy;

  while (col !== endCol || row !== endRow) {
    if (tMaxX < tMaxY) {
      tMaxX += tDeltaX;
      col += stepX;
    } else if (tMaxY < tMaxX) {
      tMaxY += tDeltaY;
      row += stepY;
    } else {
      // Exactly crossing a grid vertex: enter the diagonally opposite cell.
      tMaxX += tDeltaX;
      tMaxY += tDeltaY;
      col += stepX;
      row += stepY;
    }
    cells.push({ row, col });
  }
  return cells;
}

/** Rasterize a polyline road into the ordered list of cells it crosses. */
export function rasterizeRoad(waypoints: Point[]): CellCoord[] {
  if (waypoints.length < 2) {
    throw new Error("A road needs at least two waypoints");
  }
  const cells: CellCoord[] = [];
  for (let i = 0; i < waypoints.length - 1; i++) {
    const segment = rasterizeSegment(waypoints[i], waypoints[i + 1]);
    for (const cell of segment) {
      const last = cells[cells.length - 1];
      if (last === undefined || last.row !== cell.row || last.col !== cell.col) {
        cells.push(cell);
      }
    }
  }
  return cells;
}

export interface Road {
  id: string;
  cells: CellCoord[];
}

/** A set of roads rasterized onto the simulation grid. */
export class RoadNetwork {
  readonly roads: Road[];

  constructor(definitions: Array<{ id: string; waypoints: Point[] }>) {
    this.roads = definitions.map((def) => ({
      id: def.id,
      cells: rasterizeRoad(def.waypoints),
    }));
  }

  /**
   * Cells of every road that are water-blocked at a historical tick, i.e.
   * whose ponded depth reaches `threshold` (mm). Consecutive blocked cells
   * are merged into one breakpoint segment per maximal run.
   */
  breakpointsAt(depth: Grid2D, tick: number, threshold: number): BreakpointSegment[] {
    const segments: BreakpointSegment[] = [];
    for (const road of this.roads) {
      let current: BreakpointSegment | undefined;
      for (const cell of road.cells) {
        const rowData = depth[cell.row];
        const d = rowData === undefined ? 0 : rowData[cell.col] ?? 0;
        if (d >= threshold) {
          if (current === undefined) {
            current = { roadId: road.id, tick, cells: [], maxDepth: 0 };
            segments.push(current);
          }
          current.cells.push(cell);
          current.maxDepth = Math.max(current.maxDepth, d);
        } else {
          current = undefined;
        }
      }
    }
    return segments;
  }
}
