import test from "node:test";
import assert from "node:assert/strict";
import { RGBAImage, paint, applyMask } from "../src/image.ts";
import { Mask } from "../src/mask.ts";

test("paint composites exact bytes through a mask", () => {
  const img = new RGBAImage(2, 1);
  const mask = new Mask(2, 1, new Float32Array([1, 0.5]));
  paint(img, mask, [200, 100, 40, 255]);
  assert.deepEqual(img.pixel(0, 0), [200, 100, 40, 255]);
  // alpha 255*0.5 = 127.5 -> Uint8ClampedArray rounds half to even -> 128
  assert.deepEqual(img.pixel(1, 0), [100, 50, 20, 128]);
});

test("paint uses source-over against the existing background", () => {
  const img = RGBAImage.filled(1, 1, [0, 0, 0, 255]);
  paint(img, Mask.filled(1, 1, 1), [255, 255, 255, 128]);
  assert.deepEqual(img.pixel(0, 0), [128, 128, 128, 255]);
});

test("applyMask multiplies alpha; repeated application follows the math definition", () => {
  const img = RGBAImage.filled(2, 2, [10, 20, 30, 200]);
  const half = Mask.filled(2, 2, 0.5);
  applyMask(img, half);
  assert.equal(img.pixel(0, 0)[3], 100); // 200 * 0.5
  applyMask(img, half);
  assert.equal(img.pixel(1, 1)[3], 50); // NOT idempotent: multiplied again
  applyMask(img, half);
  assert.equal(img.pixel(0, 1)[3], 25);
  // RGB channels untouched
  assert.deepEqual(img.pixel(0, 0).slice(0, 3), [10, 20, 30]);
});

test("applyMask with zero coverage zeroes alpha only", () => {
  const img = RGBAImage.filled(1, 1, [7, 8, 9, 250]);
  applyMask(img, new Mask(1, 1));
  assert.deepEqual(img.pixel(0, 0), [7, 8, 9, 0]);
});
