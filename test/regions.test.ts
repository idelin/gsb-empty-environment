import { test } from "node:test";
import assert from "node:assert/strict";
import { connectedRegions, boundaryPolygons, analyzeMask } from "../src/regions.ts";
import type { Mask2D } from "../src/types.ts";

function maskFrom(rows: string[]): Mask2D {
  return rows.map((row) => [...row].map((ch) => ch === "#"));
}

test("connectedRegions splits disjoint flooded patches", () => {
  const mask = maskFrom([
    "##..",
    "##..",
    "....",
    "..##",
  ]);
  const regions = connectedRegions(mask);
  assert.equal(regions.length, 2);
  assert.deepEqual(
    regions.map((r) => r.length).sort(),
    [2, 4],
  );
});

test("connectedRegions uses 4-connectivity (diagonals stay separate)", () => {
  const mask = maskFrom([
    "#.",
    ".#",
  ]);
  assert.equal(connectedRegions(mask).length, 2);
});

test("single flooded cell yields one square boundary loop", () => {
  const mask = maskFrom([
    "...",
    ".#.",
    "...",
  ]);
  const [region] = connectedRegions(mask);
  const polygons = boundaryPolygons(mask, region);
  assert.equal(polygons.length, 1);
  const outer = polygons[0];
  assert.equal(outer.isHole, false);
  assert.equal(outer.vertices.length, 4);
  assert.equal(outer.area, 1);
});

test("L-shaped region boundary has 6 vertices and area 3", () => {
  const mask = maskFrom([
    "##.",
    "#..",
  ]);
  const [region] = connectedRegions(mask);
  const [outer] = boundaryPolygons(mask, region);
  assert.equal(outer.vertices.length, 6);
  assert.equal(outer.area, 3);
});

test("ring around a dry cell produces an outer loop plus a hole loop", () => {
  const mask = maskFrom([
    "###",
    "#.#",
    "###",
  ]);
  const [region] = connectedRegions(mask);
  const polygons = boundaryPolygons(mask, region);
  assert.equal(polygons.length, 2);
  const outer = polygons.find((p) => !p.isHole);
  const hole = polygons.find((p) => p.isHole);
  assert.ok(outer !== undefined && hole !== undefined);
  assert.equal(outer.area, 9);
  assert.equal(hole.area, 1);
  // Hole vertices are the inner square corners.
  const coords = new Set(hole.vertices.map((p) => `${p.x},${p.y}`));
  assert.deepEqual(coords, new Set(["1,1", "2,1", "2,2", "1,2"]));
});

test("boundary area equals flooded cell count for solid shapes", () => {
  const mask = maskFrom([
    ".##.",
    "####",
    "####",
    ".##.",
  ]);
  const [analysis] = analyzeMask(mask);
  const outer = analysis.polygons.find((p) => !p.isHole);
  assert.equal(analysis.cells.length, 12);
  assert.equal(outer?.area, 12);
});

test("analyzeMask reports every region with its polygons", () => {
  const mask = maskFrom([
    "#..#",
    "....",
    ".##.",
  ]);
  const analyses = analyzeMask(mask);
  assert.equal(analyses.length, 3);
  for (const a of analyses) {
    assert.equal(a.polygons.filter((p) => !p.isHole).length, 1);
  }
});
