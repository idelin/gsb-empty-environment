import { test } from "node:test";
import assert from "node:assert/strict";
import { Dem } from "../src/dem.ts";
import { RainfallSeries } from "../src/rainfall.ts";
import { FloodSimulator } from "../src/simulator.ts";
import { createGrid } from "../src/types.ts";

test("rainfall accumulates as ponded water on flat impermeable terrain", () => {
  const dem = Dem.constant(4, 4, 10);
  const rain = RainfallSeries.uniform([5, 5, 5], 4, 4);
  const sim = new FloodSimulator(dem, rain, createGrid(4, 4, 0));
  sim.run(3);
  // Flat terrain spreads water evenly; nothing infiltrates or leaves.
  assert.ok(Math.abs(sim.totalDepthAt(3) - 15 * 16) < 1e-9);
  const depth = sim.depthAt(3);
  for (const row of depth) {
    for (const d of row) {
      assert.ok(Math.abs(d - 15) < 1e-6, `expected uniform 15mm, got ${d}`);
    }
  }
});

test("infiltration removes water each tick", () => {
  const dem = Dem.constant(2, 2, 0);
  const rain = RainfallSeries.uniform([10], 2, 2);
  const sim = new FloodSimulator(dem, rain, createGrid(2, 2, 3));
  sim.step();
  // 10mm rain - 3mm infiltration = 7mm per cell.
  assert.ok(Math.abs(sim.totalDepthAt(1) - 7 * 4) < 1e-9);
  sim.step(); // no more rain, infiltration keeps draining
  assert.ok(Math.abs(sim.totalDepthAt(2) - 4 * 4) < 1e-9);
});

test("water flows downhill along the terrain slope", () => {
  // West edge high, east edge low; rain only on the west column.
  const elevation = createGrid(1, 5, 0);
  for (let c = 0; c < 5; c++) elevation[0][c] = (4 - c) * 10;
  const dem = new Dem(elevation);
  const rainFrames = [Array.from({ length: 1 }, () => [50, 0, 0, 0, 0])];
  const rain = new RainfallSeries(rainFrames, 1, 5);
  const sim = new FloodSimulator(dem, rain, createGrid(1, 5, 0), { flowCoeff: 0.4 });
  sim.run(6);
  const depth = sim.depthAt(6);
  // Water must have migrated eastward: deepest ponding at the lowest cell.
  const east = depth[0][4];
  const west = depth[0][0];
  assert.ok(east > west, `expected east (${east}) > west (${west})`);
  // Conservation: nothing infiltrated, total preserved.
  assert.ok(Math.abs(sim.totalDepthAt(6) - 50) < 1e-6);
});

test("water ponds in a depression instead of escaping", () => {
  // 3x3 pit: rim at 10m, center at 0m.
  const elevation = createGrid(3, 3, 10);
  elevation[1][1] = 0;
  const dem = new Dem(elevation);
  const rain = RainfallSeries.uniform([20, 20], 3, 3);
  const sim = new FloodSimulator(dem, rain, createGrid(3, 3, 0));
  sim.run(8);
  const depth = sim.depthAt(8);
  assert.ok(depth[1][1] > 0, "pit should pond water");
  // Rim cells are 10m higher; their depth stays far below the pit's.
  assert.ok(depth[0][0] < depth[1][1]);
});

test("history snapshots expose any past tick and reject future ticks", () => {
  const dem = Dem.constant(3, 3, 0);
  const rain = RainfallSeries.uniform([4, 4, 4, 4], 3, 3);
  const sim = new FloodSimulator(dem, rain, createGrid(3, 3, 0));
  sim.run(4);
  assert.equal(sim.tick, 4);
  assert.ok(sim.totalDepthAt(1) < sim.totalDepthAt(4));
  assert.throws(() => sim.depthAt(5), RangeError);
  assert.throws(() => sim.depthAt(-1), RangeError);
});

test("flooded mask respects the depth threshold", () => {
  const dem = Dem.constant(2, 2, 0);
  const rain = RainfallSeries.uniform([3], 2, 2);
  const sim = new FloodSimulator(dem, rain, createGrid(2, 2, 0), {
    floodThreshold: 3,
  });
  sim.step();
  const mask = sim.floodedMaskAt(1);
  assert.deepEqual(mask, [
    [true, true],
    [true, true],
  ]);
  const strict = sim.floodedMaskAt(1, 4);
  assert.deepEqual(strict, [
    [false, false],
    [false, false],
  ]);
});

test("spatially varying rainfall only wets the cells it hits", () => {
  const dem = Dem.constant(2, 2, 100); // flat & high: no inflow from dry cells
  const frame = [
    [8, 0],
    [0, 0],
  ];
  const rain = new RainfallSeries([frame], 2, 2);
  const sim = new FloodSimulator(dem, rain, createGrid(2, 2, 0));
  sim.step();
  const depth = sim.depthAt(1);
  assert.ok(depth[0][0] > 0);
  // Neighbours only receive what flows over, less than the rained cell.
  assert.ok(depth[0][1] < depth[0][0]);
  assert.ok(depth[1][1] < depth[0][0]);
});

test("open boundaries drain ponded water off the domain", () => {
  const dem = Dem.constant(4, 4, 0);
  const rain = RainfallSeries.uniform([10], 4, 4);
  const sim = new FloodSimulator(dem, rain, createGrid(4, 4, 0), {
    flowCoeff: 0.5,
    openBoundary: true,
  });
  sim.step();
  const afterRain = sim.totalDepthAt(1);
  sim.run(10);
  const later = sim.totalDepthAt(11);
  assert.ok(afterRain < 10 * 16, "edge cells drain already in the first tick");
  assert.ok(later < afterRain * 0.2, `expected drainage, still ${later}`);
});
