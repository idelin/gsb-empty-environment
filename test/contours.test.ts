import assert from "node:assert/strict";
import test from "node:test";

import { extractContours, type Ring } from "../src/contours.ts";
import { fillRings } from "../src/fill.ts";
import { maskFromRgba, masksEqual, type Mask } from "../src/mask.ts";
import { RgbaImage } from "../src/rgba-image.ts";

function expectRoundTrip(mask: Mask): Ring[] {
  const rings = extractContours(mask);
  const refilled = fillRings(rings, mask.width, mask.height);
  assert.ok(
    masksEqual(refilled, mask),
    "filling the extracted rings must reproduce the original mask",
  );
  return rings;
}

function expectNoRepeatedVertices(rings: Ring[]): void {
  for (const ring of rings) {
    const seen = new Set(ring.vertices.map((p) => `${p.x},${p.y}`));
    assert.equal(
      seen.size,
      ring.vertices.length,
      `ring must not repeat vertices: ${JSON.stringify(ring.vertices)}`,
    );
  }
}

test("empty image yields no contours", () => {
  const rings = extractContours(new RgbaImage(4, 4).toMask());
  assert.deepEqual(rings, []);
});

test("RGBA to mask: opaque bright = foreground, transparent/dark = background", () => {
  // 2x1: opaque white pixel, then transparent pixel.
  const rgba = [255, 255, 255, 255, 0, 0, 0, 0];
  const mask = maskFromRgba(rgba, 2, 1);
  assert.deepEqual([...mask.data], [1, 0]);
  // Opaque but dark pixel is background.
  const dark = maskFromRgba([10, 10, 10, 255], 1, 1);
  assert.deepEqual([...dark.data], [0]);
});

test("single pixel forms a closed ring of four edges", () => {
  const img = new RgbaImage(3, 3).fg(1, 1);
  const rings = expectRoundTrip(img.toMask());
  assert.equal(rings.length, 1);
  const ring = rings[0];
  assert.equal(ring.kind, "outer");
  assert.equal(ring.closed, true);
  assert.equal(ring.perimeter, 4);
  assert.equal(ring.signedArea, 1);
  assert.deepEqual(ring.vertices, [
    { x: 1, y: 1 },
    { x: 2, y: 1 },
    { x: 2, y: 2 },
    { x: 1, y: 2 },
  ]);
  assert.deepEqual(ring.pixels, [{ x: 1, y: 1 }]);
});

test("solid block yields one outer ring with correct area and perimeter", () => {
  const img = new RgbaImage(6, 6).fillRect(2, 2, 3, 3);
  const rings = expectRoundTrip(img.toMask());
  assert.equal(rings.length, 1);
  assert.equal(rings[0].kind, "outer");
  assert.equal(rings[0].signedArea, 9);
  assert.equal(rings[0].perimeter, 12);
  expectNoRepeatedVertices(rings);
});

test("ring direction distinguishes holes from outer contours", () => {
  // 5x5 block with a 1x1 hole in the middle.
  const img = new RgbaImage(7, 7).fillRect(1, 1, 5, 5).clear(3, 3);
  const rings = expectRoundTrip(img.toMask());
  assert.equal(rings.length, 2);
  const outer = rings.find((r) => r.kind === "outer");
  const hole = rings.find((r) => r.kind === "hole");
  assert.ok(outer && hole, "expected one outer ring and one hole");
  assert.ok(outer.signedArea > 0, "outer ring is clockwise (positive area)");
  assert.ok(hole.signedArea < 0, "hole is counter-clockwise (negative area)");
  assert.equal(outer.signedArea, 25);
  assert.equal(hole.signedArea, -1);
  // Hole boundary pixels are the foreground pixels surrounding the hole.
  assert.deepEqual(hole.pixels, [
    { x: 3, y: 2 },
    { x: 2, y: 3 },
    { x: 3, y: 4 },
    { x: 4, y: 3 },
  ]);
  expectNoRepeatedVertices(rings);
});

test("diagonal contact splits into separate simple rings, no phantom hole", () => {
  // Two pixels touching only at a corner.
  const img = new RgbaImage(3, 3).fg(0, 0).fg(1, 1);
  const rings = expectRoundTrip(img.toMask());
  assert.equal(rings.length, 2);
  assert.ok(rings.every((r) => r.kind === "outer"));
  assert.ok(rings.every((r) => r.perimeter === 4 && r.signedArea === 1));
  expectNoRepeatedVertices(rings);

  // 2x2 checkerboard: background pixels touch diagonally (8-connected),
  // so no hole is reported.
  const checker = new RgbaImage(2, 2).fg(0, 0).fg(1, 1);
  const checkerRings = expectRoundTrip(checker.toMask());
  assert.equal(checkerRings.length, 2);
  assert.ok(checkerRings.every((r) => r.kind === "outer"));
});

test("objects touching the image edge are closed along the border", () => {
  // 2x2 block in the top-left corner.
  const img = new RgbaImage(4, 4).fillRect(0, 0, 2, 2);
  const rings = expectRoundTrip(img.toMask());
  assert.equal(rings.length, 1);
  const ring = rings[0];
  assert.equal(ring.kind, "outer");
  assert.equal(ring.signedArea, 4);
  assert.deepEqual(ring.vertices, [
    { x: 0, y: 0 },
    { x: 1, y: 0 },
    { x: 2, y: 0 },
    { x: 2, y: 1 },
    { x: 2, y: 2 },
    { x: 1, y: 2 },
    { x: 0, y: 2 },
    { x: 0, y: 1 },
  ]);

  // Full-width bar touching left and right borders.
  const bar = new RgbaImage(5, 4).fillRect(0, 1, 5, 2);
  const barRings = expectRoundTrip(bar.toMask());
  assert.equal(barRings.length, 1);
  assert.equal(barRings[0].signedArea, 10);
});

test("concave shape with a hole round-trips exactly", () => {
  // U-shaped object with a separate hole above it.
  const img = new RgbaImage(9, 9);
  img.fillRect(1, 1, 7, 7); // 7x7 block
  img.clear(4, 2); // hole near the top
  // Carve a concave notch from the bottom edge.
  for (let y = 5; y <= 7; y++) {
    for (let x = 3; x <= 5; x++) img.clear(x, y);
  }
  const rings = expectRoundTrip(img.toMask());
  assert.equal(rings.filter((r) => r.kind === "outer").length, 1);
  assert.equal(rings.filter((r) => r.kind === "hole").length, 1);
  expectNoRepeatedVertices(rings);
});

test("multiple disjoint objects each get their own outer ring", () => {
  const img = new RgbaImage(10, 5)
    .fillRect(0, 0, 2, 2)
    .fillRect(4, 1, 3, 3)
    .fg(9, 4);
  const rings = expectRoundTrip(img.toMask());
  assert.equal(rings.length, 3);
  assert.ok(rings.every((r) => r.kind === "outer"));
  assert.deepEqual(
    rings.map((r) => r.signedArea).sort((a, b) => a - b),
    [1, 4, 9],
  );
  expectNoRepeatedVertices(rings);
});
