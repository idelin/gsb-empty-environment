import { test } from "node:test";
import assert from "node:assert/strict";
import { computeEDT, INF } from "../src/edt.ts";
import { bruteForceField, makeRng } from "./reference.ts";

function gridFrom(width: number, height: number, obstacles: Array<[number, number]>) {
  const set = new Set(obstacles.map(([x, y]) => y * width + x));
  return { width, height, obstacle: (x: number, y: number) => set.has(y * width + x) };
}

test("single obstacle: diagonal neighbour has squared distance exactly 2", () => {
  const r = computeEDT(gridFrom(5, 5, [[2, 2]]));
  const at = (x: number, y: number) => r.dist[y * 5 + x];
  assert.equal(at(3, 3), 2);   // diagonal: 1^2 + 1^2
  assert.equal(at(1, 1), 2);
  assert.equal(at(4, 2), 4);   // two cells straight
  assert.equal(at(4, 4), 8);   // 2^2 + 2^2
  assert.equal(at(2, 2), 0);   // the obstacle itself
  // source of the diagonal cell is the obstacle
  assert.equal(r.srcX[3 * 5 + 3], 2);
  assert.equal(r.srcY[3 * 5 + 3], 2);
});

test("no obstacles: every distance is Infinity, source is -1", () => {
  const r = computeEDT(gridFrom(4, 3, []));
  for (let i = 0; i < 12; i++) {
    assert.equal(r.dist[i], INF);
    assert.equal(r.srcX[i], -1);
    assert.equal(r.srcY[i], -1);
  }
});

test("equidistant sources resolve by coordinate order (x, then y)", () => {
  // obstacles at (0,0) and (4,0): cell (2,0) is equidistant (dist 4)
  const r = computeEDT(gridFrom(5, 1, [[0, 0], [4, 0]]));
  assert.equal(r.dist[2], 4);
  assert.equal(r.srcX[2], 0); // smaller source x wins
  assert.equal(r.srcY[2], 0);
  // obstacles at (0,0) and (0,4): cell (0,2) equidistant -> smaller y wins
  const r2 = computeEDT(gridFrom(1, 5, [[0, 0], [0, 4]]));
  assert.equal(r2.dist[2], 4);
  assert.equal(r2.srcY[2], 0);
});

test("separable transform matches brute force on random grids", () => {
  const rng = makeRng(12345);
  for (let iter = 0; iter < 50; iter++) {
    const w = 3 + Math.floor(rng() * 20);
    const h = 3 + Math.floor(rng() * 20);
    const obstacles: Array<[number, number]> = [];
    for (let y = 0; y < h; y++) {
      for (let x = 0; x < w; x++) {
        if (rng() < 0.15) obstacles.push([x, y]);
      }
    }
    const r = computeEDT(gridFrom(w, h, obstacles));
    const ref = bruteForceField(w, h, obstacles);
    for (let i = 0; i < w * h; i++) {
      assert.equal(r.dist[i], ref[i].dist, `dist mismatch at ${i % w},${Math.floor(i / w)}`);
      assert.equal(r.srcX[i], ref[i].srcX, `srcX mismatch at ${i % w},${Math.floor(i / w)}`);
      assert.equal(r.srcY[i], ref[i].srcY, `srcY mismatch at ${i % w},${Math.floor(i / w)}`);
    }
  }
});
