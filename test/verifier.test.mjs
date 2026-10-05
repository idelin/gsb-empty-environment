import assert from 'node:assert/strict';
import { test } from './harness.mjs';
import { SpatioTemporalIndex } from '../dist/index.js';
import { verifyQueryResult } from '../dist/verifier.js';
import { intersectSegmentBox } from '../dist/intersect.js';
import { normalizeBox, normalizeWindow } from '../dist/geometry.js';

function mulberry32(seed) {
  let a = seed >>> 0;
  return function () {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function buildRandomIndex(rand, numObjects, segsPerObject) {
  const idx = new SpatioTemporalIndex();
  let id = 0;
  for (let o = 0; o < numObjects; o++) {
    let t = Math.floor(rand() * 5);
    for (let s = 0; s < segsPerObject; s++) {
      const dur = 1 + Math.floor(rand() * 10);
      const p0 = {
        x: Math.floor(rand() * 41) - 20,
        y: Math.floor(rand() * 41) - 20,
        z: Math.floor(rand() * 41) - 20,
      };
      const stationary = rand() < 0.2;
      const p1 = stationary
        ? p0
        : {
            x: Math.floor(rand() * 41) - 20,
            y: Math.floor(rand() * 41) - 20,
            z: Math.floor(rand() * 41) - 20,
          };
      idx.addSegment({
        objectId: 'obj' + o,
        segmentId: 'seg' + id++,
        t0: t,
        t1: t + dur,
        p0,
        p1,
      });
      t += dur;
    }
  }
  idx.commit();
  return idx;
}

function randomBox(rand) {
  const lo = {
    x: Math.floor(rand() * 31) - 15,
    y: Math.floor(rand() * 31) - 15,
    z: Math.floor(rand() * 31) - 15,
  };
  const hi = {
    x: lo.x + Math.floor(rand() * 16),
    y: lo.y + Math.floor(rand() * 16),
    z: lo.z + Math.floor(rand() * 16),
  };
  return { lo, hi };
}

function randomWindow(rand) {
  const t0 = Math.floor(rand() * 40);
  return { t0, t1: t0 + 1 + Math.floor(rand() * 30) };
}

test('verifier: fuzz — index results match independent recomputation', () => {
  const rand = mulberry32(20261006);
  for (let iter = 0; iter < 300; iter++) {
    const idx = buildRandomIndex(rand, 1 + Math.floor(rand() * 4), 1 + Math.floor(rand() * 5));
    const box = randomBox(rand);
    const window = randomWindow(rand);
    const result = idx.query(box, window);
    const report = verifyQueryResult(idx, result);
    assert.ok(report.ok, 'iteration ' + iter + ' failed:\n' + report.errors.join('\n'));

    // Cross-check the object set against a direct brute-force loop.
    const nbox = normalizeBox(box);
    const nwin = normalizeWindow(window);
    const expectedObjects = new Set();
    for (const s of idx.getVersionSegments(result.version)) {
      if (intersectSegmentBox(s, nbox, nwin) !== null) {
        expectedObjects.add(s.objectId);
      }
    }
    assert.deepEqual(
      new Set(result.objects.map((o) => o.objectId)),
      expectedObjects,
      'object set mismatch at iteration ' + iter
    );
  }
});

test('verifier: fuzz across versions after updates', () => {
  const rand = mulberry32(777);
  const idx = buildRandomIndex(rand, 3, 4);
  const v1 = idx.listVersions()[0];
  idx.updateObject('obj1', [
    { objectId: 'obj1', segmentId: 'new1', t0: 0, t1: 50, p0: { x: 0, y: 0, z: 0 }, p1: { x: 10, y: 0, z: 0 } },
  ]);
  idx.commit();
  for (const version of [v1, v1 + 1]) {
    for (let i = 0; i < 30; i++) {
      const result = idx.query(randomBox(rand), randomWindow(rand), { version });
      const report = verifyQueryResult(idx, result);
      assert.ok(report.ok, 'version ' + version + ' iter ' + i + ':\n' + report.errors.join('\n'));
    }
  }
});

function validResult() {
  const idx = new SpatioTemporalIndex();
  idx.addSegments([
    { objectId: 'a', segmentId: 'a1', t0: 0, t1: 10, p0: { x: 0, y: 0, z: 0 }, p1: { x: 20, y: 0, z: 0 } },
    { objectId: 'b', segmentId: 'b1', t0: 0, t1: 10, p0: { x: 5, y: 5, z: 5 }, p1: { x: 5, y: 5, z: 5 } },
  ]);
  idx.commit();
  const box = { lo: { x: 4, y: -1, z: -1 }, hi: { x: 8, y: 6, z: 6 } };
  const window = { t0: 0, t1: 10 };
  const result = idx.query(box, window);
  const report = verifyQueryResult(idx, result);
  assert.ok(report.ok, 'baseline must verify: ' + report.errors.join('; '));
  assert.equal(report.checkedObjects, 2);
  return { idx, result };
}

function clone(obj) {
  return JSON.parse(JSON.stringify(obj, (k, v) => (typeof v === 'bigint' ? { __bigint: v.toString() } : v)), (k, v) =>
    v && v.__bigint !== undefined ? BigInt(v.__bigint) : v
  );
}

test('verifier: detects a shifted interval end (recomputes, not just checks existence)', () => {
  const { idx, result } = validResult();
  const tampered = clone(result);
  tampered.objects[0].intervals[0].end = { n: 9n, d: 1n };
  const report = verifyQueryResult(idx, tampered);
  assert.ok(!report.ok);
  assert.ok(report.errors.length > 0);
});

test('verifier: detects a removed source segment', () => {
  const { idx, result } = validResult();
  const tampered = clone(result);
  tampered.objects[0].intervals[0].sources = [];
  const report = verifyQueryResult(idx, tampered);
  assert.ok(!report.ok);
});

test('verifier: detects a dropped object', () => {
  const { idx, result } = validResult();
  const tampered = clone(result);
  tampered.objects = tampered.objects.filter((o) => o.objectId !== 'b');
  const report = verifyQueryResult(idx, tampered);
  assert.ok(!report.ok);
});

test('verifier: detects a phantom object', () => {
  const { idx, result } = validResult();
  const tampered = clone(result);
  tampered.objects.push({
    objectId: 'ghost',
    version: result.version,
    intervals: [
      {
        start: { n: 0n, d: 1n },
        end: { n: 1n, d: 1n },
        endInclusive: false,
        sources: [{ segmentId: 'a1', interval: { start: { n: 0n, d: 1n }, end: { n: 1n, d: 1n }, endInclusive: false } }],
      },
    ],
  });
  const report = verifyQueryResult(idx, tampered);
  assert.ok(!report.ok);
});

test('verifier: detects flipped endInclusive flag', () => {
  const { idx, result } = validResult();
  const tampered = clone(result);
  const iv = tampered.objects[0].intervals[0];
  iv.endInclusive = !iv.endInclusive;
  const report = verifyQueryResult(idx, tampered);
  assert.ok(!report.ok);
});

test('verifier: detects swapped source segment id', () => {
  const { idx, result } = validResult();
  const tampered = clone(result);
  tampered.objects[0].intervals[0].sources[0].segmentId = 'b1';
  const report = verifyQueryResult(idx, tampered);
  assert.ok(!report.ok);
});

test('verifier: detects inconsistent stats', () => {
  const { idx, result } = validResult();
  const tampered = clone(result);
  tampered.stats.hitSegments = 99;
  const report = verifyQueryResult(idx, tampered);
  assert.ok(!report.ok);
});
