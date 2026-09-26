import { test } from "node:test";
import assert from "node:assert/strict";
import { RegionGrower } from "../src/grower.ts";
import { colorDistance } from "../src/color.ts";

/**
 * Build an RGBA byte array from per-pixel intensity values placed in the
 * red channel only, so colorDistance between two pixels is exactly |dv|.
 */
function grays(width: number, height: number, values: number[]): number[] {
  const out: number[] = [];
  for (const v of values) out.push(v, 0, 0, 255);
  return out;
}

test("seed mode: threshold is measured against the seed color", () => {
  // 1x3: gray 0, 40, 80; seed at x=0, threshold 50.
  const g = new RegionGrower({
    width: 3, height: 1,
    pixels: grays(3, 1, [0, 40, 80]),
    connectivity: 4, threshold: 50, mode: "seed",
  });
  g.addSeed({ id: "s", x: 0, y: 0 });
  const r = g.compute();
  assert.equal(r.ownerAt(0, 0), "s");
  assert.equal(r.ownerAt(1, 0), "s");   // |40-0| <= 50
  assert.equal(r.ownerAt(2, 0), null);  // |80-0| > 50
});

test("neighbor mode: threshold is measured per edge, long chains pass", () => {
  // Same image: each step differs by 40 <= 50, so the whole row qualifies
  // even though the far end differs from the seed by 80.
  const g = new RegionGrower({
    width: 3, height: 1,
    pixels: grays(3, 1, [0, 40, 80]),
    connectivity: 4, threshold: 50, mode: "neighbor",
  });
  g.addSeed({ id: "s", x: 0, y: 0 });
  const r = g.compute();
  assert.deepEqual(r.owner, ["s", "s", "s"]);
});

test("the two threshold semantics are separate and give different results", () => {
  const pixels = grays(3, 1, [0, 40, 80]);
  const seedMode = new RegionGrower({ width: 3, height: 1, pixels, connectivity: 4, threshold: 50, mode: "seed" });
  const neighborMode = new RegionGrower({ width: 3, height: 1, pixels, connectivity: 4, threshold: 50, mode: "neighbor" });
  seedMode.addSeed({ id: "s", x: 0, y: 0 });
  neighborMode.addSeed({ id: "s", x: 0, y: 0 });
  assert.notDeepEqual(seedMode.compute().owner, neighborMode.compute().owner);
});

test("threshold boundary is inclusive", () => {
  const g = new RegionGrower({
    width: 2, height: 1,
    pixels: grays(2, 1, [0, 30]),
    connectivity: 4, threshold: 30, mode: "seed",
  });
  g.addSeed({ id: "s", x: 0, y: 0 });
  assert.equal(g.compute().ownerAt(1, 0), "s");
});

test("barrier pixels block growth and stay unowned", () => {
  // 3x1, middle pixel is a barrier.
  const g = new RegionGrower({
    width: 3, height: 1,
    pixels: grays(3, 1, [0, 0, 0]),
    connectivity: 4, threshold: 500, mode: "seed",
    barriers: [0, 1, 0],
  });
  g.addSeed({ id: "s", x: 0, y: 0 });
  const r = g.compute();
  assert.deepEqual(r.owner, ["s", null, null]);
});

test("seed on a barrier pixel throws", () => {
  const g = new RegionGrower({
    width: 2, height: 1,
    pixels: grays(2, 1, [0, 0]),
    connectivity: 4, threshold: 10, mode: "seed",
    barriers: [0, 1],
  });
  assert.throws(() => g.addSeed({ id: "bad", x: 1, y: 0 }), /barrier/);
});

test("multiple seeds on the same pixel resolve by priority then id", () => {
  const g = new RegionGrower({
    width: 2, height: 1,
    pixels: grays(2, 1, [0, 0]),
    connectivity: 4, threshold: 500, mode: "seed",
  });
  g.addSeed({ id: "aaa", x: 0, y: 0, priority: 5 });
  g.addSeed({ id: "zzz", x: 0, y: 0, priority: 1 }); // lower priority wins
  const r = g.compute();
  assert.deepEqual(r.owner, ["zzz", "zzz"]);
  assert.deepEqual(r.suppressedSeeds.map((s) => s.id), ["aaa"]);
});

test("same-pixel seeds with equal priority fall back to stable id", () => {
  const g = new RegionGrower({
    width: 1, height: 1,
    pixels: grays(1, 1, [7]),
    connectivity: 4, threshold: 0, mode: "seed",
  });
  g.addSeed({ id: "beta", x: 0, y: 0 });
  g.addSeed({ id: "alpha", x: 0, y: 0 });
  const r = g.compute();
  assert.equal(r.ownerAt(0, 0), "alpha");
  assert.deepEqual(r.suppressedSeeds.map((s) => s.id), ["beta"]);
});

test("multi-seed competition: equal bottleneck cost is decided by stable id", () => {
  // 1x3 uniform; seeds at both ends with equal priority.
  // The middle pixel has equal cost from both -> lexicographically smaller id wins.
  const g = new RegionGrower({
    width: 3, height: 1,
    pixels: grays(3, 1, [0, 0, 0]),
    connectivity: 4, threshold: 500, mode: "neighbor",
  });
  g.addSeed({ id: "right", x: 2, y: 0 });
  g.addSeed({ id: "left", x: 0, y: 0 });
  const r = g.compute();
  assert.equal(r.ownerAt(1, 0), "left");
});

test("multi-seed competition: priority beats id order", () => {
  const g = new RegionGrower({
    width: 3, height: 1,
    pixels: grays(3, 1, [0, 0, 0]),
    connectivity: 4, threshold: 500, mode: "neighbor",
  });
  g.addSeed({ id: "left", x: 0, y: 0, priority: 2 });
  g.addSeed({ id: "right", x: 2, y: 0, priority: 1 });
  const r = g.compute();
  assert.equal(r.ownerAt(1, 0), "right");
});

test("minimum bottleneck path: direct expensive edge loses to cheap detour", () => {
  // 2x2, seed at (0,0). Direct edge (0,0)->(1,0) costs 100,
  // detour (0,0)->(0,1)->(1,1)->(1,0) costs 10 per edge.
  // Neighbor mode, threshold 500: pixel (1,0) bottleneck must be 10, not 100.
  const pixels = grays(2, 2, [0, 100, 10, 10]); // row-major: (0,0)=0 (1,0)=100 (0,1)=10 (1,1)=10
  const g = new RegionGrower({ width: 2, height: 2, pixels, connectivity: 4, threshold: 500, mode: "neighbor" });
  g.addSeed({ id: "s", x: 0, y: 0 });
  const r = g.compute();
  assert.equal(r.ownerAt(1, 0), "s");
  const idx = 1; // (1,0)
  assert.ok(Math.abs(r.cost[idx] - 90) < 1e-9, `expected bottleneck 90 via detour, got ${r.cost[idx]}`);
  // pathTo must route through the cheap detour, not the direct edge.
  const path = r.pathTo(1, 0)!;
  assert.deepEqual(path.points, [
    { x: 0, y: 0 }, { x: 0, y: 1 }, { x: 1, y: 1 }, { x: 1, y: 0 },
  ]);
});

test("connectivity 4 vs 8 differ on diagonal-only passages", () => {
  // 2x2, seed at (0,0); target (1,1) only reachable diagonally
  // because orthogonal neighbors are barriers.
  const pixels = grays(2, 2, [0, 0, 0, 0]);
  const barriers = [0, 1, 1, 0];
  const g4 = new RegionGrower({ width: 2, height: 2, pixels, connectivity: 4, threshold: 500, mode: "seed", barriers });
  const g8 = new RegionGrower({ width: 2, height: 2, pixels, connectivity: 8, threshold: 500, mode: "seed", barriers });
  g4.addSeed({ id: "s", x: 0, y: 0 });
  g8.addSeed({ id: "s", x: 0, y: 0 });
  assert.equal(g4.compute().ownerAt(1, 1), null);
  assert.equal(g8.compute().ownerAt(1, 1), "s");
});

test("pathTo reconstructs a valid source path whose bottleneck equals the cost", () => {
  const g = new RegionGrower({
    width: 4, height: 1,
    pixels: grays(4, 1, [0, 3, 6, 9]),
    connectivity: 4, threshold: 500, mode: "neighbor",
  });
  g.addSeed({ id: "s", x: 0, y: 0 });
  const r = g.compute();
  const p = r.pathTo(3, 0)!;
  assert.equal(p.points.length, 4);
  assert.deepEqual(p.points[0], { x: 0, y: 0 });
  assert.deepEqual(p.points[3], { x: 3, y: 0 });
  assert.ok(Math.abs(p.cost - 3) < 1e-9); // red-channel step of 3 per edge
  assert.equal(r.pathTo(3, 0)!.cost, r.cost[3]);
});

test("pathTo returns null for unowned pixels", () => {
  const g = new RegionGrower({
    width: 2, height: 1,
    pixels: grays(2, 1, [0, 255]),
    connectivity: 4, threshold: 1, mode: "seed",
  });
  g.addSeed({ id: "s", x: 0, y: 0 });
  assert.equal(g.compute().pathTo(1, 0), null);
});

test("invalid configuration is rejected", () => {
  assert.throws(
    () => new RegionGrower({ width: 1, height: 1, pixels: grays(1, 1, [0]), connectivity: 6 as 4, threshold: 1, mode: "seed" }),
    /connectivity/,
  );
  assert.throws(
    () => new RegionGrower({ width: 1, height: 1, pixels: grays(1, 1, [0]), connectivity: 4, threshold: -1, mode: "seed" }),
    /threshold/,
  );
  assert.throws(
    () => new RegionGrower({ width: 1, height: 1, pixels: grays(1, 1, [0]), connectivity: 4, threshold: 1, mode: "mixed" as "seed" }),
    /mode/,
  );
});

test("colorDistance is Euclidean over RGBA", () => {
  assert.equal(colorDistance([0, 0, 0, 0], [0, 0, 0, 0]), 0);
  assert.equal(colorDistance([0, 0, 0, 0], [3, 4, 0, 0]), 5);
  assert.equal(colorDistance([10, 10, 10, 10], [10, 10, 10, 10]), 0);
});
