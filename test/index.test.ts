import { test } from "node:test";
import assert from "node:assert/strict";
import { InvalidSegmentError, SpatioTemporalIndex } from "../src/index.ts";
import { Rational } from "../src/rational.ts";
import type { Segment, SpatioTemporalQuery } from "../src/types.ts";

function seg(partial: Partial<Segment> & { objectId: string; segmentId: string }): Segment {
  return {
    t0: 0,
    t1: 10,
    from: { x: 0, y: 0, z: 0 },
    to: { x: 0, y: 0, z: 0 },
    ...partial,
  };
}

const query: SpatioTemporalQuery = {
  time: { t0: 0, t1: 100 },
  box: { min: { x: -1, y: -1, z: -1 }, max: { x: 1, y: 1, z: 1 } },
};

test("rejects zero-duration and negative-duration segments", () => {
  const idx = new SpatioTemporalIndex();
  assert.throws(
    () => idx.updateTrajectory("a", [seg({ objectId: "a", segmentId: "s0", t0: 5, t1: 5 })]),
    InvalidSegmentError,
  );
  assert.throws(
    () => idx.updateTrajectory("a", [seg({ objectId: "a", segmentId: "s0", t0: 5, t1: 4 })]),
    InvalidSegmentError,
  );
  assert.throws(
    () => idx.updateTrajectory("a", [seg({ objectId: "a", segmentId: "s0", from: { x: 0.5, y: 0, z: 0 } })]),
    InvalidSegmentError,
  );
  assert.equal(idx.getVersion(), 0); // failed updates do not create versions
});

test("rejects duplicate segment ids and mismatched object ids", () => {
  const idx = new SpatioTemporalIndex();
  assert.throws(() =>
    idx.updateTrajectory("a", [
      seg({ objectId: "a", segmentId: "s0" }),
      seg({ objectId: "a", segmentId: "s0" }),
    ]),
  );
  assert.throws(() =>
    idx.updateTrajectory("a", [seg({ objectId: "b", segmentId: "s0" })]),
  );
});

test("updates create versions; old snapshots remain queryable", () => {
  const idx = new SpatioTemporalIndex();
  const v1 = idx.updateTrajectory("a", [
    seg({ objectId: "a", segmentId: "old", from: { x: 0, y: 0, z: 0 }, to: { x: 0, y: 0, z: 0 } }),
  ]);
  const v2 = idx.updateTrajectory("a", [
    seg({ objectId: "a", segmentId: "new", from: { x: 100, y: 0, z: 0 }, to: { x: 100, y: 0, z: 0 } }),
  ]);
  assert.equal(v2, v1 + 1);

  const latest = idx.query(query);
  assert.equal(latest.version, v2);
  assert.equal(latest.objects.length, 0); // moved out of the box

  const old = idx.query(query, v1);
  assert.equal(old.version, v1);
  assert.equal(old.objects.length, 1);
  assert.equal(old.objects[0].version, v1);
  assert.equal(old.objects[0].intervals[0].sources[0].segmentId, "old");
});

test("per-object version resolution across global versions", () => {
  const idx = new SpatioTemporalIndex();
  idx.updateTrajectory("a", [seg({ objectId: "a", segmentId: "a1" })]); // v1
  idx.updateTrajectory("b", [seg({ objectId: "b", segmentId: "b1", t0: 50, t1: 60 })]); // v2
  idx.updateTrajectory("a", [seg({ objectId: "a", segmentId: "a2", from: { x: 50, y: 0, z: 0 }, to: { x: 50, y: 0, z: 0 } })]); // v3

  const at2 = idx.query(query, 2);
  const byId = new Map(at2.objects.map((o) => [o.objectId, o]));
  assert.equal(byId.get("a")?.version, 1); // a's v1 still in effect at v2
  assert.equal(byId.get("a")?.intervals[0].sources[0].segmentId, "a1");
  assert.equal(byId.get("b")?.version, 2);

  const at3 = idx.query(query, 3);
  const a3 = at3.objects.find((o) => o.objectId === "a");
  assert.equal(a3, undefined); // a moved out of the box in v3
});

test("merges adjacent and overlapping hits, keeps sources", () => {
  const idx = new SpatioTemporalIndex();
  idx.updateTrajectory("a", [
    // x = -6 + 3t/5, in box for t in [25/3, 10)
    seg({ objectId: "a", segmentId: "s1", t0: 0, t1: 10, from: { x: -6, y: 0, z: 0 }, to: { x: 0, y: 0, z: 0 } }),
    // x = 2(t-10)/5, in box for t in [10, 25/2)
    seg({ objectId: "a", segmentId: "s2", t0: 10, t1: 20, from: { x: 0, y: 0, z: 0 }, to: { x: 4, y: 0, z: 0 } }),
    // disjoint later visit: t in [30, 35)
    seg({ objectId: "a", segmentId: "s3", t0: 30, t1: 35, from: { x: 0, y: 0, z: 0 }, to: { x: 0, y: 0, z: 0 } }),
  ]);
  const res = idx.query(query);
  assert.equal(res.objects.length, 1);
  const intervals = res.objects[0].intervals;
  assert.equal(intervals.length, 2);

  assert.ok(intervals[0].lo.eq(Rational.of(25, 3)));
  assert.ok(intervals[0].hi.eq(Rational.of(25, 2)));
  assert.equal(intervals[0].hiOpen, false); // right end clamped by space (closed)
  assert.deepEqual(
    intervals[0].sources.map((s) => s.segmentId),
    ["s1", "s2"],
  );

  assert.ok(intervals[1].lo.eq(Rational.of(30)));
  assert.ok(intervals[1].hi.eq(Rational.of(35)));
  assert.equal(intervals[1].hiOpen, true);
  assert.deepEqual(
    intervals[1].sources.map((s) => s.segmentId),
    ["s3"],
  );
});

test("reports access statistics and prunes with the BVH", () => {
  const idx = new SpatioTemporalIndex();
  const segments: Segment[] = [];
  for (let i = 0; i < 50; i++) {
    segments.push(
      seg({
        objectId: "a",
        segmentId: `far-${i}`,
        t0: 1000 + i * 10,
        t1: 1010 + i * 10,
        from: { x: 1000, y: 0, z: 0 },
        to: { x: 1000, y: 0, z: 0 },
      }),
    );
  }
  segments.push(seg({ objectId: "a", segmentId: "near" }));
  idx.updateTrajectory("a", segments);

  const res = idx.query(query);
  assert.equal(res.stats.totalSegments, 51);
  assert.ok(res.stats.bvhNodesVisited > 0);
  assert.ok(res.stats.candidateSegments < 51, "BVH should prune far-away segments");
  assert.equal(res.stats.hitSegments, 1);
  assert.equal(res.stats.exactIntersectionTests, res.stats.candidateSegments);

  const cumulative = idx.getStats();
  assert.ok(cumulative.bvhNodesVisited >= res.stats.bvhNodesVisited);

  const brute = idx.queryBruteForce(query);
  assert.equal(brute.stats.candidateSegments, 51);
  assert.equal(brute.stats.bvhNodesVisited, 0);
});

test("rejects invalid queries", () => {
  const idx = new SpatioTemporalIndex();
  idx.updateTrajectory("a", [seg({ objectId: "a", segmentId: "s" })]);
  assert.throws(() =>
    idx.query({ time: { t0: 5, t1: 5 }, box: query.box }),
  );
  assert.throws(() =>
    idx.query({
      time: { t0: 0, t1: 5 },
      box: { min: { x: 2, y: 0, z: 0 }, max: { x: 1, y: 0, z: 0 } },
    }),
  );
});
