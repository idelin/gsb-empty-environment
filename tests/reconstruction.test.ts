import { test } from "node:test";
import assert from "node:assert/strict";
import {
  buildPalette,
  encodeIndexed,
  makeImage,
  reconstruct,
  reconstructionError,
} from "../src/index.ts";

test("round trip preserves opaque palette pixels exactly when color exists", () => {
  const img = makeImage(2, 2, [
    [10, 20, 30, 255],
    [200, 100, 50, 255],
    [10, 20, 30, 255],
    [0, 0, 0, 0],
  ]);
  const pr = buildPalette(img, { colorCount: 2 });
  const out = encodeIndexed(img, pr.palette, { method: "none" });
  const recon = reconstruct(out);
  // Opaque sources are palette colors here, so zero RGB error.
  const report = reconstructionError(img, out);
  assert.equal(report.opaqueSse, 0);
  assert.equal(report.transparentLeaks, 0);
  assert.equal(recon.data[3 * 4 + 3], 0);
  assert.equal(recon.data[0 * 4 + 3], 255);
});

test("transparent pixels reconstruct to zero alpha and never map to a color", () => {
  const img = makeImage(3, 1, [
    [0, 0, 0, 127],
    [0, 0, 0, 129],
    [123, 45, 67, 255],
  ]);
  const pr = buildPalette(img, { colorCount: 1 });
  const out = encodeIndexed(img, pr.palette);
  assert.equal(out.indices[0], out.transparentIndex);
  assert.notEqual(out.indices[1], out.transparentIndex); // alpha 129 -> opaque (threshold is >128)
  assert.equal(out.indices[2], 0);
  const report = reconstructionError(img, out);
  assert.equal(report.transparentPixelCount, 1);
  assert.equal(report.transparentLeaks, 0);
});
