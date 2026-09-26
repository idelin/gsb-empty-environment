import test from "node:test";
import assert from "node:assert/strict";
import { Renderer } from "../src/renderer.ts";
import { renderReference } from "../src/reference.ts";
import { VP64, clipFace, quadFaces, screenFace, shuffled, xorshift } from "./helpers.ts";

function buildScene(r: Renderer): void {
  for (const f of quadFaces(1, 0, 0, 64, 64, [40, 80, 120, 255])) r.setFace(f); // background
  r.setFace(screenFace(10, [[20, 20], [40, 20], [28, 36]], [255, 0, 0, 255], VP64, -0.5)); // foreground (closer)
}

test("deleting a foreground triangle restores occluded background, re-rendering only affected tiles", () => {
  const r = new Renderer(64, 64, 8);
  buildScene(r);
  r.renderFull();
  assert.deepEqual(r.image.get(28, 28), [255, 0, 0, 255], "foreground visible before delete");

  const before = r.stats.tilesRendered;
  r.removeFace(10);
  const rerendered = r.stats.tilesRendered - before;

  // Foreground bbox pixels x[20,41) y[20,37) => tiles x 2..5, y 2..4 => 12 tiles.
  assert.equal(rerendered, 12, "only the tiles under the removed face may be re-rendered");
  assert.deepEqual(r.image.get(28, 28), [40, 80, 120, 255], "occluded background must be restored");

  const ref = renderReference(r.scene.values(), 64, 64, VP64);
  assert.ok(r.image.equals(ref), "incremental result must equal a full re-render");
});

test("moving a face re-renders old+new tiles and matches the reference", () => {
  const r = new Renderer(64, 64, 8);
  buildScene(r);
  r.renderFull();
  r.setFace(screenFace(10, [[44, 44], [60, 44], [52, 58]], [0, 0, 255, 255], VP64, -0.5)); // move + recolor
  assert.deepEqual(r.image.get(28, 28), [40, 80, 120, 255], "old location restored");
  assert.deepEqual(r.image.get(52, 50), [0, 0, 255, 255], "new location drawn");
  const ref = renderReference(r.scene.values(), 64, 64, VP64);
  assert.ok(r.image.equals(ref));
});

test("random tile execution order matches the single-threaded per-pixel reference", () => {
  const faces = [
    ...quadFaces(1, 0, 0, 64, 64, [30, 60, 90, 255], VP64, 0), // opaque bg
    screenFace(3, [[10, 10], [54, 10], [10, 54]], [255, 0, 0, 255], VP64, -0.5), // tie pair...
    screenFace(7, [[10, 10], [54, 10], [10, 54]], [0, 255, 0, 255], VP64, -0.5), // ...same depth
    ...quadFaces(20, 8, 8, 40, 56, [255, 0, 0, 128], VP64, -0.25), // transparent red
    ...quadFaces(22, 24, 8, 56, 56, [0, 0, 255, 128], VP64, -0.25), // transparent blue
    clipFace(30, [ // near-plane crossing triangle
      { x: -1, y: -1, z: 0.5, w: 1, rgba: [255, 255, 0, 255] },
      { x: 1, y: -1, z: 0.5, w: 1, rgba: [255, 255, 0, 255] },
      { x: 0, y: 2, z: -1.5, w: -1, rgba: [0, 0, 0, 255] },
    ]),
  ];
  const ref = renderReference(faces, 64, 64, VP64);
  for (const seed of [1, 7, 42, 1337, 999983]) {
    const r = new Renderer(64, 64, 8);
    for (const f of faces) r.setFace(f);
    r.renderFull(shuffled(64, xorshift(seed))); // 8x8 = 64 tiles, shuffled
    assert.ok(r.image.equals(ref), `seed ${seed}: ${r.image.diffCount(ref)} bytes differ`);
  }
});

test("fixed-point overflow saturates instead of corrupting output", () => {
  const faces = [
    screenFace(1, [[-1e6, -1e6], [1e6, -1e6], [0, 1e6]], [9, 99, 199, 255]), // gigantic, covers viewport
    screenFace(2, [[-Infinity, 0], [Infinity, 0], [0, Infinity]], [1, 2, 3, 255]), // infinities saturate
    screenFace(3, [[NaN, 0], [10, 0], [0, 10]], [4, 5, 6, 255]), // NaN => face dropped
  ];
  let r: Renderer | null = null;
  assert.doesNotThrow(() => {
    r = new Renderer(64, 64, 8);
    for (const f of faces) r.setFace(f);
    r.renderFull();
  });
  assert.deepEqual(r!.image.get(32, 32), [9, 99, 199, 255], "huge triangle must fill the viewport");
  const ref = renderReference(faces, 64, 64, VP64);
  assert.ok(r!.image.equals(ref));
});
