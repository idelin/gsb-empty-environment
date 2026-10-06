import { test } from "node:test";
import assert from "node:assert/strict";
import {
  IDENTITY,
  apply,
  multiply,
  rotate,
  scale,
  translate,
  type Point,
} from "../src/matrix.ts";
import { circleClip, rectClip } from "../src/geometry.ts";
import { SceneNode } from "../src/node.ts";
import { Scene } from "../src/scene.ts";
import { snapshotView } from "../src/view.ts";
import { collectHits, hitTest } from "../src/hit.ts";

/** Forward-transform a node's local point into world space. */
function toWorld(node: SceneNode, lx: number, ly: number): Point {
  let world = IDENTITY;
  for (const current of node.chain()) world = multiply(world, current.matrix);
  return apply(world, lx, ly);
}

function bareRoot(): SceneNode {
  return new SceneNode({ name: "root" }); // zero-size rect: never hit itself
}

test("rotation: forward-generated points hit only inside the rotated rect", () => {
  const root = bareRoot();
  const node = new SceneNode({
    rect: { x: 0, y: 0, width: 10, height: 10 },
    matrix: multiply(translate(50, 50), rotate(Math.PI / 4)),
  });
  root.addChild(node);

  for (const [lx, ly] of [[5, 5], [0, 0], [10, 0], [0, 10], [10, 10], [5, 0], [0, 5]]) {
    const p = toWorld(node, lx, ly);
    assert.equal(hitTest(root, p.x, p.y), node, `inside/boundary (${lx},${ly})`);
  }
  for (const [lx, ly] of [[-0.5, 5], [10.5, 5], [5, -0.5], [5, 10.5], [-3, -3]]) {
    const p = toWorld(node, lx, ly);
    assert.equal(hitTest(root, p.x, p.y), null, `outside (${lx},${ly})`);
  }
});

test("scale: non-uniform scaling maps the hit area correctly", () => {
  const root = bareRoot();
  const node = new SceneNode({
    rect: { x: 0, y: 0, width: 10, height: 10 },
    matrix: multiply(translate(-30, 20), scale(2, 0.5)),
  });
  root.addChild(node);

  // World footprint: x in [-30, -10], y in [20, 25].
  for (const [lx, ly] of [[5, 5], [0, 0], [10, 10], [10, 5]]) {
    const p = toWorld(node, lx, ly);
    assert.equal(hitTest(root, p.x, p.y), node, `inside/boundary (${lx},${ly})`);
  }
  for (const [lx, ly] of [[-1, 5], [11, 5], [5, 11], [5, -2]]) {
    const p = toWorld(node, lx, ly);
    assert.equal(hitTest(root, p.x, p.y), null, `outside (${lx},${ly})`);
  }
  // Spot-check world coordinates directly.
  assert.equal(hitTest(root, -20, 22.5), node);
  assert.equal(hitTest(root, -9, 22.5), null);
  assert.equal(hitTest(root, -20, 26), null);
});

test("topmost reverse order: later siblings and deeper nodes win", () => {
  const root = bareRoot();
  const a = new SceneNode({ rect: { x: 0, y: 0, width: 10, height: 10 } });
  const b = new SceneNode({ rect: { x: 5, y: 5, width: 10, height: 10 } });
  root.addChild(a);
  root.addChild(b);

  assert.equal(hitTest(root, 7, 7), b, "overlap goes to the later sibling");
  assert.equal(hitTest(root, 2, 2), a);
  assert.equal(hitTest(root, 12, 12), b);
  assert.equal(hitTest(root, 15, 15), b, "boundary is inclusive");
  assert.equal(hitTest(root, 15.5, 7), null);

  root.removeChild(b);
  assert.equal(hitTest(root, 7, 7), a, "removal restores the node below");
});

test("singular matrix nodes are not hittable, nor are their children", () => {
  const root = bareRoot();
  const flat = new SceneNode({
    rect: { x: 0, y: 0, width: 10, height: 10 },
    matrix: scale(1, 0),
  });
  const child = new SceneNode({
    rect: { x: 0, y: 0, width: 10, height: 10 },
    matrix: translate(3, 3),
  });
  root.addChild(flat);
  flat.addChild(child);

  assert.equal(hitTest(root, 5, 0), null, "collapsed node itself");
  assert.equal(hitTest(root, 8, 0), null, "child of a collapsed node");
  assert.deepEqual(collectHits(root, 5, 0), []);
});

test("nested clipping: circle clip and rect clip intersect along the path", () => {
  const root = bareRoot();
  const grand = new SceneNode({
    matrix: translate(10, 10),
    clip: rectClip(0, 0, 100, 100),
  });
  const parent = new SceneNode({
    matrix: translate(60, 0),
    clip: circleClip(50, 50, 30),
  });
  const child = new SceneNode({
    rect: { x: 0, y: 0, width: 80, height: 80 },
  });
  root.addChild(grand);
  grand.addChild(parent);
  parent.addChild(child);

  // child-local (35,50): inside circle (dist 15 <= 30), inside grand rect.
  const inside = toWorld(child, 35, 50);
  assert.equal(hitTest(root, inside.x, inside.y), child);

  // child-local (50,50): inside circle but grand-space x = 110 > 100.
  const clippedByRect = toWorld(child, 50, 50);
  assert.equal(hitTest(root, clippedByRect.x, clippedByRect.y), null);

  // child-local (25,20): inside grand rect but outside the circle.
  const clippedByCircle = toWorld(child, 25, 20);
  assert.equal(hitTest(root, clippedByCircle.x, clippedByCircle.y), null);

  // Circle boundary itself is inclusive: child-local (80,50) is on the rim.
  const rim = toWorld(child, 80, 50);
  assert.equal(hitTest(root, rim.x, rim.y), null, "rim is outside grand rect here");
  const rimInside = toWorld(child, 20, 50);
  assert.equal(hitTest(root, rimInside.x, rimInside.y), child, "rim inside both clips");
});

test("ancestor clip does not clip the clipping node itself", () => {
  const root = bareRoot();
  const clipper = new SceneNode({
    rect: { x: 0, y: 0, width: 50, height: 50 },
    clip: rectClip(0, 0, 10, 10),
  });
  root.addChild(clipper);
  assert.equal(hitTest(root, 40, 40), clipper, "own rect still fully hittable");
});

test("snapshot keeps the old transform chain after updates", () => {
  const root = bareRoot();
  const node = new SceneNode({
    rect: { x: 0, y: 0, width: 10, height: 10 },
    matrix: translate(0, 0),
  });
  root.addChild(node);
  const scene = new Scene(root);

  const snap = scene.snapshot();
  node.setMatrix(translate(100, 0));

  const view = snapshotView(snap);
  assert.equal(hitTest(root, 5, 5, view), node, "old chain still hits");
  assert.equal(hitTest(root, 105, 5, view), null, "old chain misses the new spot");
  assert.equal(hitTest(root, 5, 5), null, "live view moved away");
  assert.equal(hitTest(root, 105, 5), node, "live view uses the new chain");
});
