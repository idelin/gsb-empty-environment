import type { CellCoord, Mask2D, Point, Polygon } from "./types.ts";

const DIRS: ReadonlyArray<readonly [number, number]> = [
  [-1, 0],
  [1, 0],
  [0, -1],
  [0, 1],
];

/**
 * Label 4-connected flooded regions with an iterative flood fill.
 * Returns regions in scan order, each as a list of cell coordinates.
 */
export function connectedRegions(mask: Mask2D): CellCoord[][] {
  const rows = mask.length;
  const cols = rows === 0 ? 0 : mask[0].length;
  const visited: Mask2D = Array.from({ length: rows }, () =>
    new Array<boolean>(cols).fill(false),
  );
  const regions: CellCoord[][] = [];

  for (let r = 0; r < rows; r++) {
    for (let c = 0; c < cols; c++) {
      if (!mask[r][c] || visited[r][c]) continue;
      const region: CellCoord[] = [];
      const stack: CellCoord[] = [{ row: r, col: c }];
      visited[r][c] = true;
      while (stack.length > 0) {
        const cell = stack.pop() as CellCoord;
        region.push(cell);
        for (const [dr, dc] of DIRS) {
          const nr = cell.row + dr;
          const nc = cell.col + dc;
          if (nr < 0 || nr >= rows || nc < 0 || nc >= cols) continue;
          if (!mask[nr][nc] || visited[nr][nc]) continue;
          visited[nr][nc] = true;
          stack.push({ row: nr, col: nc });
        }
      }
      regions.push(region);
    }
  }
  return regions;
}

interface DirectedEdge {
  from: Point;
  to: Point;
}

function key(p: Point): string {
  return `${p.x},${p.y}`;
}

/**
 * Boundary edges of one flooded cell, oriented so the flooded interior stays
 * on the left of the travel direction (grid coordinates, y pointing down).
 * With this convention outer loops get a negative signed area and hole loops
 * a positive one.
 */
function cellBoundaryEdges(row: number, col: number, mask: Mask2D): DirectedEdge[] {
  const rows = mask.length;
  const cols = mask[0].length;
  const isDry = (r: number, c: number): boolean =>
    r < 0 || r >= rows || c < 0 || c >= cols || !mask[r][c];
  const edges: DirectedEdge[] = [];
  if (isDry(row - 1, col)) {
    edges.push({ from: { x: col + 1, y: row }, to: { x: col, y: row } });
  }
  if (isDry(row + 1, col)) {
    edges.push({ from: { x: col, y: row + 1 }, to: { x: col + 1, y: row + 1 } });
  }
  if (isDry(row, col - 1)) {
    edges.push({ from: { x: col, y: row }, to: { x: col, y: row + 1 } });
  }
  if (isDry(row, col + 1)) {
    edges.push({ from: { x: col + 1, y: row + 1 }, to: { x: col + 1, y: row } });
  }
  return edges;
}

function signedArea(vertices: Point[]): number {
  let sum = 0;
  for (let i = 0; i < vertices.length; i++) {
    const a = vertices[i];
    const b = vertices[(i + 1) % vertices.length];
    sum += a.x * b.y - b.x * a.y;
  }
  return sum / 2;
}

/** Drop vertices that lie on the straight segment between their neighbours. */
function removeCollinear(vertices: Point[]): Point[] {
  const kept: Point[] = [];
  for (let i = 0; i < vertices.length; i++) {
    const prev = vertices[(i - 1 + vertices.length) % vertices.length];
    const curr = vertices[i];
    const next = vertices[(i + 1) % vertices.length];
    const cross =
      (curr.x - prev.x) * (next.y - curr.y) - (curr.y - prev.y) * (next.x - curr.x);
    if (cross !== 0) kept.push(curr);
  }
  return kept;
}

/**
 * Trace the boundary polygons of one connected flooded region.
 * Returns the outer loop plus one loop per enclosed dry hole.
 * Vertices are in grid units (multiply by cellSize for meters).
 */
export function boundaryPolygons(mask: Mask2D, region: CellCoord[]): Polygon[] {
  const outgoing = new Map<string, DirectedEdge[]>();
  for (const cell of region) {
    for (const edge of cellBoundaryEdges(cell.row, cell.col, mask)) {
      const k = key(edge.from);
      const bucket = outgoing.get(k);
      if (bucket === undefined) {
        outgoing.set(k, [edge]);
      } else {
        bucket.push(edge);
      }
    }
  }

  const polygons: Polygon[] = [];
  for (const bucket of outgoing.values()) {
    while (bucket.length > 0) {
      const first = bucket.pop() as DirectedEdge;
      const vertices: Point[] = [first.from, first.to];
      let cursor = first.to;
      while (cursor.x !== first.from.x || cursor.y !== first.from.y) {
        const nextBucket = outgoing.get(key(cursor));
        if (nextBucket === undefined || nextBucket.length === 0) {
          throw new Error("Broken boundary: dangling edge chain");
        }
        const next = nextBucket.pop() as DirectedEdge;
        vertices.push(next.to);
        cursor = next.to;
      }
      vertices.pop(); // last vertex equals the first; loop is implicitly closed
      const simplified = removeCollinear(vertices);
      const area = signedArea(simplified);
      polygons.push({ vertices: simplified, isHole: area > 0, area: Math.abs(area) });
    }
  }
  polygons.sort((a, b) => b.area - a.area);
  return polygons;
}

/**
 * Convenience: all flooded regions of a mask with their boundary polygons.
 */
export function analyzeMask(
  mask: Mask2D,
): Array<{ cells: CellCoord[]; polygons: Polygon[] }> {
  return connectedRegions(mask).map((cells) => ({
    cells,
    polygons: boundaryPolygons(mask, cells),
  }));
}
