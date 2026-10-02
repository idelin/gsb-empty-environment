import { test } from "node:test";
import assert from "node:assert/strict";
import { AABB } from "../src/aabb.ts";
import { BVH, type BVHItem } from "../src/bvh.ts";
import { rayTriangle, type Triangle } from "../src/triangle.ts";
import {
  bruteForceRaycast,
  generateRays,
  generateTriangles,
  makeRng,
  triangleItems,
} from "../src/scene.ts";

function randomItems(count: number, dim: number, seed: number): BVHItem<number>[] {
  const rng = makeRng(seed);
  const items: BVHItem<number>[] = [];
  for (let i = 0; i < count; i++) {
    const min = Array.from({ length: dim }, () => rng() * 100 - 50);
    const max = min.map((v) => v + rng() * 5 + 0.1);
    items.push({ aabb: new AABB(min, max), data: i });
  }
  return items;
}

test("build: tree contains all items and validates structurally", () => {
  const items = randomItems(500, 3, 1);
  const tree = BVH.build(items);
  assert.equal(tree.size, 500);
  assert.ok(tree.validate());
  for (const item of items) {
    assert.ok(tree.root!.aabb.contains(item.aabb));
  }
});

test("build: SAH produces a balanced tree (height ~ O(log n))", () => {
  const items = randomItems(1000, 3, 2);
  const tree = BVH.build(items);
  const minHeight = Math.ceil(Math.log2(1000));
  assert.ok(tree.root!.height <= minHeight * 2, `height ${tree.root!.height}`);
});

test("insert/remove: tree stays valid and size-consistent", () => {
  const items = randomItems(200, 3, 3);
  const tree = BVH.build(items.slice(0, 100));
  for (const item of items.slice(100)) tree.insert(item);
  assert.equal(tree.size, 200);
  assert.ok(tree.validate());

  // Remove half, including dynamically inserted ones.
  for (let i = 0; i < items.length; i += 2) {
    assert.ok(tree.remove(items[i]));
  }
  assert.equal(tree.size, 100);
  assert.ok(tree.validate());

  // Removing again fails; removing everything empties the tree.
  assert.ok(!tree.remove(items[0]));
  for (let i = 1; i < items.length; i += 2) tree.remove(items[i]);
  assert.equal(tree.size, 0);
  assert.equal(tree.root, null);
  assert.ok(tree.validate());
});

test("insert/remove: query results match linear scan after churn", () => {
  const items = randomItems(300, 3, 4);
  const tree = BVH.build(items.slice(0, 150));
  for (const item of items.slice(150)) tree.insert(item);
  for (let i = 0; i < 300; i += 3) tree.remove(items[i]);

  const probe = new AABB([-10, -10, -10], [10, 10, 10]);
  const fromTree = new Set<number>();
  tree.query(probe, (d) => fromTree.add(d));
  const expected = new Set(
    items
      .filter((_, i) => i % 3 !== 0)
      .filter((it) => it.aabb.overlaps(probe))
      .map((it) => it.data),
  );
  assert.deepEqual(fromTree, expected);
});

test("raycast: matches brute-force nearest hit on 3D triangle scene", () => {
  const triangles = generateTriangles(400, 20, 11);
  const items = triangleItems(triangles);
  const tree = BVH.build(items);
  const rays = generateRays(50, 12);
  for (const ray of rays) {
    const hit = tree.raycast(ray.origin, ray.dir, (tri: Triangle) =>
      rayTriangle(ray.origin, ray.dir, tri),
    );
    const brute = bruteForceRaycast(triangles, ray.origin, ray.dir, rayTriangle);
    if (brute === null) {
      assert.equal(hit, null);
    } else {
      assert.ok(hit !== null);
      assert.ok(Math.abs(hit.t - brute.t) < 1e-6);
    }
  }
});

test("raycast: works in 2D with rectangle payloads", () => {
  const items = randomItems(100, 2, 5);
  const tree = BVH.build(items);
  const origin = [-200, 0];
  const dir = [1, 0];
  const hit = tree.raycast(origin, dir, (_data, aabb) => {
    // Leaf-level precise test: ray vs the item's own box.
    return aabb.rayHit(origin, [1 / dir[0], 1 / dir[1]], Infinity);
  });
  // Ground truth: nearest box crossed by the x-axis ray.
  let best: number | null = null;
  for (const it of items) {
    if (it.aabb.min[1] <= 0 && it.aabb.max[1] >= 0 && it.aabb.max[0] >= -200) {
      const t = Math.max(it.aabb.min[0] - -200, 0);
      if (best === null || t < best) best = t;
    }
  }
  if (best === null) assert.equal(hit, null);
  else assert.ok(hit !== null && Math.abs(hit.t - best) < 1e-9);
});

test("acceptance: 1000 triangles, 100 rays, >95% leaves skipped, ms-level", () => {
  const triangles = generateTriangles(1000, 40, 42);
  const tree = BVH.build(triangleItems(triangles));
  const rays = generateRays(100, 7);

  tree.resetStats();
  const start = performance.now();
  let hits = 0;
  for (const ray of rays) {
    const hit = tree.raycast(ray.origin, ray.dir, (tri: Triangle) =>
      rayTriangle(ray.origin, ray.dir, tri),
    );
    if (hit) hits++;
  }
  const elapsed = performance.now() - start;

  // Correctness against brute force.
  for (const ray of rays) {
    const hit = tree.raycast(ray.origin, ray.dir, (tri: Triangle) =>
      rayTriangle(ray.origin, ray.dir, tri),
    );
    const brute = bruteForceRaycast(triangles, ray.origin, ray.dir, rayTriangle);
    assert.equal(hit === null, brute === null);
    if (hit && brute) assert.ok(Math.abs(hit.t - brute.t) < 1e-6);
  }

  const skipped = 1 - tree.stats.leafTests / (rays.length * triangles.length);
  assert.ok(
    skipped > 0.95,
    `expected >95% leaves skipped, got ${(skipped * 100).toFixed(2)}%`,
  );
  assert.ok(elapsed < 100, `expected ms-level ray casting, got ${elapsed.toFixed(2)}ms`);
  assert.ok(hits > 0, "scene should produce some hits");
});
