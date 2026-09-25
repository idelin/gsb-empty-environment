import assert from "node:assert/strict";
import test from "node:test";
import { SRGB_LINEAR_V1, SRGB_U8_V1 } from "../src/colorspace.ts";
import { createState, restoreState, snapshotState } from "../src/diffuse.ts";
import { buildPalette } from "../src/palette.ts";
import { quantizeFull, quantizeTiled } from "../src/stream.ts";
import { makeImage, setPixel, type RGBAImage } from "../src/types.ts";

function sampleImage(): RGBAImage {
  const img = makeImage(10, 7);
  for (let y = 0; y < 7; y++) {
    for (let x = 0; x < 10; x++) {
      setPixel(img, x, y, (x * 29) % 256, (y * 41) % 256, ((x + y) * 17) % 256);
    }
  }
  for (let y = 0; y < 7; y++) setPixel(img, 5, y, 0, 0, 0, 0); // barrier column
  return img;
}

test("tile height never changes the result", () => {
  const img = sampleImage();
  const palette = buildPalette(img, 4);
  const reference = quantizeFull(img, palette, SRGB_U8_V1, { dither: true, tileHeight: 7 });
  for (const th of [1, 2, 3, 4, 5, 6, 7, 100]) {
    const r = quantizeFull(img, palette, SRGB_U8_V1, { dither: true, tileHeight: th });
    assert.deepEqual([...r.indexed.indices], [...reference.indexed.indices], `tileHeight=${th}`);
  }
});

test("tile height never changes the result (no dither)", () => {
  const img = sampleImage();
  const palette = buildPalette(img, 3);
  const reference = quantizeFull(img, palette, SRGB_U8_V1, { dither: false, tileHeight: 7 });
  for (const th of [1, 2, 7]) {
    const r = quantizeFull(img, palette, SRGB_U8_V1, { dither: false, tileHeight: th });
    assert.deepEqual([...r.indexed.indices], [...reference.indexed.indices], `tileHeight=${th}`);
  }
});

test("stream can pause after a tile and resume from a JSON snapshot", () => {
  const img = sampleImage();
  const palette = buildPalette(img, 4);

  const state = createState(img.width, SRGB_U8_V1.version);
  const gen = quantizeTiled(img, palette, SRGB_U8_V1, { dither: true, tileHeight: 2, state });
  const head: number[] = [];
  const step1 = gen.next();
  assert.equal(step1.done, false);
  head.push(...step1.value!.indices);

  const saved = JSON.stringify(snapshotState(state));
  const resumed = restoreState(JSON.parse(saved));
  const tail = quantizeFull(img, palette, SRGB_U8_V1, { dither: true, tileHeight: 3, state: resumed });

  const stitched = new Int32Array(img.width * img.height);
  stitched.set(head, 0);
  stitched.set(tail.indexed.indices.slice(2 * img.width), 2 * img.width);

  const whole = quantizeFull(img, palette, SRGB_U8_V1, { dither: true, tileHeight: 2 });
  assert.deepEqual([...stitched], [...whole.indexed.indices]);
});

test("resuming with a mismatched color space version fails loudly", () => {
  const img = sampleImage();
  const palette = buildPalette(img, 4);
  const state = createState(img.width, SRGB_U8_V1.version);
  assert.throws(() => {
    const gen = quantizeTiled(img, palette, SRGB_LINEAR_V1, {
      dither: true,
      tileHeight: 2,
      state,
    });
    gen.next();
  }, /color space mismatch/);
});

test("color space version changes the quantization result", () => {
  const img = sampleImage();
  const palette = buildPalette(img, 4);
  const a = quantizeFull(img, palette, SRGB_U8_V1, { dither: true });
  const b = quantizeFull(img, palette, SRGB_LINEAR_V1, { dither: true });
  assert.notDeepEqual([...a.indexed.indices], [...b.indexed.indices]);
});
