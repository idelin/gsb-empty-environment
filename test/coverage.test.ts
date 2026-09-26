import test from "node:test";
import assert from "node:assert/strict";
import { projectFace } from "../src/project.ts";
import { prepareTri, triangleCover } from "../src/raster.ts";
import { Renderer } from "../src/renderer.ts";
import { renderReference } from "../src/reference.ts";
import { RGBAImage } from "../src/image.ts";
import { VP64, screenFace } from "./helpers.ts";

test("rectangle split into two triangles is covered exactly once", () => {
  // Rect pixels [16,48) x [16,32), split along the (16,16)->(48,32) diagonal.
  const f1 = screenFace(1, [[16, 16], [48, 16], [48, 32]], [255, 0, 0, 255]);
  const f2 = screenFace(2, [[16, 16], [48, 32], [16, 32]], [0, 255, 0, 255]);
  const tris = [projectFace(f1, VP64)!, projectFace(f2, VP64)!]
    .flatMap((p) => p.tris)
    .map((t) => prepareTri(t)!);
  assert.equal(tris.length, 2);
  for (let py = 0; py < 64; py++) {
    for (let px = 0; px < 64; px++) {
      let count = 0;
      for (const t of tris) if (triangleCover(t, px, py)) count++;
      const inside = px >= 16 && px < 48 && py >= 16 && py < 32;
      assert.equal(count, inside ? 1 : 0, `pixel (${px},${py}) covered ${count} times`);
    }
  }
});

test("reversed vertex order yields identical coverage", () => {
  const pts: [number, number][] = [[10, 10], [50, 12], [20, 50]];
  const a = new Renderer(64, 64, 8);
  a.setFace(screenFace(1, pts, [200, 30, 60, 255]));
  a.renderFull();
  const b = new Renderer(64, 64, 8);
  b.setFace(screenFace(1, [pts[2], pts[1], pts[0]], [200, 30, 60, 255]));
  b.renderFull();
  assert.ok(a.image.equals(b.image), `images differ in ${a.image.diffCount(b.image)} bytes`);
  // Sanity: the triangle actually drew something.
  assert.deepEqual(a.image.get(20, 20), [200, 30, 60, 255]);
});

test("degenerate triangles render nothing and never crash", () => {
  const r = new Renderer(64, 64, 8);
  r.setFace(screenFace(1, [[10, 10], [20, 20], [30, 30]], [255, 0, 0, 255])); // collinear
  r.setFace(screenFace(2, [[40, 40], [40, 40], [40, 40]], [0, 255, 0, 255])); // point
  r.setFace(screenFace(3, [[5, 5], [5, 5], [50, 50]], [0, 0, 255, 255])); // zero area
  r.renderFull();
  const blank = new RGBAImage(64, 64);
  assert.ok(r.image.equals(blank), "degenerate geometry must not emit fragments");
});
