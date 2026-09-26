import test from "node:test";
import assert from "node:assert/strict";
import { Renderer } from "../src/renderer.ts";
import { renderReference } from "../src/reference.ts";
import { VP64, clipFace, quadFaces, screenFace } from "./helpers.ts";

// Triangle with vertex C behind the camera (w < 0), crossing the near plane:
//   A = (-1,-1,0.5,1) R=255   B = (1,-1,0.5,1) R=255   C = (0,2,-1.5,-1) R=0
// Near clip (z >= -w) cuts C->A and C->B at t = 0.625, giving
//   P1 = (-0.625, 0.125, -0.25, 0.25), P2 = (0.625, 0.125, -0.25, 0.25),
//   R(P1) = R(P2) = 0.625*255 = 159.375  =>  R/w = 637.5, 1/w = 4.
// Screen: P1 -> (-48,16), P2 -> (112,16), A -> (0,64), B -> (64,64).
// Perspective-correct value at pixel center (32.5, 40.5), s = (40.5-16)/48:
//   R/w = 637.5 + s*(255-637.5) = 442.2656, 1/w = 4 + s*(1-4) = 2.46875
//   R   = 442.2656 / 2.46875    = 179.14  -> 179
// The naive mistake (projecting C unclipped) puts C at screen (32,96) and the
// pixel falls OUTSIDE the naive triangle => background. Correct = 179.
const A = { x: -1, y: -1, z: 0.5, w: 1, rgba: [255, 0, 0, 255] };
const B = { x: 1, y: -1, z: 0.5, w: 1, rgba: [255, 0, 0, 255] };
const C = { x: 0, y: 2, z: -1.5, w: -1, rgba: [0, 0, 0, 255] };

test("near-plane crossing triangle interpolates from clipped, not unclipped, coords", () => {
  const r = new Renderer(64, 64, 8);
  r.setFace(clipFace(1, [A, B, C]));
  r.renderFull();

  const [red, , , alpha] = r.image.get(32, 40);
  assert.equal(alpha, 255, "pixel must be covered (unclipped projection would miss it)");
  assert.ok(Math.abs(red - 179) <= 1, `expected perspective-correct R=179, got ${red}`);

  // Second sample for robustness: pixel (32,20) -> s = (20.5-16)/48 = 0.09375
  //   R/w = 601.64, 1/w = 3.71875 => R = 161.79 -> 162
  const [red2] = r.image.get(32, 20);
  assert.ok(Math.abs(red2 - 162) <= 1, `expected R=162 at (32,20), got ${red2}`);

  // Cross-check the whole image against the per-pixel reference.
  const ref = renderReference([clipFace(1, [A, B, C])], 64, 64, VP64);
  assert.ok(r.image.equals(ref), `tiled != reference in ${r.image.diffCount(ref)} bytes`);
});

test("geometry is clipped to the viewport rectangle", () => {
  const vp = { x: 16, y: 16, width: 32, height: 32 };
  const r = new Renderer(64, 64, 8, vp);
  // Quad covering the whole viewport (ndc +/-1 maps onto the viewport rect)...
  for (const f of quadFaces(1, 16, 16, 48, 48, [10, 200, 30, 255], vp)) r.setFace(f);
  // ...plus a huge triangle extending far outside the viewport.
  r.setFace(screenFace(9, [[-500, -500], [500, -500], [0, 500]], [10, 200, 30, 255], vp));
  r.renderFull();

  assert.deepEqual(r.image.get(15, 32), [0, 0, 0, 0], "left of viewport must stay clear");
  assert.deepEqual(r.image.get(48, 32), [0, 0, 0, 0], "right of viewport must stay clear");
  assert.deepEqual(r.image.get(32, 15), [0, 0, 0, 0], "above viewport must stay clear");
  assert.deepEqual(r.image.get(32, 48), [0, 0, 0, 0], "below viewport must stay clear");
  assert.deepEqual(r.image.get(32, 32), [10, 200, 30, 255], "inside viewport must be filled");

  const ref = renderReference(r.scene.values(), 64, 64, vp);
  assert.ok(r.image.equals(ref));
});
