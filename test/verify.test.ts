import { test } from "node:test";
import assert from "node:assert/strict";
import { SpatioTemporalIndex } from "../src/index.ts";
import { verifyQueryResult } from "../src/verify.ts";
import { Rational } from "../src/rational.ts";
import type { QueryResult, Segment, SpatioTemporalQuery } from "../src/types.ts";

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

function buildIndex(): SpatioTemporalIndex {
  const idx = new SpatioTemporalIndex();
  idx.updateTrajectory("a", [
    seg({ objectId: "a", segmentId: "s1", from: { x: -6, y: 0, z: 0 }, to: { x: 0, y: 0, z: 0 } }),
    seg({ objectId: "a", segmentId: "s2", t0: 10, t1: 20, from: { x: 0, y: 0, z: 0 }, to: { x: 4, y: 0, z: 0 } }),
  ]);
  idx.updateTrajectory("b", [
    seg({ objectId: "b", segmentId: "b1", t0: 3, t1: 4, from: { x: 0, y: 0, z: 0 }, to: { x: 0, y: 0, z: 0 } }),
  ]);
  return idx;
}

test("accepts a correct result", () => {
  const idx = buildIndex();
  const res = idx.query(query);
  const report = verifyQueryResult(idx, query, res);
  assert.deepEqual(report.errors, []);
  assert.ok(report.ok);
});

test("rejects a tampered interval", () => {
  const idx = buildIndex();
  const res = idx.query(query);
  const bad: QueryResult = {
    ...res,
    objects: res.objects.map((o) =>
      o.objectId === "a"
        ? {
            ...o,
            intervals: o.intervals.map((iv, i) =>
              i === 0 ? { ...iv, hi: iv.hi.add(Rational.of(1)) } : iv,
            ),
          }
        : o,
    ),
  };
  const report = verifyQueryResult(idx, query, bad);
  assert.ok(!report.ok);
  assert.ok(report.errors.some((e) => e.includes("union of sources")));
});

test("rejects a fabricated source segment id", () => {
  const idx = buildIndex();
  const res = idx.query(query);
  const bad: QueryResult = {
    ...res,
    objects: res.objects.map((o) => ({
      ...o,
      intervals: o.intervals.map((iv) => ({
        ...iv,
        sources: iv.sources.map((s, i) =>
          i === 0 ? { ...s, segmentId: "ghost" } : s,
        ),
      })),
    })),
  };
  const report = verifyQueryResult(idx, query, bad);
  assert.ok(!report.ok);
  assert.ok(report.errors.some((e) => e.includes("ghost")));
});

test("rejects a tampered source interval", () => {
  const idx = buildIndex();
  const res = idx.query(query);
  const bad: QueryResult = {
    ...res,
    objects: res.objects.map((o) => ({
      ...o,
      intervals: o.intervals.map((iv) => ({
        ...iv,
        sources: iv.sources.map((s, i) =>
          i === 0
            ? { ...s, interval: { ...s.interval, lo: s.interval.lo.sub(Rational.of(1)) } }
            : s,
        ),
      })),
    })),
  };
  const report = verifyQueryResult(idx, query, bad);
  assert.ok(!report.ok);
  assert.ok(report.errors.some((e) => e.includes("recomputed")));
});

test("rejects a missing object (incomplete result)", () => {
  const idx = buildIndex();
  const res = idx.query(query);
  const bad: QueryResult = {
    ...res,
    objects: res.objects.filter((o) => o.objectId !== "b"),
  };
  const report = verifyQueryResult(idx, query, bad);
  assert.ok(!report.ok);
  assert.ok(report.errors.some((e) => e.includes("b")));
});

test("rejects a wrong referenced version", () => {
  const idx = buildIndex();
  idx.updateTrajectory("a", [
    seg({ objectId: "a", segmentId: "s3", t0: 0, t1: 5, from: { x: 0, y: 0, z: 0 }, to: { x: 0, y: 0, z: 0 } }),
  ]); // v3
  const res = idx.query(query, 2);
  const bad: QueryResult = {
    ...res,
    objects: res.objects.map((o) => ({ ...o, version: 3 })),
  };
  const report = verifyQueryResult(idx, query, bad);
  assert.ok(!report.ok);
  assert.ok(report.errors.some((e) => e.includes("version")));
});
