import { test } from 'node:test';
import assert from 'node:assert/strict';
import { AABB } from '../src/aabb.ts';
import { Ray } from '../src/ray.ts';
import { BVH } from '../src/bvh.ts';
import { Triangle } from '../src/triangle.ts';
import type { Vec3 } from '../src/vec3.ts';
import {
  generateScene,
  generateRays,
  bruteForceNearest,
  makeRng,
} from '../examples/scene.ts';

const triBounds = (t: Triangle): AABB => t.bounds;
const triIntersect = (t: Triangle, ray: Ray): number | null => t.intersectRay(ray);

test('AABB: union, surface area, ray slab test', () => {
  const a = new AABB({ x: 0, y: 0, z: 0 }, { x: 1, y: 1, z: 1 });
  const b = new AABB({ x: 2, y: 0, z: 0 }, { x: 3, y: 1, z: 1 });
  const u = AABB.union(a, b);
  assert.deepEqual(u.min, { x: 0, y: 0, z: 0 });
  assert.deepEqual(u.max, { x: 3, y: 1, z: 1 });
  assert.equal(a.surfaceArea(), 6);

  const hit = a.intersectRay(new Ray({ x: -5, y: 0.5, z: 0.5 }, { x: 1, y: 0, z: 0 }));
  assert.equal(hit, 5);
  const miss = a.intersectRay(new Ray({ x: -5, y: 2, z: 0.5 }, { x: 1, y: 0, z: 0 }));
  assert.equal(miss, null);
  // Origin inside the box -> t = 0.
  const inside = a.intersectRay(new Ray({ x: 0.5, y: 0.5, z: 0.5 }, { x: 1, y: 0, z: 0 }));
  assert.equal(inside, 0);
});

test('Triangle: Möller–Trumbore hit/miss', () => {
  const tri = new Triangle(
    { x: 0, y: 0, z: 0 },
    { x: 1, y: 0, z: 0 },
    { x: 0, y: 1, z: 0 },
  );
  const hit = tri.intersectRay(new Ray({ x: 0.2, y: 0.2, z: 5 }, { x: 0, y: 0, z: -1 }));
  assert.ok(hit !== null && Math.abs(hit - 5) < 1e-9);
  const miss = tri.intersectRay(new Ray({ x: 2, y: 2, z: 5 }, { x: 0, y: 0, z: -1 }));
  assert.equal(miss, null);
  const behind = tri.intersectRay(new Ray({ x: 0.2, y: 0.2, z: -5 }, { x: 0, y: 0, z: -1 }));
  assert.equal(behind, null);
});

test('BVH build: SAH tree contains all objects and is valid', () => {
  const tris = generateScene(500, 1);
  const tree = BVH.build(tris, triBounds);
  assert.equal(tree.size, 500);
  assert.deepEqual(tree.validate(), []);
  // A balanced-ish SAH tree over 500 leaves should be far from degenerate.
  const height = (function h(n: any): number {
    return n ? 1 + Math.max(h(n.left), h(n.right)) : -1;
  })(tree.root);
  assert.ok(height < 30, `tree height ${height} suspiciously large`);
});

test('BVH raycast: matches brute-force nearest on every ray', () => {
  const tris = generateScene(1000, 42);
  const rays = generateRays(100, 7, tris);
  const tree = BVH.build(tris, triBounds);
  for (const ray of rays) {
    const fast = tree.raycast(ray, triIntersect);
    const slow = bruteForceNearest(tris, ray);
    if (slow === null) {
      assert.equal(fast, null);
    } else {
      assert.ok(fast !== null, 'BVH missed a hit');
      assert.equal(fast.object, slow.object);
      assert.ok(Math.abs(fast.t - slow.t) < 1e-6);
    }
  }
});

test('BVH raycast: prunes >95% of leaves (1000 triangles, 100 rays)', () => {
  const tris = generateScene(1000, 42);
  const rays = generateRays(100, 7, tris);
  const tree = BVH.build(tris, triBounds);
  let visited = 0;
  for (const ray of rays) {
    tree.raycast(ray, triIntersect);
    visited += tree.stats.leafNodesVisited;
  }
  const avgVisited = visited / rays.length;
  const skipRatio = 1 - avgVisited / tris.length;
  assert.ok(
    skipRatio > 0.95,
    `skip ratio ${(skipRatio * 100).toFixed(2)}% (avg leaves visited ${avgVisited.toFixed(1)}/1000)`,
  );
});

test('BVH raycast: 100 rays over 1000 triangles complete in milliseconds', () => {
  const tris = generateScene(1000, 42);
  const rays = generateRays(100, 7, tris);
  const tree = BVH.build(tris, triBounds);
  tree.raycast(rays[0], triIntersect); // warm up JIT
  const start = performance.now();
  for (const ray of rays) tree.raycast(ray, triIntersect);
  const elapsed = performance.now() - start;
  // Generous threshold: sub-ms per ray even on a loaded machine.
  assert.ok(elapsed < 100, `100 raycasts took ${elapsed.toFixed(2)}ms`);
});

test('BVH dynamic: insert and remove keep the tree valid and correct', () => {
  const rng = makeRng(123);
  const tree = new BVH<Triangle>(triBounds);
  const live: Triangle[] = [];

  const randTri = (): Triangle => {
    const p = (): Vec3 => ({
      x: (rng() - 0.5) * 200,
      y: (rng() - 0.5) * 200,
      z: (rng() - 0.5) * 200,
    });
    return new Triangle(p(), p(), p());
  };

  // Grow to 300 objects one by one.
  for (let i = 0; i < 300; i++) {
    const t = randTri();
    tree.insert(t);
    live.push(t);
  }
  assert.equal(tree.size, 300);
  assert.deepEqual(tree.validate(), []);

  // Remove half of them at random.
  for (let i = 0; i < 150; i++) {
    const idx = Math.floor(rng() * live.length);
    const [dead] = live.splice(idx, 1);
    assert.equal(tree.remove(dead), true);
  }
  assert.equal(tree.size, 150);
  assert.deepEqual(tree.validate(), []);
  assert.equal(tree.remove(live[0] && randTri()), false); // unknown object

  // Insert 50 more, then verify raycast against brute force.
  for (let i = 0; i < 50; i++) {
    const t = randTri();
    tree.insert(t);
    live.push(t);
  }
  assert.deepEqual(tree.validate(), []);

  const rays = generateRays(50, 99, live);
  for (const ray of rays) {
    const fast = tree.raycast(ray, triIntersect);
    const slow = bruteForceNearest(live, ray);
    if (slow === null) assert.equal(fast, null);
    else {
      assert.ok(fast !== null);
      assert.equal(fast.object, slow.object);
      assert.ok(Math.abs(fast.t - slow.t) < 1e-6);
    }
  }
});

test('BVH dynamic: clear and edge cases', () => {
  const tree = new BVH<Triangle>(triBounds);
  const ray = new Ray({ x: 0, y: 0, z: 10 }, { x: 0, y: 0, z: -1 });
  assert.equal(tree.raycast(ray, triIntersect), null); // empty tree
  const t = new Triangle({ x: -1, y: -1, z: 0 }, { x: 1, y: -1, z: 0 }, { x: 0, y: 1, z: 0 });
  tree.insert(t);
  tree.insert(t); // duplicate insert is a no-op
  assert.equal(tree.size, 1);
  const hit = tree.raycast(ray, triIntersect);
  assert.ok(hit !== null && Math.abs(hit.t - 10) < 1e-9);
  assert.equal(tree.remove(t), true);
  assert.equal(tree.size, 0);
  assert.equal(tree.root, null);
  tree.clear();
});

test('BVH 2D: works for planar scenes (z = 0)', () => {
  const rng = makeRng(5);
  const tris: Triangle[] = [];
  for (let i = 0; i < 200; i++) {
    const bx = (rng() - 0.5) * 100;
    const by = (rng() - 0.5) * 100;
    tris.push(
      new Triangle(
        { x: bx, y: by, z: 0 },
        { x: bx + rng() * 2, y: by, z: 0 },
        { x: bx, y: by + rng() * 2, z: 0 },
      ),
    );
  }
  const tree = BVH.build(tris, triBounds);
  assert.deepEqual(tree.validate(), []);
  const ray = new Ray({ x: 0, y: 0, z: 10 }, { x: 0, y: 0, z: -1 });
  const fast = tree.raycast(ray, triIntersect);
  const slow = bruteForceNearest(tris, ray);
  assert.equal(fast === null, slow === null);
  if (fast && slow) assert.equal(fast.object, slow.object);
});
