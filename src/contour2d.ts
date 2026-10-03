/**
 * 2D isocontour extraction via Marching Squares over an SdfGrid2D.
 * Produces line segments where the interpolated field crosses `iso`.
 */
import type { Vec2 } from './vec.ts';
import type { SdfGrid2D } from './grid.ts';

export type Segment2 = readonly [Vec2, Vec2];

/** Edge ids: 0 = bottom, 1 = right, 2 = top, 3 = left. */
const TABLE: ReadonlyArray<ReadonlyArray<readonly [number, number]>> = [
  [],                 // 0
  [[3, 0]],           // 1
  [[0, 1]],           // 2
  [[3, 1]],           // 3
  [[1, 2]],           // 4
  [],                 // 5  saddle, resolved at runtime
  [[0, 2]],           // 6
  [[3, 2]],           // 7
  [[3, 2]],           // 8
  [[0, 2]],           // 9
  [],                 // 10 saddle, resolved at runtime
  [[1, 2]],           // 11
  [[3, 1]],           // 12
  [[0, 1]],           // 13
  [[3, 0]],           // 14
  [],                 // 15
];

export function extractContour2D(grid: SdfGrid2D, iso = 0): Segment2[] {
  const { nx, ny, bounds, cellX, cellY } = grid;
  const segments: Segment2[] = [];

  const cornerPos = (i: number, j: number): Vec2 => [
    bounds.min[0] + i * cellX,
    bounds.min[1] + j * cellY,
  ];

  for (let j = 0; j < ny - 1; j++) {
    for (let i = 0; i < nx - 1; i++) {
      const v = [grid.at(i, j), grid.at(i + 1, j), grid.at(i + 1, j + 1), grid.at(i, j + 1)];
      let mask = 0;
      for (let c = 0; c < 4; c++) if (v[c] < iso) mask |= 1 << c;
      if (mask === 0 || mask === 15) continue;

      // Corner layout: 0=(i,j) 1=(i+1,j) 2=(i+1,j+1) 3=(i,j+1)
      const pos = [cornerPos(i, j), cornerPos(i + 1, j), cornerPos(i + 1, j + 1), cornerPos(i, j + 1)];
      // Edge endpoints as corner indices.
      const EDGE: ReadonlyArray<readonly [number, number]> = [[0, 1], [1, 2], [3, 2], [0, 3]];
      const pointOnEdge = (e: number): Vec2 => {
        const [a, b] = EDGE[e];
        const t = (iso - v[a]) / (v[b] - v[a]);
        return [pos[a][0] + (pos[b][0] - pos[a][0]) * t, pos[a][1] + (pos[b][1] - pos[a][1]) * t];
      };

      let pairs = TABLE[mask];
      if (mask === 5 || mask === 10) {
        // Saddle: disambiguate by the cell-centre value.
        const center = (v[0] + v[1] + v[2] + v[3]) / 4;
        const centerInside = center < iso;
        pairs = (mask === 5) === centerInside
          ? [[3, 2], [0, 1]]
          : [[3, 0], [1, 2]];
      }
      for (const [e0, e1] of pairs) {
        segments.push([pointOnEdge(e0), pointOnEdge(e1)]);
      }
    }
  }
  return segments;
}
