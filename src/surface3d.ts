/**
 * 3D isosurface extraction via Marching Tetrahedra over an SdfGrid3D.
 * Each cube cell is decomposed into 6 tetrahedra; every tetrahedron emits
 * 0, 1 or 2 triangles where the interpolated field crosses `iso`.
 * Vertices are welded so the resulting mesh is connected.
 */
import type { Vec3 } from './vec.ts';
import type { SdfGrid3D } from './grid.ts';

export interface Mesh {
  positions: Vec3[];
  /** Triples of vertex indices, wound CCW seen from outside (positive side). */
  triangles: Array<readonly [number, number, number]>;
}

/** Cube corner offsets, index 0..7. */
const CORNER: ReadonlyArray<Vec3> = [
  [0, 0, 0], [1, 0, 0], [1, 1, 0], [0, 1, 0],
  [0, 0, 1], [1, 0, 1], [1, 1, 1], [0, 1, 1],
];

/** Decomposition of the cube into 6 tetrahedra (corner indices). */
const TETS: ReadonlyArray<readonly [number, number, number, number]> = [
  [0, 5, 1, 6],
  [0, 1, 2, 6],
  [0, 2, 3, 6],
  [0, 3, 7, 6],
  [0, 7, 4, 6],
  [0, 4, 5, 6],
];

export function extractSurface3D(grid: SdfGrid3D, iso = 0): Mesh {
  const { nx, ny, nz, bounds, cellX, cellY, cellZ } = grid;
  const positions: Vec3[] = [];
  const triangles: Array<readonly [number, number, number]> = [];
  const welded = new Map<string, number>();

  /** Append a triangle, flipping its winding so the normal points toward
   *  the positive (outside) side of the field at the triangle centroid. */
  const pushTriangle = (a: number, b: number, c: number): void => {
    const pa = positions[a];
    const pb = positions[b];
    const pc = positions[c];
    const ux = pb[0] - pa[0], uy = pb[1] - pa[1], uz = pb[2] - pa[2];
    const vx = pc[0] - pa[0], vy = pc[1] - pa[1], vz = pc[2] - pa[2];
    const nx_ = uy * vz - uz * vy;
    const ny_ = uz * vx - ux * vz;
    const nz_ = ux * vy - uy * vx;
    const g = grid.gradient([
      (pa[0] + pb[0] + pc[0]) / 3,
      (pa[1] + pb[1] + pc[1]) / 3,
      (pa[2] + pb[2] + pc[2]) / 3,
    ]);
    if (nx_ * g[0] + ny_ * g[1] + nz_ * g[2] >= 0) triangles.push([a, b, c]);
    else triangles.push([a, c, b]);
  };

  const vertexIndex = (p: Vec3): number => {
    const key = `${p[0].toFixed(6)},${p[1].toFixed(6)},${p[2].toFixed(6)}`;
    const existing = welded.get(key);
    if (existing !== undefined) return existing;
    const idx = positions.length;
    positions.push(p);
    welded.set(key, idx);
    return idx;
  };

  // Per-tetrahedron scratch buffers.
  const tetPos: Vec3[] = [[0, 0, 0], [0, 0, 0], [0, 0, 0], [0, 0, 0]];
  const tetVal = [0, 0, 0, 0];

  /** Interpolated crossing on edge a-b of the current tetrahedron. */
  const crossing = (a: number, b: number): Vec3 => {
    const t = (iso - tetVal[a]) / (tetVal[b] - tetVal[a]);
    const pa = tetPos[a];
    const pb = tetPos[b];
    return [
      pa[0] + (pb[0] - pa[0]) * t,
      pa[1] + (pb[1] - pa[1]) * t,
      pa[2] + (pb[2] - pa[2]) * t,
    ];
  };

  const emitTetrahedron = (): void => {
    let mask = 0;
    for (let c = 0; c < 4; c++) if (tetVal[c] < iso) mask |= 1 << c;
    if (mask === 0 || mask === 15) return;

    const inside: number[] = [];
    const outside: number[] = [];
    for (let c = 0; c < 4; c++) (mask & (1 << c) ? inside : outside).push(c);

    if (inside.length === 1 || inside.length === 3) {
      // One vertex on the minority side -> single triangle on its 3 edges.
      const [solo] = inside.length === 1 ? inside : outside;
      const others = inside.length === 1 ? outside : inside;
      const p0 = vertexIndex(crossing(solo, others[0]));
      const p1 = vertexIndex(crossing(solo, others[1]));
      const p2 = vertexIndex(crossing(solo, others[2]));
      pushTriangle(p0, p1, p2);
    } else {
      // Two inside, two outside -> quad split into two triangles.
      const [i0, i1] = inside;
      const [o0, o1] = outside;
      const a = vertexIndex(crossing(i0, o0));
      const b = vertexIndex(crossing(i0, o1));
      const c = vertexIndex(crossing(i1, o0));
      const d = vertexIndex(crossing(i1, o1));
      pushTriangle(a, d, c);
      pushTriangle(a, b, d);
    }
  };

  for (let k = 0; k < nz - 1; k++) {
    for (let j = 0; j < ny - 1; j++) {
      for (let i = 0; i < nx - 1; i++) {
        const cubePos: Vec3[] = new Array(8);
        const cubeVal: number[] = new Array(8);
        for (let c = 0; c < 8; c++) {
          const [di, dj, dk] = CORNER[c];
          cubePos[c] = [
            bounds.min[0] + (i + di) * cellX,
            bounds.min[1] + (j + dj) * cellY,
            bounds.min[2] + (k + dk) * cellZ,
          ];
          cubeVal[c] = grid.at(i + di, j + dj, k + dk);
        }
        let straddles = false;
        for (let c = 1; c < 8; c++) {
          if ((cubeVal[c] < iso) !== (cubeVal[0] < iso)) { straddles = true; break; }
        }
        if (!straddles) continue;

        for (const tet of TETS) {
          for (let c = 0; c < 4; c++) {
            tetPos[c] = cubePos[tet[c]];
            tetVal[c] = cubeVal[tet[c]];
          }
          emitTetrahedron();
        }
      }
    }
  }
  return { positions, triangles };
}
