import { test } from 'node:test';
import assert from 'node:assert/strict';
import { performance } from 'node:perf_hooks';
import { Broadphase } from '../src/broadphase.ts';
import type { CollisionPair } from '../src/broadphase.ts';
import type { AABB } from '../src/aabb.ts';
import { makeAABB, overlaps } from '../src/aabb.ts';

function boxAt(cx: number, cy: number, cz: number, half: number): AABB {
  return makeAABB(cx - half, cy - half, cz - half, cx + half, cy + half, cz + half);
}

function bruteForcePairs(boxes: Map<number, AABB>): CollisionPair[] {
  const ids = [...boxes.keys()].sort((a, b) => a - b);
  const pairs: CollisionPair[] = [];
  for (let i = 0; i < ids.length; i++) {
    for (let j = i + 1; j < ids.length; j++) {
      if (overlaps(boxes.get(ids[i]) as AABB, boxes.get(ids[j]) as AABB)) {
        pairs.push({ bodyA: ids[i], bodyB: ids[j] });
      }
    }
  }
  return pairs;
}

function pairKey(p: CollisionPair): string {
  return `${p.bodyA}:${p.bodyB}`;
}

test('detects overlapping pairs on insertion', () => {
  const bp = new Broadphase(0.1);
  bp.addBody(1, boxAt(0, 0, 0, 1));
  bp.addBody(2, boxAt(1.5, 0, 0, 1)); // overlaps body 1
  bp.addBody(3, boxAt(10, 0, 0, 1));  // far away
  const pairs = bp.computePairs();
  assert.deepEqual(pairs, [{ bodyA: 1, bodyB: 2 }]);
});

test('incremental updates only report pairs involving moved bodies', () => {
  const bp = new Broadphase(0.1);
  bp.addBody(1, boxAt(0, 0, 0, 1));
  bp.addBody(2, boxAt(5, 0, 0, 1));
  bp.addBody(3, boxAt(10, 0, 0, 1));
  assert.equal(bp.computePairs().length, 0);

  // Move body 2 next to body 3 -> exactly one new pair.
  bp.moveBody(2, boxAt(9, 0, 0, 1));
  assert.deepEqual(bp.computePairs(), [{ bodyA: 2, bodyB: 3 }]);

  // Nothing moved -> no pairs.
  assert.equal(bp.computePairs().length, 0);

  // Small move inside the fat margin: no reinsertion, still correct.
  bp.moveBody(1, boxAt(0.05, 0, 0, 1));
  assert.equal(bp.computePairs().length, 0);
});

test('removing a body removes its pairs', () => {
  const bp = new Broadphase(0.1);
  bp.addBody(1, boxAt(0, 0, 0, 1));
  bp.addBody(2, boxAt(1, 0, 0, 1));
  assert.equal(bp.computePairs().length, 1);
  bp.removeBody(2);
  bp.moveBody(1, boxAt(0.5, 0, 0, 1));
  assert.equal(bp.computePairs().length, 0);
  assert.equal(bp.bodyCount, 1);
});

test('fuzz: matches brute force over random moves', () => {
  const bp = new Broadphase(0.25);
  const boxes = new Map<number, AABB>();
  let seed = 42;
  const rand = () => {
    seed = (seed * 1103515245 + 12345) & 0x7fffffff;
    return seed / 0x7fffffff;
  };

  const N = 120;
  for (let id = 0; id < N; id++) {
    const box = boxAt(rand() * 50, rand() * 50, rand() * 50, 0.5 + rand());
    boxes.set(id, box);
    bp.addBody(id, box);
  }

  // Drain the initial insertion pairs (all bodies start dirty).
  assert.deepEqual(
    bp.computePairs().map(pairKey),
    bruteForcePairs(boxes).map(pairKey),
    'initial insertion',
  );

  for (let step = 0; step < 30; step++) {
    // Move a random subset each step.
    const movedCount = 1 + Math.floor(rand() * 20);
    const movedIds = new Set<number>();
    for (let k = 0; k < movedCount; k++) {
      const id = Math.floor(rand() * N);
      const box = boxAt(rand() * 50, rand() * 50, rand() * 50, 0.5 + rand());
      boxes.set(id, box);
      bp.moveBody(id, box);
      movedIds.add(id);
    }
    // Incremental semantics: only pairs touching a moved body are reported.
    const expected = bruteForcePairs(boxes)
      .filter((p) => movedIds.has(p.bodyA) || movedIds.has(p.bodyB))
      .map(pairKey);
    const actual = bp.computePairs().map(pairKey);
    assert.deepEqual(actual, expected, `step ${step}`);
  }
});

test('acceptance: 500 moving bodies, 12 overlapping pairs, fast broadphase', () => {
  const bp = new Broadphase(0.1);
  const boxes = new Map<number, AABB>();
  const N = 500;

  // Spread 500 bodies on a sparse 3D grid so nothing overlaps initially.
  const spacing = 10;
  for (let id = 0; id < N; id++) {
    const x = (id % 8) * spacing;
    const y = (Math.floor(id / 8) % 8) * spacing;
    const z = Math.floor(id / 64) * spacing;
    const box = boxAt(x, y, z, 1);
    boxes.set(id, box);
    bp.addBody(id, box);
  }
  assert.equal(bp.computePairs().length, 0);

  // Pick 12 disjoint pairs of bodies and move them so each pair overlaps.
  const pairIds: Array<[number, number]> = [];
  for (let k = 0; k < 12; k++) {
    pairIds.push([2 * k, 2 * k + 1]);
  }

  // Every body moves (small jitter), the 24 chosen ones converge pairwise.
  const t0 = performance.now();
  let moveTime = 0;
  for (let id = 0; id < N; id++) {
    const old = boxes.get(id) as AABB;
    const cx = (old.minX + old.maxX) / 2;
    const cy = (old.minY + old.maxY) / 2;
    const cz = (old.minZ + old.maxZ) / 2;
    const box = boxAt(cx + 0.3, cy - 0.2, cz + 0.1, 1);
    boxes.set(id, box);
    const m0 = performance.now();
    bp.moveBody(id, box);
    moveTime += performance.now() - m0;
  }
  for (const [a, b] of pairIds) {
    const boxA = boxes.get(a) as AABB;
    const cx = (boxA.minX + boxA.maxX) / 2;
    const cy = (boxA.minY + boxA.maxY) / 2;
    const cz = (boxA.minZ + boxA.maxZ) / 2;
    const box = boxAt(cx + 0.5, cy, cz, 1); // overlaps body a (distance 0.5 < 2)
    boxes.set(b, box);
    const m0 = performance.now();
    bp.moveBody(b, box);
    moveTime += performance.now() - m0;
  }

  const q0 = performance.now();
  const pairs = bp.computePairs();
  const queryTime = performance.now() - q0;
  const total = performance.now() - t0;

  console.log(
    `    [perf] 500 bodies moved | move+update: ${(moveTime * 1000).toFixed(1)}us | ` +
    `pair query: ${(queryTime * 1000).toFixed(1)}us | total: ${(total * 1000).toFixed(1)}us | ` +
    `tree height: ${bp.treeHeight}`,
  );

  // Exactly the 12 planted pairs, nothing else.
  assert.equal(pairs.length, 12);
  const expected = new Set(pairIds.map(([a, b]) => `${a}:${b}`));
  for (const p of pairs) {
    assert.ok(expected.has(pairKey(p)), `unexpected pair ${pairKey(p)}`);
  }

  // Cross-check against O(N^2) brute force.
  assert.deepEqual(pairs.map(pairKey), bruteForcePairs(boxes).map(pairKey));

  // Broadphase must stay well inside a frame budget (spec: tens of
  // microseconds; we allow generous headroom for slow CI machines).
  assert.ok(queryTime < 10, `pair query took ${queryTime.toFixed(3)}ms`);
});
