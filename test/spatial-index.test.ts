import test from 'node:test';
import assert from 'node:assert/strict';
import {
  apply,
  exactPathContains,
  hitTest,
  multiply,
  node,
  rotation,
  scaling,
  SpatialIndex,
  translation,
  update,
  walk,
  type Mat,
  type Point,
  type SceneNode,
} from '../src/index.ts';

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

test('index agrees with direct traversal on a fixed scene', () => {
  const a = node({ matrix: rotation(0.7), rect: { x: 0, y: 0, width: 8, height: 8 } });
  const b = node({ matrix: translation(4, 4), rect: { x: 0, y: 0, width: 8, height: 8 } });
  const root = node({ children: [a, b] });
  const index = new SpatialIndex();

  for (const p of [
    { x: 1, y: 1 },
    { x: 6, y: 6 },
    { x: 50, y: 50 },
    { x: -3, y: 2 },
  ]) {
    const expected = hitTest(root, p)?.node ?? null;
    const actual = index.hitTest(root, p)?.node ?? null;
    assert.equal(actual, expected, `point ${JSON.stringify(p)}`);
  }
});

test('ancestor transform update invalidates descendants; old snapshot keeps old chain', () => {
  const leaf = node({ rect: { x: 0, y: 0, width: 10, height: 10 } });
  const parent = node({ matrix: translation(0, 0), children: [leaf] });
  const rootV1 = node({ children: [parent] });

  const index = new SpatialIndex();
  // Warm the cache on the first snapshot.
  assert.equal(index.hitTest(rootV1, { x: 5, y: 5 })?.node, leaf);

  // Move the ancestor: the leaf's world position changes even though the
  // leaf object itself is untouched.
  const rootV2 = update(rootV1, parent, (n) => n.withMatrix(translation(100, 0)));

  // New chain: leaf moved away.
  assert.equal(index.hitTest(rootV2, { x: 5, y: 5 }), null);
  assert.equal(index.hitTest(rootV2, { x: 105, y: 5 })?.node, leaf);
  // Old snapshot still serves the old chain.
  assert.equal(index.hitTest(rootV1, { x: 5, y: 5 })?.node, leaf);
  assert.equal(index.hitTest(rootV1, { x: 105, y: 5 }), null);

  // Same for the plain traversal: snapshots are fully independent.
  assert.equal(hitTest(rootV1, { x: 5, y: 5 })?.node, leaf);
  assert.equal(hitTest(rootV2, { x: 5, y: 5 }), null);
});

test('randomized: index candidates never miss an exactly-hittable node', () => {
  const rand = mulberry32(0xc0ffee);
  const root = randomScene(rand);
  const index = new SpatialIndex();

  const allPaths: Array<{ node: SceneNode; path: readonly SceneNode[] }> = [...walk(root)];

  for (let i = 0; i < 3000; i++) {
    const p: Point = { x: (rand() - 0.5) * 400, y: (rand() - 0.5) * 400 };

    // 1) Indexed hit test must equal the direct inverse-transform traversal.
    const expected = hitTest(root, p)?.node ?? null;
    const actual = index.hitTest(root, p)?.node ?? null;
    assert.equal(actual, expected, `hit mismatch at ${JSON.stringify(p)}`);

    // 2) Every exactly-hittable node must appear among the AABB candidates.
    const candidates = new Set(index.query(root, p));
    for (const { node: n, path } of allPaths) {
      if (exactPathContains(path, p)) {
        assert.ok(candidates.has(n), `candidate set missed a hittable node at ${JSON.stringify(p)}`);
      }
    }
  }
});

test('randomized: forward-generated points are found on their generating node', () => {
  const rand = mulberry32(1234);
  for (let trial = 0; trial < 200; trial++) {
    const angle = rand() * Math.PI * 2;
    const sx = 0.25 + rand() * 3;
    const sy = 0.25 + rand() * 3;
    const m: Mat = multiply(translation((rand() - 0.5) * 100, (rand() - 0.5) * 100), multiply(rotation(angle), scaling(sx, sy)));
    const target = node({ matrix: m, rect: { x: 0, y: 0, width: 10, height: 10 } });
    const root = node({ children: [target] });
    const index = new SpatialIndex();

    const local: Point = { x: rand() * 10, y: rand() * 10 };
    const world = apply(m, local);
    assert.equal(hitTest(root, world)?.node, target);
    assert.equal(index.hitTest(root, world)?.node, target);
  }
});

function randomScene(rand: () => number): SceneNode {
  const build = (depth: number): SceneNode => {
    const singular = rand() < 0.08;
    const m: Mat = singular
      ? scaling(0, 1)
      : multiply(
          translation((rand() - 0.5) * 60, (rand() - 0.5) * 60),
          multiply(rotation((rand() - 0.5) * 2), scaling(0.5 + rand() * 2, 0.5 + rand() * 2)),
        );
    const rect =
      rand() < 0.15
        ? null
        : { x: (rand() - 0.5) * 20, y: (rand() - 0.5) * 20, width: 2 + rand() * 18, height: 2 + rand() * 18 };
    const clipRoll = rand();
    const clip =
      clipRoll < 0.2
        ? { kind: 'rect' as const, rect: { x: (rand() - 0.5) * 16, y: (rand() - 0.5) * 16, width: 4 + rand() * 16, height: 4 + rand() * 16 } }
        : clipRoll < 0.4
          ? { kind: 'circle' as const, circle: { cx: (rand() - 0.5) * 10, cy: (rand() - 0.5) * 10, r: 3 + rand() * 12 } }
          : null;
    const childCount = depth >= 4 ? 0 : Math.floor(rand() * 3);
    const children = Array.from({ length: childCount }, () => build(depth + 1));
    return node({ matrix: m, rect, clip, children });
  };
  return node({ children: [build(0), build(0), build(0)] });
}
