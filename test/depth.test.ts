import test from "node:test";
import assert from "node:assert/strict";
import { Renderer } from "../src/renderer.ts";
import { renderReference } from "../src/reference.ts";
import { VP64, quadFaces, screenFace } from "./helpers.ts";

test("equal depth is resolved by stable face id, independent of insertion order", () => {
  const pts: [number, number][] = [[10, 10], [54, 10], [10, 54]];
  const red = screenFace(3, pts, [255, 0, 0, 255]);
  const green = screenFace(7, pts, [0, 255, 0, 255]);

  const r1 = new Renderer(64, 64, 8);
  r1.setFace(red);
  r1.setFace(green);
  r1.renderFull();

  const r2 = new Renderer(64, 64, 8);
  r2.setFace(green); // reversed insertion order
  r2.setFace(red);
  r2.renderFull();

  assert.deepEqual(r1.image.get(20, 20), [255, 0, 0, 255], "smaller face id must win the tie");
  assert.ok(r1.image.equals(r2.image), "tie-break must not depend on insertion order");

  const ref = renderReference([green, red], 64, 64, VP64);
  assert.ok(r1.image.equals(ref));
});

test("transparent layers blend deterministically in ascending face-id order", () => {
  const build = (): Renderer => {
    const r = new Renderer(64, 64, 8);
    for (const f of quadFaces(1, 0, 0, 64, 64, [255, 255, 255, 255])) r.setFace(f); // opaque white bg
    for (const f of quadFaces(20, 8, 8, 40, 56, [255, 0, 0, 128])) r.setFace(f); // red layer
    for (const f of quadFaces(22, 24, 8, 56, 56, [0, 0, 255, 128])) r.setFace(f); // blue layer
    r.renderFull();
    return r;
  };
  const r = build();

  // Single red layer over white: (255,127,127,191)
  assert.deepEqual(r.image.get(16, 32), [255, 127, 127, 191]);
  // Red then blue (id order): (127,63,191,159)
  assert.deepEqual(r.image.get(32, 32), [127, 63, 191, 159]);
  // Single blue layer over white: (127,127,255,191)
  assert.deepEqual(r.image.get(48, 32), [127, 127, 255, 191]);
  // Untouched background
  assert.deepEqual(r.image.get(4, 4), [255, 255, 255, 255]);

  const ref = renderReference(r.scene.values(), 64, 64, VP64);
  assert.ok(r.image.equals(ref), `tiled != reference in ${r.image.diffCount(ref)} bytes`);
});

test("transparent fragment behind opaque geometry is hidden by the depth test", () => {
  const r = new Renderer(64, 64, 8);
  for (const f of quadFaces(1, 0, 0, 64, 64, [10, 10, 10, 255], VP64, 0)) r.setFace(f); // bg at z=0
  for (const f of quadFaces(3, 16, 16, 48, 48, [255, 255, 0, 128], VP64, 0.5)) r.setFace(f); // behind bg
  r.renderFull();
  assert.deepEqual(r.image.get(32, 32), [10, 10, 10, 255], "transparent layer behind opaque must be occluded");
});
