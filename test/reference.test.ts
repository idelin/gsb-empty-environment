import { test } from "node:test";
import assert from "node:assert/strict";
import { SpatioTemporalIndex } from "../src/index.ts";
import { verifyQueryResult } from "../src/verify.ts";
import type {
  MergedInterval,
  ObjectResult,
  QueryResult,
  Segment,
  SpatioTemporalQuery,
} from "../src/types.ts";

/** Deterministic PRNG (mulberry32) so failures reproduce. */
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

function randomSegment(rand: () => number, objectId: string, i: number): Segment {
  const t0 = Math.floor(rand() * 200) - 50;
  const duration = 1 + Math.floor(rand() * 30);
  const coord = () => Math.floor(rand() * 41) - 20;
  // 1 in 6 segments is stationary on all axes.
  const stationary = rand() < 1 / 6;
  const from = { x: coord(), y: coord(), z: coord() };
  const to = stationary
    ? { ...from }
    : { x: coord(), y: coord(), z: coord() };
  return { objectId, segmentId: `s${i}`, t0, t1: t0 + duration, from, to };
}

function randomQuery(rand: () => number): SpatioTemporalQuery {
  const t0 = Math.floor(rand() * 200) - 50;
  const t1 = t0 + 1 + Math.floor(rand() * 60);
  const lo = () => Math.floor(rand() * 31) - 15;
  const span = () => Math.floor(rand() * 15); // possibly degenerate (a face)
  const mins = { x: lo(), y: lo(), z: lo() };
  return {
    time: { t0, t1 },
    box: {
      min: mins,
      max: { x: mins.x + span(), y: mins.y + span(), z: mins.z + span() },
    },
  };
}

function normalize(iv: MergedInterval): string {
  const src = iv.sources
    .map(
      (s) =>
        `${s.segmentId}@[${s.interval.lo},${s.interval.hi}${s.interval.hiOpen ? ")" : "]"}`,
    )
    .join("|");
  return `[${iv.lo},${iv.hi}${iv.hiOpen ? ")" : "]"} <- ${src}`;
}

function normalizeResult(res: QueryResult): string {
  const objs = [...res.objects].sort((a, b) =>
    a.objectId < b.objectId ? -1 : 1,
  );
  return objs
    .map(
      (o: ObjectResult) =>
        `${o.objectId}@v${o.version}: ${o.intervals.map(normalize).join(" ; ")}`,
    )
    .join("\n");
}

test("BVH index matches brute-force reference on randomized workloads", () => {
  const rand = rng(20261005);
  for (let trial = 0; trial < 30; trial++) {
    const idx = new SpatioTemporalIndex();
    const objectIds = ["alpha", "beta", "gamma"];
    // Two versions per object; queries also target the older snapshot.
    const versions: number[] = [];
    for (const id of objectIds) {
      const n1 = 1 + Math.floor(rand() * 12);
      const segs1 = Array.from({ length: n1 }, (_, i) =>
        randomSegment(rand, id, i),
      );
      versions.push(idx.updateTrajectory(id, segs1));
      const n2 = 1 + Math.floor(rand() * 12);
      const segs2 = Array.from({ length: n2 }, (_, i) =>
        randomSegment(rand, id, 1000 + i),
      );
      versions.push(idx.updateTrajectory(id, segs2));
    }
    for (let q = 0; q < 10; q++) {
      const query = randomQuery(rand);
      const version =
        q % 3 === 0
          ? versions[Math.floor(rand() * versions.length)]
          : undefined;
      const fast = idx.query(query, version);
      const slow = idx.queryBruteForce(query, version);
      assert.equal(
        normalizeResult(fast),
        normalizeResult(slow),
        `trial ${trial} query ${q} version ${fast.version}`,
      );
      assert.ok(fast.stats.candidateSegments <= slow.stats.candidateSegments);
      const report = verifyQueryResult(idx, query, fast);
      assert.deepEqual(
        report.errors,
        [],
        `verifier failed on trial ${trial} query ${q}`,
      );
    }
  }
});

test("degenerate point hits survive merging and match the reference", () => {
  // Two segments whose hit sets are single points 5 apart, plus one that
  // touches the box corner exactly at its own t1 (must be excluded).
  const idx = new SpatioTemporalIndex();
  idx.updateTrajectory("a", [
    {
      objectId: "a",
      segmentId: "touch5",
      t0: 0,
      t1: 10,
      from: { x: 0, y: 0, z: 0 },
      to: { x: 10, y: 0, z: 10 },
    },
    {
      objectId: "a",
      segmentId: "touchAtEnd",
      t0: 10,
      t1: 20,
      from: { x: 100, y: 100, z: 100 },
      to: { x: 5, y: 0, z: 5 },
    },
  ]);
  const query: SpatioTemporalQuery = {
    time: { t0: 0, t1: 100 },
    box: { min: { x: 5, y: 0, z: 5 }, max: { x: 5, y: 0, z: 5 } },
  };
  const fast = idx.query(query);
  const slow = idx.queryBruteForce(query);
  assert.equal(normalizeResult(fast), normalizeResult(slow));
  assert.equal(fast.objects.length, 1);
  assert.equal(fast.objects[0].intervals.length, 1);
  const iv = fast.objects[0].intervals[0];
  assert.ok(iv.lo.eq(iv.hi));
  assert.equal(iv.lo.toString(), "5");
  assert.equal(iv.sources.length, 1);
  assert.equal(iv.sources[0].segmentId, "touch5");
  assert.ok(verifyQueryResult(idx, query, fast).ok);
});
