import { test } from "node:test";
import assert from "node:assert/strict";
import {
  COLOR_SPACE_VERSION,
  DiffusionEncoder,
  buildPalette,
  encodeIndexed,
  frontierFromJSON,
  makeImage,
} from "../src/index.ts";

function sampleImage() {
  const px = [];
  let k = 0;
  for (let y = 0; y < 7; y++) {
    for (let x = 0; x < 5; x++) {
      // Deterministic pseudo-variation with a transparent cross barrier.
      const transparent = x === 2 || y === 3;
      if (transparent) {
        px.push([0, 0, 0, 0]);
      } else {
        px.push([(k * 37) % 256, (k * 91) % 256, (k * 53) % 256, 255]);
      }
      k++;
    }
  }
  return makeImage(5, 7, px);
}

test("chunk sizes do not change the result vs one-shot", () => {
  const image = sampleImage();
  const pr = buildPalette(image, { colorCount: 4 });
  const oneShot = encodeIndexed(image, pr.palette, { method: "floyd-steinberg" });

  for (const chunks of [
    [7],
    [1, 1, 1, 1, 1, 1, 1],
    [3, 1, 2, 1],
    [2, 3, 2],
    [5, 5, 5],
  ]) {
    const enc = new DiffusionEncoder(image, pr.palette);
    const collected: number[] = [];
    for (const rows of chunks) {
      const part = enc.processRows(rows);
      collected.push(...Array.from(part.indices));
    }
    assert.ok(enc.done);
    assert.deepEqual(collected, Array.from(oneShot.indices));
    assert.deepEqual(
      Array.from(enc.result().residual),
      Array.from(oneShot.residual)
    );
  }
});

test("saved/restored frontier continues identically", () => {
  const image = sampleImage();
  const pr = buildPalette(image, { colorCount: 4 });
  const oneShot = encodeIndexed(image, pr.palette, { method: "floyd-steinberg" });

  const enc = new DiffusionEncoder(image, pr.palette);
  enc.processRows(2);
  enc.processRows(1);
  const state = enc.saveFrontier();
  assert.equal(state.colorSpaceVersion, COLOR_SPACE_VERSION);
  assert.equal(state.row, 3);

  const json = enc.toJSON();
  const restoredState = frontierFromJSON(json);
  const enc2 = DiffusionEncoder.restore(image, pr.palette, restoredState);
  while (!enc2.done) enc2.processRows(2);

  assert.deepEqual(Array.from(enc2.result().indices), Array.from(oneShot.indices));
});

test("restoring an incompatible color-space version is rejected", () => {
  const image = sampleImage();
  const pr = buildPalette(image, { colorCount: 4 });
  const enc = new DiffusionEncoder(image, pr.palette);
  enc.processRows(1);
  const state = enc.saveFrontier();
  const tampered = { ...state, colorSpaceVersion: "srgb-rgb-fs-v0" };
  assert.throws(
    () => DiffusionEncoder.restore(image, pr.palette, tampered),
    /incompatible color space version/
  );
});

test("method=none streaming also matches one-shot", () => {
  const image = sampleImage();
  const pr = buildPalette(image, { colorCount: 4 });
  const oneShot = encodeIndexed(image, pr.palette, { method: "none" });
  const enc = new DiffusionEncoder(image, pr.palette, { method: "none" });
  while (!enc.done) enc.processRows(2);
  assert.deepEqual(Array.from(enc.result().indices), Array.from(oneShot.indices));
});
