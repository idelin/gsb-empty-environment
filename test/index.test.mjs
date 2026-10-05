import assert from 'node:assert/strict';
import { test } from './harness.mjs';
import { fracToString } from '../dist/fraction.js';
import { SpatioTemporalIndex, formatQueryResult } from '../dist/index.js';

const BOX = { lo: { x: 0, y: 0, z: 0 }, hi: { x: 10, y: 10, z: 10 } };

function fmt(iv) {
  return fracToString(iv.start) + '..' + fracToString(iv.end) + (iv.endInclusive ? ']' : ')');
}

test('index: query before any commit throws', () => {
  const idx = new SpatioTemporalIndex();
  assert.throws(() => idx.query(BOX, { t0: 0, t1: 10 }), /commit/);
});

test('index: unknown version throws', () => {
  const idx = new SpatioTemporalIndex();
  idx.addSegment({ objectId: 'a', segmentId: 'a0', t0: 0, t1: 5, p0: { x: 1, y: 1, z: 1 }, p1: { x: 1, y: 1, z: 1 } });
  idx.commit();
  assert.throws(() => idx.query(BOX, { t0: 0, t1: 10 }, { version: 99 }), /unknown version/);
});

test('index: duplicate segmentId in working set throws', () => {
  const idx = new SpatioTemporalIndex();
  const s = { objectId: 'a', segmentId: 'dup', t0: 0, t1: 5, p0: { x: 1, y: 1, z: 1 }, p1: { x: 1, y: 1, z: 1 } };
  idx.addSegment(s);
  assert.throws(() => idx.addSegment(s), /duplicate/);
});

test('index: zero-duration segment rejected at insert', () => {
  const idx = new SpatioTemporalIndex();
  assert.throws(
    () => idx.addSegment({ objectId: 'a', segmentId: 'z', t0: 3, t1: 3, p0: { x: 0, y: 0, z: 0 }, p1: { x: 0, y: 0, z: 0 } }),
    /zero-duration/
  );
});

test('index: update creates a new version, old snapshot stays queryable', () => {
  const idx = new SpatioTemporalIndex();
  idx.addSegment({ objectId: 'ship', segmentId: 's1', t0: 0, t1: 10, p0: { x: 1, y: 1, z: 1 }, p1: { x: 1, y: 1, z: 1 } });
  const v1 = idx.commit();
  idx.updateObject('ship', [
    { objectId: 'ship', segmentId: 's2', t0: 0, t1: 10, p0: { x: 500, y: 500, z: 500 }, p1: { x: 500, y: 500, z: 500 } },
  ]);
  const v2 = idx.commit();
  assert.equal(v1, 1);
  assert.equal(v2, 2);
  assert.deepEqual(idx.listVersions(), [1, 2]);

  const r1 = idx.query(BOX, { t0: 0, t1: 10 }, { version: v1 });
  assert.equal(r1.version, 1);
  assert.equal(r1.objects.length, 1);
  assert.equal(r1.objects[0].intervals[0].sources[0].segmentId, 's1');

  const r2 = idx.query(BOX, { t0: 0, t1: 10 });
  assert.equal(r2.version, 2);
  assert.equal(r2.objects.length, 0, 'moved object no longer in box on latest version');

  const r1again = idx.query(BOX, { t0: 0, t1: 10 }, { version: 1 });
  assert.equal(r1again.objects.length, 1, 'old snapshot still queryable after later queries');
});

test('index: adjacent segments of one object merge into disjoint intervals with sources', () => {
  const idx = new SpatioTemporalIndex();
  // s1 enters the box at t=3 and stays inside until its end (t=10);
  // s2 sits inside for [10, 20); s3 sits inside for [30, 40).
  idx.addSegments([
    { objectId: 'o', segmentId: 's1', t0: 0, t1: 10, p0: { x: -3, y: 1, z: 1 }, p1: { x: 7, y: 1, z: 1 } },
    { objectId: 'o', segmentId: 's2', t0: 10, t1: 20, p0: { x: 5, y: 1, z: 1 }, p1: { x: 5, y: 1, z: 1 } },
    { objectId: 'o', segmentId: 's3', t0: 30, t1: 40, p0: { x: 2, y: 2, z: 2 }, p1: { x: 2, y: 2, z: 2 } },
  ]);
  idx.commit();
  const r = idx.query(BOX, { t0: 0, t1: 100 });
  assert.equal(r.objects.length, 1);
  const ivs = r.objects[0].intervals;
  assert.equal(ivs.length, 2, 'two disjoint merged intervals');
  assert.equal(fmt(ivs[0]), '3..20)');
  assert.deepEqual(ivs[0].sources.map((s) => s.segmentId), ['s1', 's2']);
  assert.equal(fmt(ivs[0].sources[0].interval), '3..10)');
  assert.equal(fmt(ivs[0].sources[1].interval), '10..20)');
  assert.equal(fmt(ivs[1]), '30..40)');
  assert.deepEqual(ivs[1].sources.map((s) => s.segmentId), ['s3']);
});

test('index: point hit merges with adjacent interval', () => {
  const idx = new SpatioTemporalIndex();
  // s1 grazes the box corner at exactly t=5 (point hit), s2 sits inside from t=5.
  idx.addSegments([
    { objectId: 'o', segmentId: 'g', t0: 0, t1: 10, p0: { x: 0, y: 0, z: 0 }, p1: { x: 10, y: 10, z: 0 } },
    { objectId: 'o', segmentId: 'h', t0: 5, t1: 9, p0: { x: 5, y: 5, z: 0 }, p1: { x: 5, y: 5, z: 0 } },
  ]);
  idx.commit();
  const r = idx.query({ lo: { x: 0, y: 5, z: 0 }, hi: { x: 5, y: 10, z: 1 } }, { t0: 0, t1: 100 });
  const ivs = r.objects[0].intervals;
  assert.equal(ivs.length, 1);
  assert.equal(fmt(ivs[0]), '5..9)');
  assert.equal(ivs[0].sources.length, 2);
});

test('index: 4D bounding box overlap alone does not produce a hit', () => {
  const idx = new SpatioTemporalIndex();
  // Diagonal in (x, y, t): its 4D AABB overlaps the query, but x is inside
  // the box only for t >= 8 while y is inside only for t <= 2.
  idx.addSegment({ objectId: 'o', segmentId: 'diag', t0: 0, t1: 10, p0: { x: 0, y: 0, z: 0 }, p1: { x: 10, y: 10, z: 0 } });
  idx.commit();
  const r = idx.query({ lo: { x: 8, y: 0, z: -1 }, hi: { x: 10, y: 2, z: 1 } }, { t0: 0, t1: 10 });
  assert.equal(r.objects.length, 0);
  assert.equal(r.stats.candidateSegments, 1, 'BVH passed it as a candidate');
  assert.equal(r.stats.exactTests, 1);
  assert.equal(r.stats.hitSegments, 0);
});

test('index: access statistics are consistent and pruning works', () => {
  const idx = new SpatioTemporalIndex();
  for (let i = 0; i < 100; i++) {
    idx.addSegment({
      objectId: 'obj' + (i % 10),
      segmentId: 'seg' + i,
      t0: i,
      t1: i + 5,
      p0: { x: i * 100, y: 0, z: 0 },
      p1: { x: i * 100 + 10, y: 0, z: 0 },
    });
  }
  idx.commit();
  const r = idx.query({ lo: { x: 0, y: -5, z: -5 }, hi: { x: 50, y: 5, z: 5 } }, { t0: 0, t1: 200 });
  const st = r.stats;
  assert.equal(st.totalSegments, 100);
  assert.ok(st.candidateSegments < 100, 'some segments pruned, got ' + st.candidateSegments);
  assert.equal(st.prunedSegments, st.totalSegments - st.candidateSegments);
  assert.equal(st.exactTests, st.candidateSegments);
  assert.ok(st.nodesVisited > 0);
  assert.ok(st.nodesPruned > 0);
  assert.ok(st.leavesVisited > 0);
  assert.equal(idx.lastQueryStats, st);
});

test('index: empty committed version queries cleanly', () => {
  const idx = new SpatioTemporalIndex();
  idx.commit();
  const r = idx.query(BOX, { t0: 0, t1: 10 });
  assert.equal(r.objects.length, 0);
  assert.equal(r.stats.totalSegments, 0);
  assert.equal(r.stats.nodesVisited, 0);
});

test('index: formatQueryResult renders fractions and bigints as strings', () => {
  const idx = new SpatioTemporalIndex();
  idx.addSegment({ objectId: 'o', segmentId: 's', t0: 0, t1: 2, p0: { x: 0, y: 0, z: 0 }, p1: { x: 3, y: 0, z: 0 } });
  const v = idx.commit();
  const r = idx.query({ lo: { x: 1, y: -1, z: -1 }, hi: { x: 2, y: 1, z: 1 } }, { t0: 0, t1: 10 });
  const f = formatQueryResult(r);
  assert.equal(f.version, v);
  assert.equal(f.window.t0, '0');
  assert.equal(f.objects[0].intervals[0].start, '2/3');
  assert.equal(f.objects[0].intervals[0].end, '4/3');
  assert.equal(f.objects[0].intervals[0].endInclusive, true);
  assert.equal(f.objects[0].intervals[0].sources[0].segmentId, 's');
  JSON.stringify(f);
});
