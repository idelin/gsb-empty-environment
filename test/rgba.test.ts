import { test } from "node:test";
import assert from "node:assert/strict";
import { DistanceField } from "../src/distanceField.ts";
import {
  createImage,
  setPixel,
  fillRect,
  getPixel,
  obstaclesFromImage,
} from "../src/rgba.ts";
import { bruteForceField } from "./reference.ts";

test("hand-built RGBA image decodes to obstacles without any image library", () => {
  // 24x16 image, transparent background, opaque red obstacles drawn
  // pixel-by-pixel and with a filled rect. Decoded manually from bytes.
  const img = createImage(24, 16);
  setPixel(img, 3, 3, 255, 0, 0, 255);
  setPixel(img, 4, 4, 255, 0, 0, 255);
  fillRect(img, 16, 10, 4, 3, 200, 30, 30, 255);
  setPixel(img, 10, 8, 255, 0, 0, 100); // semi-transparent: NOT an obstacle

  assert.deepEqual(getPixel(img, 3, 3), [255, 0, 0, 255]);
  assert.deepEqual(getPixel(img, 0, 0), [0, 0, 0, 0]);

  const obstacles = obstaclesFromImage(img);
  assert.equal(obstacles.length, 2 + 4 * 3);
  assert.ok(!obstacles.some(([x, y]) => x === 10 && y === 8));

  const f = DistanceField.fromObstacles(img.width, img.height, obstacles);
  assertMatchesBruteForce(f, obstacles);

  // Diagonal neighbour of the isolated pixel (3,3): squared distance 2.
  assert.equal(f.cellAt(f.version, 4, 4).obstacle, true);
  assert.equal(f.cellAt(f.version, 5, 5).dist, 2);
  assert.equal(f.cellAt(f.version, 5, 5).srcX, 4);
  assert.equal(f.cellAt(f.version, 5, 5).srcY, 4);
});

test("custom predicate: decode obstacles from the blue channel", () => {
  const img = createImage(8, 8);
  setPixel(img, 2, 2, 0, 0, 255, 40); // blue but nearly transparent
  const obstacles = obstaclesFromImage(img, (_r, _g, b) => b > 200);
  assert.deepEqual(obstacles, [[2, 2]]);
  const f = DistanceField.fromObstacles(8, 8, obstacles);
  assert.equal(f.cellAt(f.version, 4, 4).dist, 8);
});

test("image-origin offset maps pixels into world coordinates", () => {
  const img = createImage(4, 4);
  setPixel(img, 1, 1, 255, 255, 255, 255);
  // Place the image so its (0,0) pixel lands on world (-10, -6).
  const obstacles = obstaclesFromImage(img, undefined, -10, -6);
  assert.deepEqual(obstacles, [[-9, -5]]);
  const f = DistanceField.fromObstacles(16, 16, obstacles, -12, -8);
  const c = f.cellAt(f.version, -8, -4); // diagonal neighbour
  assert.equal(c.dist, 2);
  assert.equal(c.srcX, -9);
  assert.equal(c.srcY, -5);
});

function assertMatchesBruteForce(
  f: DistanceField,
  obstacles: Array<[number, number]>,
): void {
  const ref = bruteForceField(f.width, f.height, obstacles);
  const v = f.version;
  for (let y = 0; y < f.height; y++) {
    for (let x = 0; x < f.width; x++) {
      const c = f.cellAt(v, x, y);
      const r = ref[y * f.width + x];
      assert.equal(c.dist, r.dist, `dist at ${x},${y}`);
      assert.equal(c.srcX, r.srcX, `srcX at ${x},${y}`);
      assert.equal(c.srcY, r.srcY, `srcY at ${x},${y}`);
    }
  }
}
