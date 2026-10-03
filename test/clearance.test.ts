import { test } from "node:test";
import assert from "node:assert/strict";
import { DistanceField } from "../src/distanceField.ts";

test("clearanceAt is the Euclidean distance to the nearest obstacle", () => {
  const f = DistanceField.fromObstacles(16, 16, [[8, 8]]);
  const v = f.version;
  assert.equal(f.clearanceAt(v, 8, 8), 0);
  assert.ok(Math.abs(f.clearanceAt(v, 11, 12) - 5) < 1e-9); // 3-4-5 triangle
  assert.ok(Math.abs(f.clearanceAt(v, 9, 9) - Math.SQRT2) < 1e-9);
});

test("path clearance inspects the whole path, not just the endpoints", () => {
  // Corridor: obstacle sits in the MIDDLE of the segment; both endpoints
  // are far away from it. An endpoints-only check would wrongly pass.
  const f = DistanceField.fromObstacles(32, 8, [[16, 4]]);
  const v = f.version;
  const path = [
    { x: 0.5, y: 4.5 },
    { x: 31.5, y: 4.5 },
  ];
  const endA = f.clearanceAt(v, 0.5, 4.5);
  const endB = f.clearanceAt(v, 31.5, 4.5);
  assert.ok(endA > 14 && endB > 14, "endpoints are far from the obstacle");

  const result = f.pathClearance(v, path);
  // The path passes straight over the obstacle cell.
  assert.ok(result.clearance < 1, `clearance ${result.clearance} should be < 1`);
  assert.ok(Math.abs(result.at.x - 16) <= 1, "minimum is at the obstacle");

  // A radius the endpoints would satisfy still fails along the path.
  assert.equal(f.isPathClear(v, path, 10), false);
  // A detour three rows above keeps clearance 3 from the obstacle.
  const detour = [
    { x: 0.5, y: 1.5 },
    { x: 31.5, y: 1.5 },
  ];
  assert.equal(f.isPathClear(v, detour, 2), true);
  assert.equal(f.isPathClear(v, detour, 4), false);
});

test("path clearance respects the field version", () => {
  const f = DistanceField.fromObstacles(24, 8, [[12, 4]]);
  const v1 = f.retain(f.version);
  const path = [
    { x: 0.5, y: 4.5 },
    { x: 23.5, y: 4.5 },
  ];
  assert.ok(f.pathClearance(v1, path).clearance < 1);

  const s = f.beginEdit();
  s.setObstacle(12, 4, false);
  s.commit();
  const v2 = f.version;
  // After removal the nearest obstacle is far away: the same path clears.
  assert.ok(f.pathClearance(v2, path).clearance > 10);
  // The retained old version still reports the obstacle.
  assert.ok(f.pathClearance(v1, path).clearance < 1);
  f.release(v1);
});

test("multi-segment polylines sample every segment", () => {
  const f = DistanceField.fromObstacles(24, 24, [[18, 18]]);
  const v = f.version;
  const path = [
    { x: 2.5, y: 2.5 },
    { x: 2.5, y: 18.5 },
    { x: 21.5, y: 18.5 }, // last segment passes straight over (18,18)
  ];
  const r = f.pathClearance(v, path);
  assert.ok(r.clearance < 1, `clearance ${r.clearance}`);
  assert.equal(f.isPathClear(v, path, 2), false);
});
