import assert from "node:assert/strict";
import test from "node:test";
import { SRGB_U8_V1 } from "../src/colorspace.ts";
import { buildPalette } from "../src/palette.ts";
import { affectedRegion } from "../src/revision.ts";
import { quantizeFull } from "../src/stream.ts";
import { makeImage, setPixel, type RGBAImage } from "../src/types.ts";

function gradientImage(w: number, h: number): RGBAImage {
  const img = makeImage(w, h);
  for (let y = 0; y < h; y++) {
    for (let x = 0; x < w; x++) {
      setPixel(img, x, y, (x * 23) % 256, (y * 31) % 256, ((x * y) % 16) * 16);
    }
  }
  return img;
}

test("a revision propagates far beyond its own tile", () => {
  const img = gradientImage(12, 8);
  const spans = affectedRegion(img, { x: 4, y: 2, width: 1, height: 1 }, true);
  // The wedge must reach the bottom of the image and widen as it goes:
  // not just the modified tile.
  const last = spans[spans.length - 1];
  assert.equal(last.y, 7);
  assert.ok(last.x1 > 4, "wedge widens to the right");
  assert.ok(last.x0 < 4, "wedge widens to the left");
  // On the edited row itself, error pushes right along the row: the span
  // starts at x=4 and extends to the right edge.
  assert.deepEqual(spans[0], { y: 2, x0: 4, x1: 11 });
});

test("transparent barriers clip the propagation wedge", () => {
  const img = gradientImage(12, 8);
  for (let y = 0; y < 8; y++) setPixel(img, 7, y, 0, 0, 0, 0); // barrier column
  const spans = affectedRegion(img, { x: 4, y: 2, width: 1, height: 1 }, true);
  for (const s of spans) {
    assert.ok(s.x1 <= 6, `row ${s.y} must stop at the barrier, got x1=${s.x1}`);
  }
});

test("without dithering the affected region is exactly the rect", () => {
  const img = gradientImage(12, 8);
  const spans = affectedRegion(img, { x: 3, y: 2, width: 2, height: 2 }, false);
  assert.deepEqual(spans, [
    { y: 2, x0: 3, x1: 4 },
    { y: 3, x0: 3, x1: 4 },
  ]);
});

test("pixels outside the affected region are provably unchanged", () => {
  const img = gradientImage(12, 8);
  const palette = buildPalette(img, 4);
  const before = quantizeFull(img, palette, SRGB_U8_V1, { dither: true, tileHeight: 3 });

  const rect = { x: 5, y: 3, width: 1, height: 1 };
  const spans = affectedRegion(img, rect, true);
  const affected = new Set<number>();
  for (const s of spans) {
    for (let x = s.x0; x <= s.x1; x++) affected.add(s.y * img.width + x);
  }

  const edited = gradientImage(12, 8);
  setPixel(edited, 5, 3, 255, 0, 128);
  // Same fixed palette: a local pixel revision must not ripple outside
  // the affected region.
  const after = quantizeFull(edited, palette, SRGB_U8_V1, { dither: true, tileHeight: 3 });

  // Sanity: the edit did change something inside the region.
  let changedInside = 0;
  let changedOutside = 0;
  for (let p = 0; p < before.indexed.indices.length; p++) {
    if (before.indexed.indices[p] !== after.indexed.indices[p]) {
      if (affected.has(p)) changedInside++;
      else changedOutside++;
    }
  }
  assert.ok(changedInside > 0, "edit should change something");
  assert.equal(changedOutside, 0, "nothing outside the affected region may change");
});
