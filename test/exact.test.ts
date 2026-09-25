import assert from "node:assert/strict";
import test from "node:test";
import { SRGB_U8_V1 } from "../src/colorspace.ts";
import { exactBaseline } from "../src/exact.ts";
import { assignmentError, quantizeImage } from "../src/pipeline.ts";
import { buildPalette } from "../src/palette.ts";
import { makeImage, setPixel } from "../src/types.ts";

test("exact baseline matches brute force on a tiny domain", () => {
  // 4 distinct colors, K=2: S(4,1)+S(4,2)=8 partitions, checkable by hand.
  const img = makeImage(4, 1);
  setPixel(img, 0, 0, 0, 0, 0);
  setPixel(img, 1, 0, 10, 0, 0);
  setPixel(img, 2, 0, 200, 0, 0);
  setPixel(img, 3, 0, 210, 0, 0);
  const b = exactBaseline(img, 2, SRGB_U8_V1)!;
  assert.equal(b.partitions, 8);
  // Optimal split {0,10}/{200,210} with centroids 5 and 205:
  // per pair 2 * 5^2 = 50 -> total 100.
  assert.equal(b.error, 100);
  assert.deepEqual(
    [...b.palette.colors].sort((a, c) => a[0] - c[0]),
    [[5, 0, 0], [205, 0, 0]],
  );
});

test("K >= distinct colors gives zero exact error", () => {
  const img = makeImage(3, 1);
  setPixel(img, 0, 0, 1, 2, 3);
  setPixel(img, 1, 0, 4, 5, 6);
  setPixel(img, 2, 0, 1, 2, 3);
  const b = exactBaseline(img, 5, SRGB_U8_V1)!;
  assert.equal(b.distinctColors, 2);
  assert.equal(b.error, 0);
});

test("transparent pixels are excluded from the exact baseline", () => {
  const img = makeImage(3, 1);
  setPixel(img, 0, 0, 0, 0, 0, 0); // transparent, must be ignored
  setPixel(img, 1, 0, 100, 0, 0);
  setPixel(img, 2, 0, 100, 0, 0);
  const b = exactBaseline(img, 1, SRGB_U8_V1)!;
  assert.equal(b.distinctColors, 1);
  assert.equal(b.error, 0);
});

test("oversized domains return null instead of a baseline", () => {
  const img = makeImage(16, 16);
  for (let y = 0; y < 16; y++) {
    for (let x = 0; x < 16; x++) setPixel(img, x, y, x * 16, y * 16, (x + y) * 8);
  }
  assert.equal(exactBaseline(img, 8, SRGB_U8_V1, 10), null);
});

test("heuristic reports its gap and never claims optimality", () => {
  const img = makeImage(6, 2);
  const colors: [number, number, number][] = [
    [0, 0, 0], [12, 0, 0], [250, 0, 0], [0, 250, 0], [0, 0, 250], [240, 240, 0],
    [5, 5, 5], [18, 0, 0], [245, 5, 0], [5, 245, 0], [0, 5, 245], [235, 235, 5],
  ];
  colors.forEach((c, i) => setPixel(img, i % 6, Math.floor(i / 6), c[0], c[1], c[2]));

  const report = quantizeImage(img, { colors: 3, dither: false });
  const b = report.baseline!;
  assert.ok(b, "baseline should exist for a small domain");
  assert.equal(b.claimsOptimal, false);
  assert.ok(b.gap >= -1e-9, `gap must be >= 0, got ${b.gap}`);
  assert.ok(Math.abs(b.heuristicError - (b.error + b.gap)) < 1e-9);

  // The exact palette is at least as good as the heuristic palette.
  const heuristic = buildPalette(img, 3);
  const heuristicErr = assignmentError(img, heuristic, SRGB_U8_V1);
  assert.ok(heuristicErr >= b.error - 1e-9);
});
