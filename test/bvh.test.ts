import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import {
  AABB4,
  SpacetimeBVH,
  buildSegments,
  clipSegmentToBox,
} from '../src/index.ts';
import type { QueryStats, TrajectoryPoint, TrajectorySegment } from '../src/index.ts';

// Deterministic PRNG (mulberry32) so randomized tests are reproducible.
function rng(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function randomTrajectory(rand: () => number, id: string, n: number): TrajectoryPoint[] {
  const points: TrajectoryPoint[] = [];
  let x = (rand() - 0.5) * 20000;
  let y = (rand() - 0.5) * 20000;
  let z = rand() * 300;
  let t = Math.floor(rand() * 3600_000);
  for (let i = 0; i < n; i++) {
    points.push({ x, y, z, t });
    x += (rand() - 0.5) * 200;
    y += (rand() - 0.5) * 200;
    z = Math.max(0, z + (rand() - 0.5) * 40);
    t += 1000;
  }
  return points;
}

function bruteForce(segments: TrajectorySegment[], box: AABB4): TrajectorySegment[] {
  return segments.filter((s) => s.aabb.intersects(box));
}

describe('AABB4', () => {
  it('detects intersection on all four axes including time', () => {
    const a = new AABB4(0, 0, 0, 0, 10, 10, 10, 10);
    assert.ok(a.intersects(new AABB4(5, 5, 5, 5, 20, 20, 20, 20)));
    assert.ok(a.intersects(new AABB4(10, 10, 10, 10, 20, 20, 20, 20))); // touching
    assert.ok(!a.intersects(new AABB4(0, 0, 0, 11, 10, 10, 10, 20))); // disjoint in t
    assert.ok(!a.intersects(new AABB4(11, 0, 0, 0, 20, 10, 10, 10))); // disjoint in x
  });

  it('merges and computes union boxes', () => {
    const a = new AABB4(0, 0, 0, 0, 1, 1, 1, 1);
    const b = new AABB4(2, -1, 0, 5, 3, 0, 1, 6);
    const m = AABB4.merged(a, b);
    assert.deepEqual(
      [m.minX, m.minY, m.minZ, m.minT, m.maxX, m.maxY, m.maxZ, m.maxT],
      [0, -1, 0, 0, 3, 1, 1, 6],
    );
  });
});

describe('clipSegmentToBox', () => {
  it('returns exact entry/exit times for a crossing segment', () => {
    const [seg] = buildSegments('d1', [
      { x: -100, y: 0, z: 150, t: 0 },
      { x: 100, y: 0, z: 150, t: 1000 },
    ]);
    const box = new AABB4(-50, -10, 100, 0, 50, 10, 200, 1000);
    const frag = clipSegmentToBox(seg, box)!;
    assert.ok(frag !== null);
    assert.equal(frag.enter.x, -50);
    assert.equal(frag.exit.x, 50);
    assert.equal(frag.enter.t, 250); // 25% along the segment
    assert.equal(frag.exit.t, 750);
  });

  it('rejects segments whose AABB overlaps but that miss the box corner-wise', () => {
    // Diagonal in (x, t): inside x-range and t-range of the box, but never
    // inside both at the same time.
    const [seg] = buildSegments('d1', [
      { x: 0, y: 0, z: 0, t: 0 },
      { x: 100, y: 0, z: 0, t: 100 },
    ]);
    const box = new AABB4(0, -1, -1, 60, 40, 1, 1, 100);
    assert.ok(seg.aabb.intersects(box));
    assert.equal(clipSegmentToBox(seg, box), null);
  });
});

describe('SpacetimeBVH', () => {
  it('bulk build query matches brute force on random data', () => {
    const rand = rng(42);
    const segments: TrajectorySegment[] = [];
    for (let i = 0; i < 50; i++) {
      segments.push(...buildSegments(`t${i}`, randomTrajectory(rand, `t${i}`, 60)));
    }
    const bvh = new SpacetimeBVH();
    bvh.build(segments);

    for (let q = 0; q < 100; q++) {
      const cx = (rand() - 0.5) * 20000;
      const cy = (rand() - 0.5) * 20000;
      const cz = rand() * 300;
      const t1 = rand() * 3600_000;
      const box = AABB4.cube(cx, cy, cz, 500 + rand() * 2000, t1, t1 + rand() * 600_000);
      const got = new Set(bvh.query(box));
      const want = bruteForce(segments, box);
      assert.equal(got.size, want.length);
      for (const seg of want) assert.ok(got.has(seg));
    }
  });

  it('incremental insert matches bulk build results', () => {
    const rand = rng(7);
    const segments: TrajectorySegment[] = [];
    for (let i = 0; i < 30; i++) {
      segments.push(...buildSegments(`t${i}`, randomTrajectory(rand, `t${i}`, 40)));
    }
    const bulk = new SpacetimeBVH();
    bulk.build(segments);
    const inc = new SpacetimeBVH();
    for (const seg of segments) inc.insert(seg);
    assert.equal(inc.size, segments.length);

    for (let q = 0; q < 50; q++) {
      const box = AABB4.cube(
        (rand() - 0.5) * 20000, (rand() - 0.5) * 20000, rand() * 300,
        1000, rand() * 3600_000, rand() * 3600_000,
      );
      const a = new Set(bulk.query(box));
      const b = new Set(inc.query(box));
      assert.equal(b.size, a.size);
      for (const seg of a) assert.ok(b.has(seg));
    }
  });

  it('filters by time window and altitude (acceptance scenario)', () => {
    const T = (h: number, m: number) => Date.UTC(2026, 9, 3, h, m);
    const bvh = new SpacetimeBVH();

    // Drone A: crosses the zone at 14:05 at 150 m -> must match.
    bvh.insertTrajectory('A', [
      { x: -2000, y: 0, z: 150, t: T(14, 0) },
      { x: 2000, y: 0, z: 150, t: T(14, 10) },
    ]);
    // Drone B: crosses the zone but at 14:30 -> outside time window.
    bvh.insertTrajectory('B', [
      { x: -2000, y: 0, z: 150, t: T(14, 25) },
      { x: 2000, y: 0, z: 150, t: T(14, 35) },
    ]);
    // Drone C: inside time window but only at 50 m -> below 100 m.
    bvh.insertTrajectory('C', [
      { x: -2000, y: 0, z: 50, t: T(14, 0) },
      { x: 2000, y: 0, z: 50, t: T(14, 10) },
    ]);
    // Drone D: inside time window, high enough, but flies around the zone.
    bvh.insertTrajectory('D', [
      { x: 5000, y: 5000, z: 150, t: T(14, 0) },
      { x: 6000, y: 6000, z: 150, t: T(14, 10) },
    ]);

    const zone = new AABB4(-1000, -1000, 100, T(14, 0), 1000, 1000, 500, T(14, 10));
    const hits = bvh.queryFragments(zone);

    assert.equal(hits.length, 1);
    const frag = hits[0];
    assert.equal(frag.segment.trajectoryId, 'A');
    // The 4 km leg crosses the 2 km-wide zone between 1/4 and 3/4 of the
    // segment: enters at 14:02:30, leaves at 14:07:30.
    assert.equal(frag.enter.t, T(14, 2) + 30_000);
    assert.equal(frag.exit.t, T(14, 7) + 30_000);
    assert.equal(frag.enter.z, 150);
  });

  it('precise mode drops AABB false positives', () => {
    const [diag] = buildSegments('diag', [
      { x: 0, y: 0, z: 0, t: 0 },
      { x: 100, y: 0, z: 0, t: 100 },
    ]);
    const bvh = new SpacetimeBVH();
    bvh.build([diag]);
    const box = new AABB4(0, -1, -1, 60, 40, 1, 1, 100);
    assert.equal(bvh.query(box).length, 1);                    // AABB overlap
    assert.equal(bvh.query(box, { precise: true }).length, 0); // exact: miss
    assert.equal(bvh.queryFragments(box).length, 0);
  });

  it('prunes subtrees and answers large queries in milliseconds', () => {
    const rand = rng(1234);
    const segments: TrajectorySegment[] = [];
    for (let i = 0; i < 200; i++) {
      segments.push(...buildSegments(`t${i}`, randomTrajectory(rand, `t${i}`, 100)));
    }
    const bvh = new SpacetimeBVH();
    bvh.build(segments);
    assert.equal(bvh.size, segments.length);

    const box = AABB4.cube(0, 0, 150, 1000, 1_800_000, 2_400_000);
    const stats: QueryStats = { nodesVisited: 0, nodesPruned: 0, segmentsTested: 0 };
    const start = performance.now();
    const hits = bvh.query(box, { stats });
    const elapsed = performance.now() - start;

    assert.deepEqual(
      new Set(hits),
      new Set(bruteForce(segments, box)),
      'BVH result must equal brute force',
    );
    assert.ok(stats.nodesPruned > 0, 'expected some subtrees to be pruned');
    assert.ok(
      stats.segmentsTested < segments.length,
      `expected pruning to test fewer than all ${segments.length} segments, got ${stats.segmentsTested}`,
    );
    assert.ok(elapsed < 50, `query took ${elapsed.toFixed(2)} ms, expected < 50 ms`);
  });

  it('handles an empty tree gracefully', () => {
    const bvh = new SpacetimeBVH();
    const box = new AABB4(0, 0, 0, 0, 1, 1, 1, 1);
    assert.deepEqual(bvh.query(box), []);
    assert.deepEqual(bvh.queryFragments(box), []);
    assert.equal(bvh.depth(), 0);
    assert.equal(bvh.size, 0);
  });
});
