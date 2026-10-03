import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  DelaunayTriangulator,
  circumcircle,
  orient2d,
  inCircle,
  dist,
} from '../src/index.ts';
import type { Point, TriangleMesh } from '../src/index.ts';

function mulberry32(seed: number): () => number {
  let s = seed >>> 0;
  return () => {
    s = (s + 0x6d2b79f5) >>> 0;
    let t = s;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function randomPoints(n: number, seed: number, range = 1000): Point[] {
  const rng = mulberry32(seed);
  return Array.from({ length: n }, () => ({
    x: rng() * range,
    y: rng() * range,
  }));
}

/** Every triangle's circumcircle must be free of any fourth point. */
function assertEmptyCircumcircle(mesh: TriangleMesh): void {
  for (const [a, b, c] of mesh.triangles) {
    const { center, radius } = circumcircle(
      mesh.points[a],
      mesh.points[b],
      mesh.points[c],
    );
    const tol = 1e-6 * Math.max(radius, 1);
    for (let i = 0; i < mesh.points.length; i++) {
      if (i === a || i === b || i === c) continue;
      const gap = dist(center, mesh.points[i]) - radius;
      assert.ok(
        gap >= -tol,
        `triangle (${a},${b},${c}) circumcircle contains point ${i} (gap=${gap})`,
      );
    }
  }
}

test('50 random survey points: empty circumcircle property holds', () => {
  const points = randomPoints(50, 20261004);
  const delaunay = new DelaunayTriangulator(points);
  const mesh = delaunay.toMesh();
  assertEmptyCircumcircle(mesh);
});

test('50 random points: inCircle predicate confirms legality of every edge', () => {
  const points = randomPoints(50, 7);
  const mesh = new DelaunayTriangulator(points).toMesh();
  const scale4 = 1000 ** 4;
  for (const [a, b, c] of mesh.triangles) {
    for (let i = 0; i < points.length; i++) {
      if (i === a || i === b || i === c) continue;
      const det = inCircle(points[a], points[b], points[c], points[i]);
      assert.ok(
        det <= 1e-7 * scale4,
        `point ${i} inside circumcircle of (${a},${b},${c})`,
      );
    }
  }
});

test('Euler characteristic: T = 2n-2-h and E = 3n-3-h', () => {
  const n = 50;
  const mesh = new DelaunayTriangulator(randomPoints(n, 42)).toMesh();
  const h = mesh.hull.length;
  const T = mesh.triangles.length;
  assert.equal(T, 2 * n - 2 - h);
  const E = (3 * T + h) / 2;
  assert.equal(E, 3 * n - 3 - h);
});

test('adjacency graph is symmetric and neighbors share the correct edge', () => {
  const mesh = new DelaunayTriangulator(randomPoints(50, 99)).toMesh();
  const boundaryEdges: string[] = [];
  for (let t = 0; t < mesh.triangles.length; t++) {
    const tri = mesh.triangles[t];
    for (let k = 0; k < 3; k++) {
      const a = tri[(k + 1) % 3];
      const b = tri[(k + 2) % 3];
      const nb = mesh.neighbors[t][k];
      if (nb === -1) {
        boundaryEdges.push(`${Math.min(a, b)}_${Math.max(a, b)}`);
        continue;
      }
      // neighbor shares exactly the two edge endpoints
      const shared = mesh.triangles[nb].filter((v) => v === a || v === b);
      assert.equal(shared.length, 2, `T${t} and T${nb} must share edge ${a}-${b}`);
      // symmetry: t must be registered as a neighbor of nb
      assert.ok(
        mesh.neighbors[nb].includes(t),
        `T${t} missing from neighbor list of T${nb}`,
      );
    }
  }
  // every boundary edge belongs to the convex hull loop
  assert.equal(boundaryEdges.length, mesh.hull.length);
});

test('convex hull is convex, CCW, and contains every point', () => {
  const points = randomPoints(50, 1234);
  const mesh = new DelaunayTriangulator(points).toMesh();
  const h = mesh.hull.length;
  assert.ok(h >= 3);

  for (let i = 0; i < h; i++) {
    const p0 = points[mesh.hull[i]];
    const p1 = points[mesh.hull[(i + 1) % h]];
    const p2 = points[mesh.hull[(i + 2) % h]];
    assert.ok(orient2d(p0, p1, p2) > 0, 'hull must be strictly convex and CCW');
  }

  for (let i = 0; i < h; i++) {
    const p0 = points[mesh.hull[i]];
    const p1 = points[mesh.hull[(i + 1) % h]];
    for (let j = 0; j < points.length; j++) {
      assert.ok(
        orient2d(p0, p1, points[j]) >= -1e-6,
        `point ${j} lies outside hull edge ${i}`,
      );
    }
  }
});

test('incremental insertion keeps the mesh Delaunay after every step', () => {
  const points = randomPoints(30, 555);
  const delaunay = new DelaunayTriangulator();
  for (const p of points) {
    delaunay.insert(p);
    assertEmptyCircumcircle(delaunay.toMesh());
  }
});

test('growing beyond the initial supertriangle triggers a correct rebuild', () => {
  const delaunay = new DelaunayTriangulator(randomPoints(10, 1, 10));
  for (const p of randomPoints(20, 2, 10000)) delaunay.insert(p);
  const mesh = delaunay.toMesh();
  assert.equal(mesh.points.length, 30);
  assertEmptyCircumcircle(mesh);
  assert.equal(mesh.triangles.length, 2 * 30 - 2 - mesh.hull.length);
});

test('duplicate points are inserted only once', () => {
  const delaunay = new DelaunayTriangulator();
  const p = { x: 5, y: 5 };
  assert.equal(delaunay.insert(p), 0);
  assert.equal(delaunay.insert({ x: 5, y: 5 }), 0);
  assert.equal(delaunay.size, 1);
});

test('small deterministic case: square corners + center', () => {
  const mesh = new DelaunayTriangulator([
    { x: 0, y: 0 },
    { x: 10, y: 0 },
    { x: 10, y: 10 },
    { x: 0, y: 10 },
    { x: 5, y: 5 },
  ]).toMesh();
  assert.equal(mesh.triangles.length, 4);
  assert.equal(mesh.hull.length, 4);
  assertEmptyCircumcircle(mesh);
});

test('multiple seeds and sizes all satisfy the Delaunay property', () => {
  for (const [n, seed] of [
    [20, 11],
    [50, 22],
    [100, 33],
    [200, 44],
  ] as const) {
    const mesh = new DelaunayTriangulator(randomPoints(n, seed)).toMesh();
    assertEmptyCircumcircle(mesh);
    assert.equal(mesh.triangles.length, 2 * n - 2 - mesh.hull.length);
  }
});
