import { test } from "node:test";
import assert from "node:assert/strict";
import { rasterizeSegment, rasterizeRoad, RoadNetwork } from "../src/roads.ts";
import { createGrid } from "../src/types.ts";

test("rasterizeSegment walks a horizontal line cell by cell", () => {
  const cells = rasterizeSegment({ x: 0.5, y: 0.5 }, { x: 3.5, y: 0.5 });
  assert.deepEqual(cells, [
    { row: 0, col: 0 },
    { row: 0, col: 1 },
    { row: 0, col: 2 },
    { row: 0, col: 3 },
  ]);
});

test("rasterizeSegment covers every cell a diagonal touches", () => {
  const cells = rasterizeSegment({ x: 0.5, y: 0.5 }, { x: 2.5, y: 2.5 });
  assert.deepEqual(cells, [
    { row: 0, col: 0 },
    { row: 1, col: 1 },
    { row: 2, col: 2 },
  ]);
});

test("rasterizeRoad joins waypoints without duplicate junction cells", () => {
  const cells = rasterizeRoad([
    { x: 0.5, y: 0.5 },
    { x: 2.5, y: 0.5 },
    { x: 2.5, y: 2.5 },
  ]);
  assert.deepEqual(cells, [
    { row: 0, col: 0 },
    { row: 0, col: 1 },
    { row: 0, col: 2 },
    { row: 1, col: 2 },
    { row: 2, col: 2 },
  ]);
});

test("breakpointsAt reports only cells deeper than the threshold", () => {
  const network = new RoadNetwork([
    {
      id: "R1",
      waypoints: [
        { x: 0.5, y: 0.5 },
        { x: 4.5, y: 0.5 },
      ],
    },
  ]);
  const depth = createGrid(1, 5, 0);
  depth[0][2] = 8;
  depth[0][3] = 6;
  const segments = network.breakpointsAt(depth, 7, 5);
  assert.equal(segments.length, 1);
  assert.equal(segments[0].roadId, "R1");
  assert.equal(segments[0].tick, 7);
  assert.deepEqual(segments[0].cells, [
    { row: 0, col: 2 },
    { row: 0, col: 3 },
  ]);
  assert.equal(segments[0].maxDepth, 8);
});

test("breakpointsAt splits interrupted runs into separate segments", () => {
  const network = new RoadNetwork([
    {
      id: "R2",
      waypoints: [
        { x: 0.5, y: 0.5 },
        { x: 5.5, y: 0.5 },
      ],
    },
  ]);
  const depth = createGrid(1, 6, 0);
  depth[0][1] = 10;
  depth[0][4] = 10;
  depth[0][5] = 10;
  const segments = network.breakpointsAt(depth, 3, 5);
  assert.equal(segments.length, 2);
  assert.deepEqual(segments[0].cells, [{ row: 0, col: 1 }]);
  assert.deepEqual(segments[1].cells, [
    { row: 0, col: 4 },
    { row: 0, col: 5 },
  ]);
});

test("breakpointsAt returns an empty list when all roads are passable", () => {
  const network = new RoadNetwork([
    {
      id: "R3",
      waypoints: [
        { x: 0.5, y: 0.5 },
        { x: 2.5, y: 0.5 },
      ],
    },
  ]);
  const depth = createGrid(1, 3, 1);
  assert.deepEqual(network.breakpointsAt(depth, 0, 5), []);
});
