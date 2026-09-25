import assert from "node:assert/strict";
import test from "node:test";
import { quantizeImage } from "../src/pipeline.ts";
import { makeImage, setPixel, TRANSPARENT_INDEX, type RGBAImage } from "../src/types.ts";

function sample(): RGBAImage {
  const img = makeImage(12, 9);
  for (let y = 0; y < 9; y++) {
    for (let x = 0; x < 12; x++) {
      setPixel(img, x, y, (x * 21) % 256, (y * 27) % 256, ((x + 2 * y) * 13) % 256);
    }
  }
  for (let y = 0; y < 9; y++) setPixel(img, 6, y, 0, 0, 0, 0);
  for (let x = 0; x < 3; x++) setPixel(img, x, 8, 0, 0, 0, 0);
  return img;
}

test("same input produces byte-identical output across runs", () => {
  const img = sample();
  const a = quantizeImage(img, { colors: 4, dither: true, tileHeight: 2 });
  const b = quantizeImage(img, { colors: 4, dither: true, tileHeight: 2 });
  assert.deepEqual(a.palette, b.palette);
  assert.deepEqual([...a.indexed.indices], [...b.indexed.indices]);
  assert.deepEqual(a.error, b.error);
  assert.deepEqual(a.baseline, b.baseline);
});

test("colors = 1 is defined end to end", () => {
  const img = sample();
  const r = quantizeImage(img, { colors: 1, dither: true });
  assert.equal(r.palette.colors.length, 1);
  for (let p = 0; p < img.width * img.height; p++) {
    const alpha = img.data[p * 4 + 3];
    const idx = r.indexed.indices[p];
    if (alpha === 0) assert.equal(idx, TRANSPARENT_INDEX);
    else assert.equal(idx, 0);
  }
  assert.ok(r.error.rmse > 0);
});

test("reconstruction error is computable and zero when the palette covers all colors", () => {
  const img = makeImage(4, 1);
  setPixel(img, 0, 0, 255, 0, 0);
  setPixel(img, 1, 0, 0, 255, 0);
  setPixel(img, 2, 0, 0, 0, 255);
  setPixel(img, 3, 0, 0, 0, 0, 0);
  const r = quantizeImage(img, { colors: 3, dither: false });
  assert.equal(r.error.sse, 0);
  assert.equal(r.error.mse, 0);
  assert.equal(r.error.transparentPixels, 1);
  assert.equal(r.error.opaquePixels, 3);
  assert.equal(r.baseline!.error, 0);
  assert.equal(r.baseline!.gap, 0);
});

test("transparent pixels are excluded from every error statistic", () => {
  const img = makeImage(2, 1);
  setPixel(img, 0, 0, 200, 200, 200);
  setPixel(img, 1, 0, 0, 0, 0, 0);
  const r = quantizeImage(img, { colors: 1, dither: false });
  assert.equal(r.error.opaquePixels, 1);
  assert.equal(r.error.transparentPixels, 1);
  assert.equal(r.error.sse, 0); // the single opaque color is represented exactly
});

test("tileHeight does not change pipeline output", () => {
  const img = sample();
  const a = quantizeImage(img, { colors: 5, dither: true, tileHeight: 1, exactBaselineLimit: 0 });
  const b = quantizeImage(img, { colors: 5, dither: true, tileHeight: 9, exactBaselineLimit: 0 });
  assert.deepEqual([...a.indexed.indices], [...b.indexed.indices]);
});
