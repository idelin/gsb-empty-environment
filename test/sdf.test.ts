import { test } from "node:test";
import assert from "node:assert/strict";

import {
  circle,
  box2,
  sphere,
  box3,
  torus,
  union,
  subtract,
} from "../src/geometry.ts";
import { SDFGrid2D, SDFGrid3D } from "../src/grid.ts";
import { rayMarch2D, rayMarch3D } from "../src/raymarch.ts";

const BOUNDS2 = { min: [-2, -2] as [number, number], max: [2, 2] as [number, number] };
const BOUNDS3 = {
  min: [-2, -2, -2] as [number, number, number],
  max: [2, 2, 2] as [number, number, number],
};

// ---------------------------------------------------------------------------
// Analytic SDF primitives
// ---------------------------------------------------------------------------

test("analytic primitives follow the inside-negative sign convention", () => {
  const c = circle([0, 0], 1);
  assert.ok(c([0, 0]) < 0, "circle center is inside");
  assert.ok(c([2, 0]) > 0, "far point is outside");
  assert.ok(Math.abs(c([1, 0])) < 1e-12, "point on rim has distance 0");

  const s = sphere([0, 0, 0], 1);
  assert.ok(s([0, 0, 0]) < 0);
  assert.ok(s([0, 3, 0]) > 0);
  assert.ok(Math.abs(s([0, 0, -1])) < 1e-12);

  const b = box2([0, 0], [0.5, 0.5]);
  assert.ok(Math.abs(b([0.5, 0])) < 1e-12, "box face lies at distance 0");
  assert.ok(b([0, 0]) < 0);

  const t = torus([0, 0, 0], 1, 0.25);
  assert.ok(Math.abs(t([1.25, 0, 0])) < 1e-12, "torus tube outer rim");
  assert.ok(t([1, 0, 0]) < 0, "ring centerline is inside the tube");
  assert.ok(t([0, 0, 0]) > 0, "torus hole is outside");
});

test("CSG operators compose SDFs", () => {
  const two = union(circle([-0.5, 0], 0.6), circle([0.5, 0], 0.6));
  assert.ok(two([0.9, 0]) < 0, "union covers both lobes");
  const carved = subtract(circle([0, 0], 1), circle([0.8, 0], 0.5));
  assert.ok(carved([0.9, 0]) > 0, "subtraction removes material");
  assert.ok(carved([-0.5, 0]) < 0, "subtraction keeps the rest");
});

// ---------------------------------------------------------------------------
// 2D grid rasterization (acceptance: 64x64 grid)
// ---------------------------------------------------------------------------

test("rasterizes a 64x64 signed-distance grid from geometry", () => {
  const grid = new SDFGrid2D(64, 64, BOUNDS2).rasterize(circle([0, 0], 1));
  assert.equal(grid.data.length, 64 * 64);

  // Sign convention on grid nodes.
  assert.ok(grid.at(32, 32) < 0, "center node is inside (negative)");
  assert.ok(grid.at(0, 0) > 0, "corner node is outside (positive)");

  // Sampled field approximates the analytic SDF everywhere.
  const exact = circle([0, 0], 1);
  let maxErr = 0;
  for (let n = 0; n < 2000; n++) {
    // Deterministic Halton sequence keeps the test reproducible.
    const u = (n * 0.6180339887498949) % 1;
    const v = (n * 0.7548776662466927) % 1;
    const p: [number, number] = [-2 + 4 * u, -2 + 4 * v];
    maxErr = Math.max(maxErr, Math.abs(grid.sample(p) - exact(p)));
  }
  assert.ok(maxErr < 0.05, `bilinear sample error ${maxErr} within tolerance`);
});

test("grid gradient points away from the surface", () => {
  const grid = new SDFGrid2D(64, 64, BOUNDS2).rasterize(circle([0, 0], 1));
  const g = grid.gradient([1.5, 0]);
  const len = Math.hypot(g[0], g[1]);
  assert.ok(Math.abs(len - 1) < 0.05, "SDF gradient has unit magnitude");
  assert.ok(g[0] > 0.9 && Math.abs(g[1]) < 0.1, "gradient points radially out");
});

// ---------------------------------------------------------------------------
// 2D ray marching (acceptance: safe SDF-sized leaps, fast convergence)
// ---------------------------------------------------------------------------

test("ray marching converges on the surface in very few steps", () => {
  const grid = new SDFGrid2D(64, 64, BOUNDS2).rasterize(circle([0, 0], 1));
  const sdf = (p: [number, number]) => grid.sample(p);

  const res = rayMarch2D(sdf, [-1.9, 0.3], [1, 0]);
  assert.ok(res.hit, "ray hits the circle");
  assert.ok(res.steps <= 8, `converged in ${res.steps} steps (sphere tracing)`);
  // Expected hit: x = -sqrt(1 - 0.3^2) ≈ -0.9539, distance ≈ 0.9461.
  assert.ok(
    Math.abs(res.distance - (1.9 - Math.sqrt(1 - 0.09))) < 0.02,
    `hit distance ${res.distance} matches analytic expectation`,
  );
  assert.ok(Math.abs(res.finalSDF) <= 1e-4, "final SDF within epsilon");
});

test("each march step advances by exactly the current SDF value", () => {
  const exact = circle([0, 0], 1);
  const origin: [number, number] = [-1.9, 0];
  const visited: number[] = [];
  const probe = (p: [number, number]) => {
    const d = exact(p);
    visited.push(d);
    return d;
  };
  const res = rayMarch2D(probe, origin, [1, 0]);
  assert.ok(res.hit);
  // Cumulative sum of recorded SDF values equals the total travel distance:
  // every leap spans exactly the maximum guaranteed-empty distance.
  const sum = visited.slice(0, -1).reduce((a, b) => a + b, 0);
  assert.ok(Math.abs(sum - res.distance) < 1e-9);
  assert.ok(res.steps <= 3, "analytic field converges almost immediately");
});

test("ray marching reports a miss when nothing is in the path", () => {
  const grid = new SDFGrid2D(64, 64, BOUNDS2).rasterize(circle([0, 0], 1));
  const sdf = (p: [number, number]) => grid.sample(p);
  const res = rayMarch2D(sdf, [0, 1.5], [0, 1], { maxDistance: 5 });
  assert.equal(res.hit, false);
});

test("ray marching works from an arbitrary origin and direction", () => {
  const grid = new SDFGrid2D(64, 64, BOUNDS2).rasterize(
    union(circle([-0.6, 0.4], 0.5), box2([0.7, -0.5], [0.3, 0.3])),
  );
  const sdf = (p: [number, number]) => grid.sample(p);
  const res = rayMarch2D(sdf, [1.7, 1.8], [-1, -2.3]);
  assert.ok(res.hit, "diagonal ray hits the compound shape");
  assert.ok(res.steps <= 16, `converged in ${res.steps} steps`);
  assert.ok(Math.abs(res.finalSDF) <= 1e-4);
});

// ---------------------------------------------------------------------------
// 3D grid + ray marching
// ---------------------------------------------------------------------------

test("rasterizes a 64x64x64 grid and marches a ray onto a sphere", () => {
  const grid = new SDFGrid3D(64, 64, 64, BOUNDS3).rasterize(
    sphere([0, 0, 0], 1),
  );
  assert.equal(grid.data.length, 64 * 64 * 64);
  assert.ok(grid.at(32, 32, 32) < 0, "center voxel inside");
  assert.ok(grid.at(0, 0, 0) > 0, "corner voxel outside");

  const sdf = (p: [number, number, number]) => grid.sample(p);
  const res = rayMarch3D(sdf, [0, 0, -1.9], [0, 0, 1]);
  assert.ok(res.hit, "ray hits the sphere");
  assert.ok(res.steps <= 8, `converged in ${res.steps} steps`);
  assert.ok(
    Math.abs(res.distance - 0.9) < 0.02,
    `hit distance ${res.distance} ≈ 0.9`,
  );

  const g = grid.gradient([0, 1.5, 0]);
  assert.ok(g[1] > 0.9, "3D gradient points radially outward");
});

test("3D ray marcher handles compound geometry and misses", () => {
  const grid = new SDFGrid3D(64, 64, 64, BOUNDS3).rasterize(
    union(sphere([-0.7, 0, 0], 0.6), box3([0.8, 0, 0], [0.4, 0.4, 0.4])),
  );
  const sdf = (p: [number, number, number]) => grid.sample(p);

  const hit = rayMarch3D(sdf, [0.8, 0, 1.9], [0, 0, -1]);
  assert.ok(hit.hit, "ray hits the box");
  assert.ok(Math.abs(hit.distance - (1.9 - 0.4)) < 0.03);

  const miss = rayMarch3D(sdf, [0, 1.9, 1.9], [0, 1, 1], { maxDistance: 6 });
  assert.equal(miss.hit, false);
});

// ---------------------------------------------------------------------------
// Isosurface extraction
// ---------------------------------------------------------------------------

test("marching squares extracts the zero contour of a circle", () => {
  const grid = new SDFGrid2D(64, 64, BOUNDS2).rasterize(circle([0, 0], 1));
  const segments = grid.extractIsosurface();
  assert.ok(segments.length > 50, "contour is well resolved");
  for (const [a, b] of segments) {
    for (const p of [a, b]) {
      const r = Math.hypot(p[0], p[1]);
      assert.ok(
        Math.abs(r - 1) < 0.05,
        `contour vertex at radius ${r} lies on the circle`,
      );
    }
  }
});

test("marching tetrahedra extracts the zero surface of a sphere", () => {
  const grid = new SDFGrid3D(64, 64, 64, BOUNDS3).rasterize(
    sphere([0, 0, 0], 1),
  );
  const triangles = grid.extractIsosurface();
  assert.ok(triangles.length > 1000, "surface is well resolved");
  for (let n = 0; n < 50; n++) {
    const tri = triangles[Math.floor(Math.random() * triangles.length)];
    for (const p of tri) {
      const r = Math.hypot(p[0], p[1], p[2]);
      assert.ok(Math.abs(r - 1) < 0.06, `vertex at radius ${r} on sphere`);
    }
  }
});

// ---------------------------------------------------------------------------
// Dynamic update
// ---------------------------------------------------------------------------

test("grid can be re-rasterized when geometry moves", () => {
  const grid = new SDFGrid2D(64, 64, BOUNDS2);
  grid.rasterize(circle([-1, 0], 0.5));
  assert.ok(grid.sample([-1, 0]) < 0);
  assert.ok(grid.sample([1, 0]) > 0);

  grid.rasterize(circle([1, 0], 0.5));
  assert.ok(grid.sample([1, 0]) < 0, "field follows the moved geometry");
  assert.ok(grid.sample([-1, 0]) > 0);
});
