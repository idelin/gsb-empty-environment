import type { Point } from './geometry.ts';

/**
 * Compact triangle topology mesh.
 * - `triangles[i]` lists the three vertex indices of triangle i in CCW order.
 * - `neighbors[i][k]` is the triangle adjacent across the edge opposite
 *   `triangles[i][k]`, or -1 when that edge lies on the outer boundary.
 * - `hull` lists the convex-hull vertex indices in CCW order.
 */
export interface TriangleMesh {
  points: Point[];
  triangles: [number, number, number][];
  neighbors: [number, number, number][];
  hull: number[];
}

/** Build the compact adjacency mesh from a point set and CCW triangle list. */
export function buildMesh(
  points: Point[],
  triangles: [number, number, number][],
): TriangleMesh {
  const neighbors: [number, number, number][] = triangles.map(
    () => [-1, -1, -1] as [number, number, number],
  );

  // Stitch triangles through shared (undirected) edges.
  const edgeOwner = new Map<string, { t: number; k: number }>();
  for (let t = 0; t < triangles.length; t++) {
    const tri = triangles[t];
    for (let k = 0; k < 3; k++) {
      const a = tri[(k + 1) % 3];
      const b = tri[(k + 2) % 3];
      const key = a < b ? `${a}_${b}` : `${b}_${a}`;
      const prev = edgeOwner.get(key);
      if (prev === undefined) {
        edgeOwner.set(key, { t, k });
      } else {
        neighbors[t][k] = prev.t;
        neighbors[prev.t][prev.k] = t;
      }
    }
  }

  // Boundary edges (exactly one incident triangle) form the convex hull.
  // Each is directed so the triangle interior stays on its left, hence
  // chaining them yields the hull loop in CCW order.
  const nextVertex = new Map<number, number>();
  for (let t = 0; t < triangles.length; t++) {
    const tri = triangles[t];
    for (let k = 0; k < 3; k++) {
      if (neighbors[t][k] === -1) {
        nextVertex.set(tri[(k + 1) % 3], tri[(k + 2) % 3]);
      }
    }
  }

  const hull: number[] = [];
  if (nextVertex.size > 0) {
    const start = nextVertex.keys().next().value as number;
    let cur = start;
    do {
      hull.push(cur);
      cur = nextVertex.get(cur) as number;
    } while (cur !== start && hull.length <= points.length);
  } else if (points.length > 0 && points.length < 3) {
    for (let i = 0; i < points.length; i++) hull.push(i);
  }

  return { points: points.slice(), triangles, neighbors, hull };
}
