import { SpatioTemporalIndex } from "../src/index.ts";
import { verifyQueryResult } from "../src/verify.ts";
import type { QueryResult, SpatioTemporalQuery } from "../src/types.ts";

const index = new SpatioTemporalIndex();

// Version 1: two objects with linear segments (integer positions/ticks).
const v1 = index.updateTrajectory("ship-1", [
  { objectId: "ship-1", segmentId: "s1", t0: 0, t1: 10, from: { x: -6, y: 0, z: 0 }, to: { x: 0, y: 0, z: 0 } },
  { objectId: "ship-1", segmentId: "s2", t0: 10, t1: 20, from: { x: 0, y: 0, z: 0 }, to: { x: 4, y: 0, z: 0 } },
]);
const v2 = index.updateTrajectory("ship-2", [
  { objectId: "ship-2", segmentId: "s1", t0: 0, t1: 30, from: { x: 0, y: 0, z: 0 }, to: { x: 0, y: 0, z: 0 } },
]);

const query: SpatioTemporalQuery = {
  time: { t0: 0, t1: 100 },
  box: { min: { x: -1, y: -1, z: -1 }, max: { x: 1, y: 1, z: 1 } },
};

function toJson(res: QueryResult): unknown {
  return {
    version: res.version,
    objects: res.objects.map((o) => ({
      objectId: o.objectId,
      trajectoryVersion: o.version,
      intervals: o.intervals.map((iv) => ({
        lo: iv.lo.toString(),
        hi: iv.hi.toString(),
        hiOpen: iv.hiOpen,
        sources: iv.sources.map((s) => ({
          segmentId: s.segmentId,
          lo: s.interval.lo.toString(),
          hi: s.interval.hi.toString(),
          hiOpen: s.interval.hiOpen,
        })),
      })),
    })),
    stats: res.stats,
  };
}

const latest = index.query(query);
console.log(`--- query at latest version (v${latest.version}) ---`);
console.log(JSON.stringify(toJson(latest), null, 2));

const report = verifyQueryResult(index, query, latest);
console.log("verification:", report.ok ? "OK" : report.errors);

// Update ship-1: it teleports away. Old snapshot v1/v2 stays queryable.
index.updateTrajectory("ship-1", [
  { objectId: "ship-1", segmentId: "s3", t0: 0, t1: 10, from: { x: 500, y: 0, z: 0 }, to: { x: 500, y: 0, z: 0 } },
]);
const old = index.query(query, v2);
console.log(`--- same query at old version (v${v2}, updates up to v${index.getVersion()} exist) ---`);
console.log(JSON.stringify(toJson(old), null, 2));
console.log(`(v1 = ${v1}, v2 = ${v2})`);
