import { test } from "node:test";
import assert from "node:assert/strict";
import { Delaunay } from "../src/delaunay.ts";
import { orient2d, dist2 } from "../src/geometry.ts";
import type { Point } from "../src/geometry.ts";
import type { TriangleMesh } from "../src/delaunay.ts";

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

function randomPoints(n: number, seed: number, extent = 1000): Point[] {
  const rand = mulberry32(seed);
  return Array.from({ length: n }, () => ({
    x: rand() * extent,
    y: rand() * extent,
  }));
}

/** Every triangle's circumcircle must contain no fourth point. */
function assertEmptyCircumcircle(mesh: TriangleMesh, tol = 1e-6): void {
  for (const tri of mesh.triangles) {
    const r2 = tri.circumradius * tri.circumradius;
    for (let i = 0; i < mesh.points.length; i++) {
      if (tri.vertices.includes(i)) continue;
      const d2 = dist2(mesh.points[i], tri.circumcenter);
      assert.ok(
        d2 >= r2 * (1 - tol),
        `point ${i} inside circumcircle of triangle ${tri.vertices}`,
      );
    }
  }
}

test("50 random points: empty circumcircle property holds", () => {
  const points = randomPoints(50, 20261003);
  const mesh = new Delaunay(points).mesh();
  assertEmptyCircumcircle(mesh);
});

test("triangle count matches Euler formula T = 2n - 2 - h", () => {
  const points = randomPoints(50, 42);
  const mesh = new Delaunay(points).mesh();
  const n = mesh.points.length;
  const h = mesh.hull.length;
  assert.equal(mesh.triangles.length, 2 * n - 2 - h);
});

test("adjacency graph is symmetric and shares exact edges", () => {
  const points = randomPoints(50, 7);
  const mesh = new Delaunay(points).mesh();
  mesh.triangles.forEach((tri, i) => {
    tri.neighbors.forEach((nb, ei) => {
      if (nb === -1) return;
      const other = mesh.triangles[nb];
      // symmetry: neighbor must list us back
      const back = other.neighbors.indexOf(i);
      assert.notEqual(back, -1, `triangle ${nb} does not link back to ${i}`);
      // the shared edge must be the same pair of vertices
      const edgeA = [tri.vertices[(ei + 1) % 3], tri.vertices[(ei + 2) % 3]].sort();
      const edgeB = [other.vertices[(back + 1) % 3], other.vertices[(back + 2) % 3]].sort();
      assert.deepEqual(edgeA, edgeB);
    });
  });
});

test("convex hull is convex, CCW, and contains all points", () => {
  const points = randomPoints(50, 99);
  const mesh = new Delaunay(points).mesh();
  const { hull } = mesh;
  assert.ok(hull.length >= 3);
  for (let i = 0; i < hull.length; i++) {
    const a = mesh.points[hull[i]];
    const b = mesh.points[hull[(i + 1) % hull.length]];
    const c = mesh.points[hull[(i + 2) % hull.length]];
    assert.ok(orient2d(a, b, c) > 0, "hull is not strictly convex / CCW");
    // every point lies inside or on every hull edge
    for (let k = 0; k < mesh.points.length; k++) {
      assert.ok(orient2d(a, b, mesh.points[k]) >= -1e-6);
    }
  }
});

test("dynamic incremental insertion keeps the triangulation legal", () => {
  const del = new Delaunay(randomPoints(30, 1));
  assertEmptyCircumcircle(del.mesh());
  // insert points one by one, some far outside the initial bounding box
  const extra = [...randomPoints(15, 2), ...randomPoints(5, 3, 5000)];
  for (const p of extra) del.insert(p);
  const mesh = del.mesh();
  assert.equal(mesh.points.length, 50);
  assertEmptyCircumcircle(mesh);
  assert.equal(mesh.triangles.length, 2 * 50 - 2 - mesh.hull.length);
});

test("square plus center produces 4 triangles with correct adjacency", () => {
  const mesh = new Delaunay([
    { x: 0, y: 0 },
    { x: 1, y: 0 },
    { x: 1, y: 1 },
    { x: 0, y: 1 },
    { x: 0.5, y: 0.5 },
  ]).mesh();
  assert.equal(mesh.triangles.length, 4);
  assert.equal(mesh.hull.length, 4);
  for (const tri of mesh.triangles) {
    // each triangle touches the hull once and has exactly 2 neighbors
    assert.equal(tri.neighbors.filter((n) => n !== -1).length, 2);
  }
});

test("duplicate point insertion is ignored", () => {
  const points = randomPoints(20, 5);
  const del = new Delaunay(points);
  const before = del.size;
  const idx = del.insert({ ...points[7] });
  assert.equal(idx, 7);
  assert.equal(del.size, before);
  assertEmptyCircumcircle(del.mesh());
});

test("fewer than 3 points yields an empty mesh without crashing", () => {
  assert.equal(new Delaunay([]).mesh().triangles.length, 0);
  assert.equal(new Delaunay([{ x: 0, y: 0 }]).mesh().triangles.length, 0);
  assert.equal(
    new Delaunay([{ x: 0, y: 0 }, { x: 1, y: 1 }]).mesh().triangles.length,
    0,
  );
});

test("all triangles are counter-clockwise and non-degenerate", () => {
  const mesh = new Delaunay(randomPoints(50, 1234)).mesh();
  for (const tri of mesh.triangles) {
    const [a, b, c] = tri.vertices.map((i) => mesh.points[i]);
    assert.ok(orient2d(a, b, c) > 0);
  }
});
