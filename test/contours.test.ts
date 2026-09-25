import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { extractContours, extractContoursFromMask, fillContours, masksEqual } from "../src/contours.ts";
import { createRgbaImage, maskToRgba, rasterizeRgba, rgbaToMask, setPixel, type RgbaImage } from "../src/image.ts";

const RING_3X3 = ["###", "#.#", "###"];

function assertClosedAndUnique(image: RgbaImage): void {
  const contours = extractContours(image);
  for (const contour of contours) {
    assert.ok(contour.vertices.length >= 4);
    assert.equal(new Set(contour.vertices.map((point) => `${point.x},${point.y}`)).size, contour.vertices.length);
    assert.equal(contour.edges.length, contour.vertices.length);

    for (let i = 0; i < contour.edges.length; i += 1) {
      const edge = contour.edges[i];
      assert.deepEqual(edge.from, contour.vertices[i]);
      assert.deepEqual(edge.to, contour.vertices[(i + 1) % contour.vertices.length]);
      assert.equal(Math.abs(edge.to.x - edge.from.x) + Math.abs(edge.to.y - edge.from.y), 1);
    }
  }
}

function assertRoundTrip(image: RgbaImage, options?: { alphaThreshold?: number }): void {
  const original = rgbaToMask(image, options);
  const contours = extractContours(image, options);
  const filled = fillContours(contours, image.width, image.height);
  assert.ok(masksEqual(original, filled));
}

describe("RGBA binary image contour extraction", () => {
  it("returns no contours for an empty image", () => {
    const image = createRgbaImage(4, 3);
    assert.deepEqual(extractContours(image), []);
    assert.deepEqual(fillContours([], 4, 3), new Uint8Array(12));
  });

  it("turns one pixel into four unit boundary edges", () => {
    const image = createRgbaImage(3, 2);
    setPixel(image, 1, 0, [255, 0, 0, 255]);

    const [contour] = extractContours(image);
    assert.equal(contour.kind, "outer");
    assert.equal(contour.signedArea, 1);
    assert.deepEqual(contour.vertices, [
      { x: 1, y: 0 },
      { x: 2, y: 0 },
      { x: 2, y: 1 },
      { x: 1, y: 1 },
    ]);
    assert.equal(contour.edges.length, 4);
    assertRoundTrip(image);
    assertClosedAndUnique(image);
  });

  it("handles foreground objects touching all image borders", () => {
    const image = rasterizeRgba(["##.", "###", "..#"]);
    const contours = extractContours(image);

    assert.equal(contours.length, 1);
    assert.equal(contours[0].kind, "outer");
    assert.equal(contours[0].signedArea, 6);
    assert.deepEqual(contours[0].vertices[0], { x: 0, y: 0 });
    assertRoundTrip(image);
    assertClosedAndUnique(image);
  });

  it("keeps a fully foreground image closed on all four image borders", () => {
    const image = rasterizeRgba(["##", "##"]);
    const contours = extractContours(image);

    assert.equal(contours.length, 1);
    assert.equal(contours[0].kind, "outer");
    assert.equal(contours[0].edges.length, 8);
    assert.deepEqual(contours[0].vertices, [
      { x: 0, y: 0 },
      { x: 1, y: 0 },
      { x: 2, y: 0 },
      { x: 2, y: 1 },
      { x: 2, y: 2 },
      { x: 1, y: 2 },
      { x: 0, y: 2 },
      { x: 0, y: 1 },
    ]);
    assertRoundTrip(image);
  });

  it("extracts a hole with the opposite signed orientation", () => {
    const image = rasterizeRgba(RING_3X3);
    const contours = extractContours(image);

    assert.deepEqual(contours.map((contour) => contour.kind), ["outer", "hole"]);
    assert.deepEqual(contours.map((contour) => contour.signedArea), [9, -1]);
    assert.deepEqual(contours[1].vertices, [
      { x: 2, y: 1 },
      { x: 1, y: 1 },
      { x: 1, y: 2 },
      { x: 2, y: 2 },
    ]);
    assertRoundTrip(image);
    assertClosedAndUnique(image);
  });

  it("does not bridge diagonally touching foreground cells into a hole", () => {
    const image = rasterizeRgba(["#.", ".#"]);
    const contours = extractContours(image);

    assert.equal(contours.length, 2);
    assert.deepEqual(contours.map((contour) => contour.kind), ["outer", "outer"]);
    assert.deepEqual(contours.map((contour) => contour.signedArea), [1, 1]);
    assert.deepEqual(contours[0].vertices[0], { x: 0, y: 0 });
    assert.deepEqual(contours[1].vertices[0], { x: 1, y: 1 });
    assertRoundTrip(image);
    assertClosedAndUnique(image);
  });

  it("preserves a nested outer inside a hole after fill", () => {
    const art = [
      "#######",
      "#.....#",
      "#.###.#",
      "#.###.#",
      "#.###.#",
      "#.....#",
      "#######",
    ];
    const image = rasterizeRgba(art);
    const mask = rgbaToMask(image);
    const contours = extractContoursFromMask(mask, image.width, image.height);
    const filled = fillContours(contours, image.width, image.height);

    assert.deepEqual(contours.map((contour) => contour.kind), ["outer", "hole", "outer"]);
    assert.deepEqual(
      contours.map((contour) => Math.sign(contour.signedArea)),
      [1, -1, 1],
    );
    assert.deepEqual(contours.map((contour) => contour.signedArea), [49, -25, 9]);
    assert.ok(masksEqual(mask, filled));
    assertClosedAndUnique(image);
  });

  it("uses alpha threshold and supports custom foreground predicates", () => {
    const thresholdImage = createRgbaImage(3, 1);
    setPixel(thresholdImage, 0, 0, [0, 0, 0, 100]);
    setPixel(thresholdImage, 2, 0, [0, 0, 0, 200]);

    assert.equal(extractContours(thresholdImage, { alphaThreshold: 128 }).length, 1);
    assert.deepEqual(extractContours(thresholdImage, { alphaThreshold: 50 }).length, 2);

    const predicateImage = rasterizeRgba(["RG", "GB"], {
      ".": [0, 0, 0, 0],
      R: [255, 0, 0, 255],
      G: [0, 255, 0, 255],
      B: [0, 0, 255, 255],
    });
    const contours = extractContours(predicateImage, {
      isForeground: ({ r, g }) => r > g,
    });

    assert.equal(contours.length, 1);
    assert.deepEqual(contours[0].vertices[0], { x: 0, y: 0 });
  });

  it("reconstructs a mask to the exact original RGBA alpha mask", () => {
    const art = [
      "...###..",
      "..#####.",
      "..##.##.",
      "...####.",
      "....##..",
      ".#......",
      "##......",
    ];
    const image = rasterizeRgba(art);
    const original = rgbaToMask(image);
    const contours = extractContours(image);
    const reconstructed = maskToRgba(fillContours(contours, image.width, image.height), image.width, image.height);

    assert.ok(masksEqual(original, rgbaToMask(reconstructed)));
    assertClosedAndUnique(image);
  });
});
