import { test } from "node:test";
import assert from "node:assert/strict";
import {
  buildPalette,
  cloneImage,
  DiffusionEncoder,
  encodeIndexed,
  makeImage,
  revisePixels,
} from "../src/index.ts";

function gradientImage() {
  const px = [];
  let k = 0;
  for (let y = 0; y < 6; y++) {
    for (let x = 0; x < 6; x++) {
      const transparent = x === 3; // vertical barrier column
      px.push(
        transparent
          ? [0, 0, 0, 0]
          : [(k * 41) % 256, (k * 67) % 256, (k * 23) % 256, 255]
      );
      k++;
    }
  }
  return makeImage(6, 6, px);
}

test("local revision equals full re-encode from the modified image", () => {
  const baseImage = gradientImage();
  const pr = buildPalette(baseImage, { colorCount: 5 });
  const indexed = encodeIndexed(baseImage, pr.palette, { method: "floyd-steinberg" });

  // Modify a copy via the revision API.
  const revisedImage = cloneImage(baseImage);
  const revised = encodeIndexed(cloneImage(baseImage), pr.palette, {
    method: "floyd-steinberg",
  });
  const result = revisePixels(revisedImage, revised, [
    { x: 0, y: 0, rgba: [255, 0, 0, 255] },
    { x: 2, y: 1, rgba: [0, 255, 128, 255] },
  ]);

  // Ground truth: re-encode the whole modified image.
  const expected = encodeIndexed(revisedImage, pr.palette, { method: "floyd-steinberg" });

  assert.deepEqual(Array.from(result.indices), Array.from(expected.indices));
  assert.deepEqual(Array.from(result.residual), Array.from(expected.residual));
  assert.ok(result.affectedPixels >= 2);
});

test("affected cone reaches beyond the edited tile", () => {
  const baseImage = gradientImage();
  const pr = buildPalette(baseImage, { colorCount: 5 });
  const indexed = encodeIndexed(cloneImage(baseImage), pr.palette);

  const revisedImage = cloneImage(baseImage);
  const result = revisePixels(revisedImage, indexed, [
    { x: 0, y: 0, rgba: [255, 255, 255, 255] },
  ]);
  // Diffusion cone must propagate rightward and downward well past pixel 0.
  assert.ok(result.affectedPixels > 6, `cone too small: ${result.affectedPixels}`);
});

test("revision does not propagate across the transparent barrier", () => {
  const baseImage = gradientImage();
  const pr = buildPalette(baseImage, { colorCount: 5 });
  const indexed = encodeIndexed(cloneImage(baseImage), pr.palette);
  const rightSideBefore: number[] = [];
  for (let y = 0; y < 6; y++) rightSideBefore.push(indexed.indices[y * 6 + 5]!);

  const revisedImage = cloneImage(baseImage);
  revisePixels(revisedImage, indexed, [
    { x: 0, y: 0, rgba: [255, 255, 255, 255] },
    { x: 2, y: 5, rgba: [255, 0, 255, 255] },
  ]);

  // Column 5 is separated by transparent column 3; indices there unchanged.
  const rightSideAfter = [];
  for (let y = 0; y < 6; y++) rightSideAfter.push(indexed.indices[y * 6 + 5]!);
  assert.deepEqual(rightSideAfter, rightSideBefore);
});

test("editing pixels in chunks then finishing equals one streamed encode", () => {
  // Chunk boundary must not change revision results either.
  const baseImage = gradientImage();
  const pr = buildPalette(baseImage, { colorCount: 5 });
  const enc = new DiffusionEncoder(cloneImage(baseImage), pr.palette);
  for (let rows = 0; rows < 6; rows += 2) enc.processRows(2);
  const streamed = enc.result();

  const revisedImage = cloneImage(baseImage);
  const result = revisePixels(revisedImage, streamed, [
    { x: 1, y: 2, rgba: [10, 200, 40, 255] },
  ]);
  const expected = encodeIndexed(revisedImage, pr.palette);
  assert.deepEqual(Array.from(result.indices), Array.from(expected.indices));
});

test("changing an opaque pixel to transparent recomputes the downstream cone", () => {
  const baseImage = gradientImage();
  const pr = buildPalette(baseImage, { colorCount: 5 });
  const indexed = encodeIndexed(cloneImage(baseImage), pr.palette);

  const revisedImage = cloneImage(baseImage);
  const result = revisePixels(revisedImage, indexed, [
    { x: 1, y: 0, rgba: [0, 0, 0, 0] },
  ]);

  assert.equal(result.indices[1], indexed.transparentIndex);
  const expected = encodeIndexed(revisedImage, pr.palette);
  assert.deepEqual(Array.from(result.indices), Array.from(expected.indices));
  assert.ok(result.changedPixels > 0);
});
