import assert from "node:assert/strict";
import test from "node:test";
import { SRGB_U8_V1 } from "../src/colorspace.ts";
import {
  createState,
  processRow,
  restoreState,
  snapshotState,
} from "../src/diffuse.ts";
import { paletteInSpace } from "../src/quantize.ts";
import { makeImage, setPixel, TRANSPARENT_INDEX } from "../src/types.ts";

const PALETTE = { colors: [[0, 0, 0], [255, 255, 255]] as [number, number, number][] };

test("transparent pixels are barriers: error does not cross them", () => {
  // Row 0: mid-gray, mid-gray, TRANSPARENT, mid-gray.
  // Without the barrier, error from x=0/1 would push into x=2/3 and also
  // down into row 1. With the barrier, x=3 and everything below x=2 must
  // see zero accumulated error.
  const img = makeImage(4, 2);
  for (let y = 0; y < 2; y++) {
    for (let x = 0; x < 4; x++) setPixel(img, x, y, 128, 128, 128);
  }
  setPixel(img, 2, 0, 0, 0, 0, 0);
  setPixel(img, 2, 1, 0, 0, 0, 0);

  const ps = paletteInSpace(PALETTE, SRGB_U8_V1);
  const state = createState(4, SRGB_U8_V1.version);
  const out = new Int32Array(4);
  processRow(img, 0, PALETTE, ps, SRGB_U8_V1, true, state, out);

  assert.equal(out[2], TRANSPARENT_INDEX);
  // x=3 saw zero incoming error (the barrier at x=2 swallowed everything
  // coming from the left), so plain 128 quantizes to white (index 1).
  assert.equal(out[3], 1);
  // After the row, buffers have rotated: state.errCur now holds the
  // accumulator for row 1. Deposits into the transparent pixel (2,1)
  // were skipped entirely.
  assert.equal(state.errCur[2 * 3], 0);
  assert.equal(state.errCur[2 * 3 + 1], 0);
  assert.equal(state.errCur[2 * 3 + 2], 0);
  // Error still flows down from opaque pixels right of the barrier:
  // (3,1) receives 5/16 of the quantization error of (3,0).
  assert.ok(Math.abs(state.errCur[3 * 3] - ((128 - 255) * 5) / 16) < 1e-9);
});

test("frontier snapshot/restore is bit-exact", () => {
  const img = makeImage(6, 4);
  for (let y = 0; y < 4; y++) {
    for (let x = 0; x < 6; x++) setPixel(img, x, y, (x * 40) % 256, (y * 60) % 256, 128);
  }
  const ps = paletteInSpace(PALETTE, SRGB_U8_V1);
  const out = new Int32Array(6);

  const a = createState(6, SRGB_U8_V1.version);
  processRow(img, 0, PALETTE, ps, SRGB_U8_V1, true, a, out);
  processRow(img, 1, PALETTE, ps, SRGB_U8_V1, true, a, out);

  const snap = JSON.parse(JSON.stringify(snapshotState(a)));
  const b = restoreState(snap);
  assert.equal(b.colorSpaceVersion, "srgb-u8-v1");
  assert.equal(b.nextRow, 2);

  const outA = new Int32Array(6);
  const outB = new Int32Array(6);
  processRow(img, 2, PALETTE, ps, SRGB_U8_V1, true, a, outA);
  processRow(img, 2, PALETTE, ps, SRGB_U8_V1, true, b, outB);
  assert.deepEqual([...outA], [...outB]);
  assert.deepEqual([...a.errCur], [...b.errCur]);
  assert.deepEqual([...a.errNext], [...b.errNext]);
});

test("restore rejects unknown color space versions and bad shapes", () => {
  const good = snapshotState(createState(2, "srgb-u8-v1"));
  assert.throws(() => restoreState({ ...good, colorSpaceVersion: "lab-v9" }));
  assert.throws(() => restoreState({ ...good, errCur: [1, 2, 3] }));
  assert.throws(() => restoreState({ ...good, format: 999 }));
});

test("dither disabled leaves the frontier untouched", () => {
  const img = makeImage(3, 1);
  for (let x = 0; x < 3; x++) setPixel(img, x, 0, 128, 128, 128);
  const ps = paletteInSpace(PALETTE, SRGB_U8_V1);
  const state = createState(3, SRGB_U8_V1.version);
  processRow(img, 0, PALETTE, ps, SRGB_U8_V1, false, state, new Int32Array(3));
  assert.deepEqual([...state.errCur], [0, 0, 0, 0, 0, 0, 0, 0, 0]);
  assert.deepEqual([...state.errNext], [0, 0, 0, 0, 0, 0, 0, 0, 0]);
});
