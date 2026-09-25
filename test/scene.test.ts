import test from "node:test";
import assert from "node:assert/strict";
import { renderScene, group, shapeNode, invertNode } from "../src/scene.ts";
import { referenceCoverage } from "../src/reference.ts";
import { rectShape, pathShape } from "../src/geometry.ts";
import { translate, rotate, scale, multiply } from "../src/matrix.ts";
import { assertCoverageClose } from "./helpers.ts";

const FAST_SS = 12;
const REF_SS = 48;

test("group transform places children at expected pixels (exact)", () => {
  const scene = group("union", [shapeNode(rectShape(0, 0, 2, 2))], translate(3, 4));
  const m = renderScene(scene, 8, 8, 4);
  assert.equal(m.at(3, 4), 1);
  assert.equal(m.at(4, 5), 1);
  assert.equal(m.at(2, 4), 0);
  assert.equal(m.at(5, 5), 0);
});

test("rotate composed with translate lands exactly (exact)", () => {
  const t = multiply(translate(4, 1), rotate(Math.PI / 2));
  const scene = shapeNode(rectShape(0, 0, 2, 1), t);
  const m = renderScene(scene, 8, 8, 4);
  assert.equal(m.at(3, 1), 1);
  assert.equal(m.at(3, 2), 1);
  assert.equal(m.at(4, 1), 0);
  assert.equal(m.at(3, 3), 0);
});

test("nested transforms (3 levels) match the per-pixel reference", () => {
  const scene = group(
    "union",
    [
      shapeNode(rectShape(-2, -1, 4, 2)),
      group(
        "union",
        [shapeNode(pathShape([[0, -2, 2, 2, -2, 2]]), translate(1, 0))],
        multiply(rotate(0.35), scale(1.2, 0.8)),
      ),
    ],
    multiply(translate(6, 6), rotate(0.2)),
  );
  assertCoverageClose(renderScene(scene, 12, 12, FAST_SS), referenceCoverage(scene, 12, 12, REF_SS));
});

test("boolean subtract: rect minus (diamond union rect) matches reference", () => {
  const scene = group("subtract", [
    shapeNode(rectShape(1, 1, 10, 10)),
    group("union", [
      shapeNode(pathShape([[6, 2, 10, 6, 6, 10, 2, 6]])),
      shapeNode(rectShape(4, 7, 5, 2), rotate(0.1)),
    ]),
  ]);
  assertCoverageClose(renderScene(scene, 12, 12, FAST_SS), referenceCoverage(scene, 12, 12, REF_SS));
});

test("boolean intersect of two transformed shapes matches reference", () => {
  const scene = group("intersect", [
    shapeNode(rectShape(1, 1, 8, 8), rotate(0.3)),
    shapeNode(pathShape([[2, 2, 10, 3, 9, 10, 3, 9]]), translate(0.5, -0.5)),
  ]);
  assertCoverageClose(renderScene(scene, 12, 12, FAST_SS), referenceCoverage(scene, 12, 12, REF_SS));
});

test("inverse mask over full canvas matches reference (frame)", () => {
  const scene = invertNode(shapeNode(rectShape(2, 2, 8, 8)));
  const fast = renderScene(scene, 12, 12, FAST_SS);
  assert.equal(fast.at(0, 0), 1);
  assert.equal(fast.at(6, 6), 0);
  assertCoverageClose(fast, referenceCoverage(scene, 12, 12, REF_SS));
});

test("inverse mask restricted to a sub-region matches reference", () => {
  const scene = invertNode(shapeNode(rectShape(2, 2, 3, 3)), {
    x: 1,
    y: 1,
    w: 6,
    h: 6,
  });
  const fast = renderScene(scene, 9, 9, FAST_SS);
  assert.equal(fast.at(8, 8), 0); // outside region: complement does not leak
  assert.equal(fast.at(1, 1), 1); // inside region, outside rect
  assert.equal(fast.at(3, 3), 0); // inside rect
  assertCoverageClose(fast, referenceCoverage(scene, 9, 9, REF_SS));
});

test("kitchen sink: union of subtract-group and inverted shape, nested transforms", () => {
  const scene = group(
    "union",
    [
      group(
        "subtract",
        [
          shapeNode(rectShape(0, 0, 6, 6)),
          shapeNode(pathShape([[3, 1, 5, 3, 3, 5, 1, 3]])),
        ],
        multiply(translate(2, 2), rotate(0.15)),
      ),
      invertNode(shapeNode(rectShape(8, 8, 3, 3)), { x: 7, y: 7, w: 5, h: 5 }),
      shapeNode(pathShape([[10, 1, 12, 4, 9, 3]]), scale(0.9, 0.9)),
    ],
    translate(0.5, 0.5),
  );
  assertCoverageClose(renderScene(scene, 14, 14, FAST_SS), referenceCoverage(scene, 14, 14, REF_SS));
});
