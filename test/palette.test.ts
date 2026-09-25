import assert from "node:assert/strict";
import test from "node:test";
import { buildPalette, opaqueColors } from "../src/palette.ts";
import { makeImage, setPixel } from "../src/types.ts";

test("palette is deterministic across runs", () => {
  const img = makeImage(8, 8);
  for (let y = 0; y < 8; y++) {
    for (let x = 0; x < 8; x++) {
      setPixel(img, x, y, (x * 37 + y * 11) % 256, (x * 5 + y * 53) % 256, (x * 91 + y * 7) % 256);
    }
  }
  const a = buildPalette(img, 5);
  const b = buildPalette(img, 5);
  assert.deepEqual(a, b);
});

test("colorCount = 1 is defined: the mean of all opaque colors", () => {
  const img = makeImage(2, 1);
  setPixel(img, 0, 0, 10, 20, 30);
  setPixel(img, 1, 0, 30, 40, 50);
  const p = buildPalette(img, 1);
  assert.deepEqual(p.colors, [[20, 30, 40]]);
});

test("colorCount = 0 or fractional is rejected", () => {
  const img = makeImage(1, 1);
  setPixel(img, 0, 0, 1, 2, 3);
  assert.throws(() => buildPalette(img, 0));
  assert.throws(() => buildPalette(img, 1.5));
});

test("transparent pixels never consume the opaque color budget", () => {
  const img = makeImage(4, 4);
  // Half the image transparent; opaque pixels are exactly red and blue.
  for (let y = 0; y < 4; y++) {
    for (let x = 0; x < 4; x++) {
      if (x < 2) setPixel(img, x, y, 0, 0, 0, 0);
      else setPixel(img, x, y, y < 2 ? 255 : 0, 0, y < 2 ? 0 : 255);
    }
  }
  const p = buildPalette(img, 2);
  assert.equal(p.colors.length, 2);
  assert.deepEqual([...p.colors].sort(), [[0, 0, 255], [255, 0, 0]]);
  // And opaqueColors() skips transparent pixels entirely.
  assert.equal(opaqueColors(img).length, 8);
});

test("all-transparent image yields an empty palette", () => {
  const img = makeImage(3, 3); // alpha defaults to 0
  const p = buildPalette(img, 4);
  assert.deepEqual(p.colors, []);
});

test("fewer distinct colors than requested yields a smaller palette", () => {
  const img = makeImage(4, 1);
  setPixel(img, 0, 0, 7, 7, 7);
  setPixel(img, 1, 0, 7, 7, 7);
  setPixel(img, 2, 0, 200, 100, 50);
  setPixel(img, 3, 0, 200, 100, 50);
  const p = buildPalette(img, 8);
  assert.equal(p.colors.length, 2);
});
