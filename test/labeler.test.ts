import test from "node:test";
import assert from "node:assert/strict";
import { RGBAImage } from "../src/rgba-image.ts";
import { ComponentLabeler } from "../src/labeler.ts";

const DIAGONAL = [
  "#.",
  ".#",
];

test("RGBA foreground predicate: opaque dark = on, transparent/light = off", () => {
  const img = new RGBAImage(2, 3);
  img.setPixel(0, 0, 0, 0, 0, 255);       // opaque black -> foreground
  img.setPixel(1, 0, 0, 0, 0, 0);         // transparent black -> background
  img.setPixel(0, 1, 255, 255, 255, 255); // opaque white -> background
  img.setPixel(1, 1, 10, 10, 10, 100);    // dark but too transparent -> background
  img.setPixel(0, 2, 127, 127, 127, 255); // just below threshold -> foreground
  img.setPixel(1, 2, 128, 128, 128, 255); // at threshold -> background
  assert.equal(img.isForeground(0, 0), true);
  assert.equal(img.isForeground(1, 0), false);
  assert.equal(img.isForeground(0, 1), false);
  assert.equal(img.isForeground(1, 1), false);
  assert.equal(img.isForeground(0, 2), true);
  assert.equal(img.isForeground(1, 2), false);
});

test("4-connectivity: diagonal pixels stay in separate components", () => {
  const labeler = new ComponentLabeler(RGBAImage.fromAscii(DIAGONAL), 4);
  assert.equal(labeler.componentCount, 2);
  assert.notEqual(labeler.labelAt(0, 0), 0);
  assert.notEqual(labeler.labelAt(1, 1), 0);
  assert.notEqual(labeler.labelAt(0, 0), labeler.labelAt(1, 1));
});

test("8-connectivity: diagonal pixels form one component", () => {
  const labeler = new ComponentLabeler(RGBAImage.fromAscii(DIAGONAL), 8);
  assert.equal(labeler.componentCount, 1);
  assert.equal(labeler.labelAt(0, 0), labeler.labelAt(1, 1));
  assert.equal(labeler.componentSize(labeler.labelAt(0, 0)), 2);
});

test("removing a bridge pixel splits the component in two", () => {
  // top row is a bridge between two 2x2 blocks
  const img = RGBAImage.fromAscii([
    "#####",
    "##.##",
  ]);
  const labeler = new ComponentLabeler(img, 4);
  assert.equal(labeler.componentCount, 1);
  assert.equal(labeler.componentSize(1), 9);

  img.setForeground(2, 0, false); // cut the bridge
  const update = labeler.updatePixel(img, 2, 0);
  assert.equal(update.changed, true);

  assert.equal(labeler.componentCount, 2);
  // anchor (0,0) is on the left, so the left block keeps id 1
  assert.equal(labeler.labelAt(0, 0), 1);
  assert.equal(labeler.labelAt(1, 1), 1);
  const rightId = labeler.labelAt(4, 0);
  assert.notEqual(rightId, 0);
  assert.notEqual(rightId, 1);
  assert.equal(labeler.labelAt(3, 1), rightId);
  assert.equal(labeler.componentSize(1), 4);
  assert.equal(labeler.componentSize(rightId), 4);
});

test("adding a bridge pixel merges components; the larger keeps its ID", () => {
  const img = RGBAImage.fromAscii([
    "###.##",
    "###.##",
  ]);
  const labeler = new ComponentLabeler(img, 4);
  assert.equal(labeler.componentCount, 2);
  const leftId = labeler.labelAt(0, 0);   // size 6
  const rightId = labeler.labelAt(5, 1);  // size 4
  assert.equal(labeler.componentSize(leftId), 6);
  assert.equal(labeler.componentSize(rightId), 4);

  img.setForeground(3, 0, true); // build the bridge
  labeler.updatePixel(img, 3, 0);

  assert.equal(labeler.componentCount, 1);
  assert.equal(labeler.labelAt(3, 0), leftId);  // new pixel joins the survivor
  assert.equal(labeler.labelAt(5, 1), leftId);  // smaller side is relabeled
  assert.equal(labeler.componentSize(leftId), 11);
  assert.equal(labeler.componentSize(rightId), 0); // old ID is gone
});

test("unrelated components keep stable IDs across local edits", () => {
  const img = RGBAImage.fromAscii([
    "###...",
    "#.....",
    "....##",
  ]);
  const labeler = new ComponentLabeler(img, 4);
  const idA = labeler.labelAt(0, 0);
  const idB = labeler.labelAt(4, 2);
  assert.equal(idA, 1);
  assert.equal(idB, 2);
  assert.deepEqual(labeler.representativeOf(idA), { x: 0, y: 0 });

  // split A by removing its middle bridge pixel (1,0)
  img.setForeground(1, 0, false);
  labeler.updatePixel(img, 1, 0);
  assert.equal(labeler.componentCount, 3);
  // the part holding the representative (0,0) keeps idA
  assert.equal(labeler.labelAt(0, 0), idA);
  assert.equal(labeler.labelAt(0, 1), idA);
  const idC = labeler.labelAt(2, 0);
  assert.notEqual(idC, idA);
  assert.notEqual(idC, idB);
  // B is untouched
  assert.equal(labeler.labelAt(4, 2), idB);
  assert.equal(labeler.labelAt(5, 2), idB);
  assert.equal(labeler.componentSize(idB), 2);

  // merge A and C back by restoring the bridge pixel
  img.setForeground(1, 0, true);
  labeler.updatePixel(img, 1, 0);
  assert.equal(labeler.componentCount, 2);
  assert.equal(labeler.labelAt(2, 0), idA); // larger part (idA) absorbs idC
  assert.equal(labeler.labelAt(1, 0), idA);
  assert.equal(labeler.componentSize(idC), 0);
  // B still untouched
  assert.equal(labeler.labelAt(4, 2), idB);
  assert.equal(labeler.labelAt(5, 2), idB);
  assert.deepEqual(labeler.representativeOf(idA), { x: 0, y: 0 });
});

test("splitting off the representative pixel hands the ID to the largest part", () => {
  const img = RGBAImage.fromAscii([
    "#..",
    "###",
  ]);
  const labeler = new ComponentLabeler(img, 4);
  const id = labeler.labelAt(0, 0);
  assert.deepEqual(labeler.representativeOf(id), { x: 0, y: 0 });

  img.setForeground(0, 0, false); // remove the representative itself
  labeler.updatePixel(img, 0, 0);

  assert.equal(labeler.componentCount, 1); // bottom row is still connected
  assert.equal(labeler.labelAt(1, 1), id); // largest (only) part keeps the ID
  assert.equal(labeler.componentSize(id), 3);
});

test("isolated pixel add/remove creates and retires component IDs", () => {
  const img = RGBAImage.fromAscii([
    "#..",
    "...",
  ]);
  const labeler = new ComponentLabeler(img, 4);
  assert.equal(labeler.componentCount, 1);

  img.setForeground(2, 1, true);
  labeler.updatePixel(img, 2, 1);
  assert.equal(labeler.componentCount, 2);
  const newId = labeler.labelAt(2, 1);
  assert.notEqual(newId, 0);
  assert.notEqual(newId, labeler.labelAt(0, 0));

  img.setForeground(2, 1, false);
  labeler.updatePixel(img, 2, 1);
  assert.equal(labeler.componentCount, 1);
  assert.equal(labeler.labelAt(2, 1), 0);
  assert.equal(labeler.componentSize(newId), 0);
});

test("updatePixel reports no change when the image matches tracked state", () => {
  const img = RGBAImage.fromAscii(["#"]);
  const labeler = new ComponentLabeler(img, 4);
  const update = labeler.updatePixel(img, 0, 0);
  assert.equal(update.changed, false);
  assert.equal(labeler.componentCount, 1);
});

test("8-connectivity: removing a diagonal hinge splits the component", () => {
  const img = RGBAImage.fromAscii([
    "#..",
    ".#.",
    "..#",
  ]);
  const labeler = new ComponentLabeler(img, 8);
  assert.equal(labeler.componentCount, 1);

  img.setForeground(1, 1, false); // remove the hinge
  labeler.updatePixel(img, 1, 1);

  assert.equal(labeler.componentCount, 2);
  assert.notEqual(labeler.labelAt(0, 0), labeler.labelAt(2, 2));
});

test("rebuild reassigns deterministic scan-order IDs", () => {
  const img = RGBAImage.fromAscii([
    "#.#",
    "...",
    "#.#",
  ]);
  const labeler = new ComponentLabeler(img, 4);
  assert.equal(labeler.componentCount, 4);
  assert.equal(labeler.labelAt(0, 0), 1);
  assert.equal(labeler.labelAt(2, 0), 2);
  assert.equal(labeler.labelAt(0, 2), 3);
  assert.equal(labeler.labelAt(2, 2), 4);
});
