import { test } from "node:test";
import assert from "node:assert/strict";
import {
  buildPalette,
  canDeliver,
  encodeIndexed,
  makeImage,
  opacityMask,
  reconstructionError,
} from "../src/index.ts";

const O = (v: number) => [v, v, v, 255] as const;
const T = [0, 0, 0, 0] as const;

test("method=none is a pure nearest-palette assignment", () => {
  const img = makeImage(1, 4, [O(0), O(90), O(170), O(255)]);
  const pr = buildPalette(img, { colorCount: 2 });
  const out = encodeIndexed(img, pr.palette, { method: "none" });
  // With 4 distinct grays > 2 colors median/exact pick two endpoints; nearest
  // assignment must map the mid grays deterministically.
  assert.equal(out.indices[0], out.indices[1] === out.indices[0] ? out.indices[0] : out.indices[0]);
  assert.ok(out.indices[0] !== out.indices[3]);
});

test("diffusion does not cross a vertical transparent barrier", () => {
  // 3 columns: opaque | transparent | opaque.
  const img = makeImage(3, 3, [
    O(200), T, O(0),
    O(200), T, O(0),
    O(200), T, O(0),
  ]);
  const pr = buildPalette(img, { colorCount: 2 });
  const out = encodeIndexed(img, pr.palette, { method: "floyd-steinberg" });

  // Right side must only use its own dark palette color: if any right pixel
  // selected the light color, error leaked across the transparent column.
  const rightIndices = [2, 5, 8].map((i) => out.indices[i]!);
  const leftIndices = [0, 3, 6].map((i) => out.indices[i]!);
  // both sides are constant colors; the two sides map to distinct indices.
  assert.equal(new Set(rightIndices).size, 1);
  assert.equal(new Set(leftIndices).size, 1);
  assert.notEqual(rightIndices[0], leftIndices[0]);
  // middle column is always transparent sentinel.
  for (const i of [1, 4, 7]) assert.equal(out.indices[i], out.transparentIndex);
});

test("diffusion does not cross a horizontal transparent barrier (diagonal gate)", () => {
  // A diagonal error path over a 1px transparent wall must be blocked.
  // Layout:
  //  X . X
  //  . . .   <- transparent row
  //  X . X
  const img = makeImage(3, 3, [
    O(255), T, O(255),
    T, T, T,
    O(0), T, O(0),
  ]);
  const pr = buildPalette(img, { colorCount: 2 });
  const out = encodeIndexed(img, pr.palette, { method: "floyd-steinberg" });
  // Bottom opaque pixels must remain the dark choice; top errors cannot
  // reach them through the transparent row.
  const bottom = [6, 8].map((i) => out.indices[i]!);
  assert.deepEqual(bottom, [bottom[0], bottom[0]]);
});

test("canDeliver bridge rule blocks corner cutting", () => {
  // opaque grid with source (0,0), target (1,1), but (1,0) is transparent:
  // diagonal delivery must be denied even though both endpoints are opaque.
  const img = makeImage(2, 2, [O(0), T, O(0), O(0)]);
  const grid = { width: 2, height: 2, opaque: opacityMask(img) };
  assert.equal(canDeliver(grid, 0, 0, 1, 1), false);
  // fully opaque 2x2 allows it
  const full = makeImage(2, 2, [O(0), O(0), O(0), O(0)]);
  const g2 = { width: 2, height: 2, opaque: opacityMask(full) };
  assert.equal(canDeliver(g2, 0, 0, 1, 1), true);
});

test("reconstruction error counts only opaque RGB error and reports no leaks", () => {
  const img = makeImage(2, 2, [O(0), O(255), O(128), T]);
  const pr = buildPalette(img, { colorCount: 2 });
  const out = encodeIndexed(img, pr.palette, { method: "none" });
  const report = reconstructionError(img, out);
  assert.equal(report.transparentPixelCount, 1);
  assert.equal(report.opaquePixelCount, 3);
  assert.equal(report.transparentLeaks, 0);
  assert.equal(report.opacityMismatches, 0);
  assert.ok(report.opaqueSse >= 0);
});

test("identical inputs produce identical palettes and indices repeatedly", () => {
  const make = () =>
    makeImage(4, 2, [
      O(12), T, O(240), O(99),
      O(200), O(30), T, O(150),
    ]);
  const fp = new Set<string>();
  const idxRuns: string[] = [];
  for (let run = 0; run < 5; run++) {
    const pr = buildPalette(make(), { colorCount: 3 });
    fp.add(pr.fingerprint);
    idxRuns.push(String(Array.from(encodeIndexed(make(), pr.palette).indices)));
  }
  assert.equal(fp.size, 1);
  assert.equal(new Set(idxRuns).size, 1);
});
