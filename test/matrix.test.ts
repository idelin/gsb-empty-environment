import test from "node:test";
import assert from "node:assert/strict";
import {
  IDENTITY,
  multiply,
  translate,
  scale,
  rotate,
  applyToPoint,
  invertMatrix,
} from "../src/matrix.ts";

const closeTo = (actual: [number, number], expected: [number, number]) => {
  assert.ok(
    Math.abs(actual[0] - expected[0]) < 1e-9 &&
      Math.abs(actual[1] - expected[1]) < 1e-9,
    `expected ${actual} ≈ ${expected}`,
  );
};

test("identity leaves points unchanged", () => {
  closeTo(applyToPoint(IDENTITY, 3.5, -2), [3.5, -2]);
});

test("translate / scale / rotate", () => {
  closeTo(applyToPoint(translate(2, 3), 1, 1), [3, 4]);
  closeTo(applyToPoint(scale(2, 4), 1, 1), [2, 4]);
  closeTo(applyToPoint(rotate(Math.PI / 2), 1, 0), [0, 1]);
});

test("multiply composes right-to-left (m1 * m2 applies m2 first)", () => {
  const m = multiply(translate(10, 5), multiply(scale(2, 2), rotate(Math.PI / 2)));
  // (1,0) -> rotate -> (0,1) -> scale -> (0,2) -> translate -> (10,7)
  closeTo(applyToPoint(m, 1, 0), [10, 7]);
});

test("nested transforms: parent * child matches manual composition", () => {
  const parent = multiply(translate(3, 4), scale(2, 2));
  const child = translate(1, 1);
  const composed = multiply(parent, child);
  // (0,0) -> child -> (1,1) -> parent -> (2*1+3, 2*1+4) = (5,6)
  closeTo(applyToPoint(composed, 0, 0), [5, 6]);
  // nested one more level
  const grand = rotate(Math.PI / 2);
  const deep = multiply(composed, grand);
  // (1,0) -> grand -> (0,1) -> child -> (1,2) -> parent -> (5,8)
  closeTo(applyToPoint(deep, 1, 0), [5, 8]);
});

test("invertMatrix undoes the transform", () => {
  const m = multiply(translate(3, -2), multiply(rotate(0.7), scale(2, 0.5)));
  const inv = invertMatrix(m);
  const [x, y] = applyToPoint(m, 1.25, -4.5);
  closeTo(applyToPoint(inv, x, y), [1.25, -4.5]);
  const round = multiply(inv, m);
  closeTo(applyToPoint(round, 7, 8), [7, 8]);
});
