import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  AABB4,
  SpaceTimeBVH,
  makeSegment,
  queryBox,
  segmentEntersBox,
  trajectoryToSegments,
  type TrajectorySegment,
  type Vec4,
} from '../src/index.ts';

// ---------- AABB4 ----------

test('AABB4 intersection across all four axes', () => {
  const a = new AABB4(0, 0, 0, 0, 10, 10, 10, 10);
  assert.ok(a.intersects(new AABB4(5, 5, 5, 5, 20, 20, 20, 20)));
  // Overlap in space but disjoint in time => no intersection.
  assert.ok(!a.intersects(new AABB4(5, 5, 5, 11, 20, 20, 20, 20)));
  // Overlap in time but disjoint in z => no intersection.
  assert.ok(!a.intersects(new AABB4(5, 5, 11, 5, 20, 20, 20, 20)));
});

test('AABB4 widestAxis considers the time axis', () => {
  const box = new AABB4(0, 0, 0, 0, 1, 1, 1, 1000);
  assert.equal(box.widestAxis(), 3);
});

// ---------- exact segment/box test ----------

test('segmentEntersBox rejects AABB false positives', () => {
  // The segment's 4D AABB overlaps the box, but the actual path crosses the
  // x-range and y-range at different parameters => never inside the cube.
  const seg = makeSegment('t', 0, { x: 0, y: 0, z: 0, t: 0 }, { x: 10, y: 10, z: 0, t: 0 });
  const box = queryBox(-1, 1, 4, 6, 7, 9, -1, 1);
  assert.ok(seg.bounds.intersects(box), 'AABB should overlap');
  assert.ok(!segmentEntersBox(seg, box), 'exact test should reject');
});

test('segmentEntersBox accepts partial temporal overlap', () => {
  // Segment spans 13:00-15:00 at a fixed position inside the zone;
  // query window 14:00-14:10 overlaps only part of it.
  const seg = makeSegment('t', 0, { x: 0, y: 0, z: 120, t: 0 }, { x: 1, y: 1, z: 120, t: 7200 });
  const box = queryBox(3600, 4200, -10, 10, -10, 10, 100, Infinity);
  assert.ok(segmentEntersBox(seg, box));
});

// ---------- BVH correctness vs brute force ----------

function randomSegments(count: number, seed = 42): TrajectorySegment[] {
  // Deterministic LCG so tests are reproducible.
  let state = seed;
  const rand = (): number => {
    state = (state * 1103515245 + 12345) % 2147483648;
    return state / 2147483648;
  };
  const segments: TrajectorySegment[] = [];
  for (let i = 0; i < count; i++) {
    const p0: Vec4 = {
      x: rand() * 10000 - 5000,
      y: rand() * 10000 - 5000,
      z: rand() * 500,
      t: Math.floor(rand() * 3600_000),
    };
    const p1: Vec4 = {
      x: p0.x + rand() * 400 - 200,
      y: p0.y + rand() * 400 - 200,
      z: Math.max(0, p0.z + rand() * 100 - 50),
      t: p0.t + 1000 + Math.floor(rand() * 60_000),
    };
    segments.push(makeSegment(`traj-${i % 100}`, i, p0, p1));
  }
  return segments;
}

test('BVH query matches brute-force scan exactly', () => {
  const segments = randomSegments(5000);
  const bvh = new SpaceTimeBVH(segments);
  const box = queryBox(600_000, 1_200_000, -1000, 1000, -1000, 1000, 100, Infinity);

  const expected = segments
    .filter((s) => s.bounds.intersects(box) && segmentEntersBox(s, box))
    .map((s) => s.segmentIndex)
    .sort((a, b) => a - b);
  const actual = bvh.query(box).segments
    .map((s) => s.segmentIndex)
    .sort((a, b) => a - b);

  assert.deepEqual(actual, expected);
  assert.ok(expected.length > 0, 'test query should produce hits');
});

test('dynamic insert produces the same results as bulk build', () => {
  const segments = randomSegments(500);
  const bulk = new SpaceTimeBVH(segments);
  const incremental = new SpaceTimeBVH();
  for (const seg of segments) incremental.insert(seg);

  const box = queryBox(0, 3_600_000, -2000, 2000, -2000, 2000, 50, 300);
  const key = (r: { segments: TrajectorySegment[] }): string =>
    r.segments.map((s) => s.segmentIndex).sort((a, b) => a - b).join(',');
  assert.equal(key(incremental.query(box)), key(bulk.query(box)));
});

// ---------- pruning & performance ----------

test('BVH prunes the vast majority of the tree', () => {
  const segments = randomSegments(50_000);
  const bvh = new SpaceTimeBVH(segments);
  // Tight query: 10-minute window, 2km cube, above 100m.
  const box = queryBox(600_000, 1_200_000, -1000, 1000, -1000, 1000, 100, Infinity);

  const { stats } = bvh.query(box);
  assert.ok(
    stats.candidatesTested < segments.length * 0.05,
    `expected heavy pruning, tested ${stats.candidatesTested}/${segments.length}`,
  );
  assert.ok(stats.nodesVisited < bvh.nodeCount());
});

test('millisecond-level query latency on 50k segments', () => {
  const segments = randomSegments(50_000, 7);
  const bvh = new SpaceTimeBVH(segments);
  const box = queryBox(600_000, 1_200_000, -1000, 1000, -1000, 1000, 100, Infinity);

  // Warm up JIT, then measure.
  bvh.query(box);
  const { stats } = bvh.query(box);
  assert.ok(
    stats.elapsedMs < 10,
    `query took ${stats.elapsedMs.toFixed(2)} ms, expected < 10 ms`,
  );
});

// ---------- acceptance scenario: drone cruise ----------

test('acceptance: drones in protection zone, 14:00-14:10, altitude >= 100m', () => {
  const t = (hhmm: string): number => new Date(`2026-10-02T${hhmm}:00+08:00`).getTime();
  const drones: Array<{ id: string; points: Vec4[] }> = [
    {
      id: 'UAV-in-window-high', // crosses zone at 150m inside window -> HIT
      points: [
        { x: -3000, y: 0, z: 150, t: t('13:58') },
        { x: 0, y: 0, z: 150, t: t('14:05') },
        { x: 3000, y: 0, z: 150, t: t('14:12') },
      ],
    },
    {
      id: 'UAV-outside-window', // same path but at 14:30 -> MISS
      points: [
        { x: -3000, y: 0, z: 150, t: t('14:20') },
        { x: 3000, y: 0, z: 150, t: t('14:40') },
      ],
    },
    {
      id: 'UAV-too-low', // inside window but at 60m -> MISS
      points: [
        { x: -3000, y: 0, z: 60, t: t('14:02') },
        { x: 3000, y: 0, z: 60, t: t('14:08') },
      ],
    },
    {
      id: 'UAV-outside-zone', // high, in window, far away -> MISS
      points: [
        { x: -3000, y: 5000, z: 300, t: t('14:02') },
        { x: 3000, y: 5000, z: 300, t: t('14:08') },
      ],
    },
  ];

  const segments = drones.flatMap((d) => trajectoryToSegments(d.id, d.points));
  const bvh = new SpaceTimeBVH(segments);
  const box = queryBox(t('14:00'), t('14:10'), -1000, 1000, -1000, 1000, 100, Infinity);

  const { segments: hits, stats } = bvh.query(box);
  const hitIds = [...new Set(hits.map((s) => s.trajectoryId))];

  assert.deepEqual(hitIds, ['UAV-in-window-high']);
  assert.equal(hits.length, 2, 'both legs of the crossing drone');
  assert.ok(stats.elapsedMs < 10);
});
