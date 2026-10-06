import { test } from "node:test";
import assert from "node:assert/strict";
import {
  multiply,
  rotate,
  scale,
  translate,
  type Mat2D,
} from "../src/matrix.ts";
import { circleClip, rectClip, type ClipShape } from "../src/geometry.ts";
import { SceneNode } from "../src/node.ts";
import { Scene } from "../src/scene.ts";
import { snapshotView } from "../src/view.ts";
import { collectHits, hitTest } from "../src/hit.ts";
import { SpatialIndex } from "../src/index.ts";

function mulberry32(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (state + 0x6d2b79f5) >>> 0;
    let t = state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

test("ancestor transform updates invalidate the whole subtree cache", () => {
  const root = new SceneNode({ name: "root" });
  const grand = new SceneNode({ matrix: translate(0, 0) });
  const parent = new SceneNode({ matrix: translate(5, 5) });
  const child = new SceneNode({
    rect: { x: 0, y: 0, width: 10, height: 10 },
    matrix: translate(2, 0),
  });
  root.addChild(grand);
  grand.addChild(parent);
  parent.addChild(child);
  const scene = new Scene(root);
  const index = new SpatialIndex(scene);

  assert.ok(index.candidates(8, 6).includes(child), "child near origin");
  const keyBefore = index.cacheKeyOf(child);

  grand.setMatrix(translate(100, 0));

  const keyAfter = index.cacheKeyOf(child);
  assert.notEqual(keyAfter, keyBefore, "descendant cache key invalidated");
  assert.ok(!index.candidates(8, 6).includes(child), "old spot no longer candidate");
  assert.ok(index.candidates(108, 6).includes(child), "new spot is candidate");
  assert.equal(index.hit(108, 6), child);
});

test("old snapshots keep old chains for both index and hit test", () => {
  const root = new SceneNode({ name: "root" });
  const node = new SceneNode({
    rect: { x: 0, y: 0, width: 10, height: 10 },
    matrix: translate(0, 0),
  });
  root.addChild(node);
  const scene = new Scene(root);
  const index = new SpatialIndex(scene);

  const snap = scene.snapshot();
  assert.ok(index.candidates(5, 5).includes(node), "cache populated pre-move");

  node.setMatrix(translate(100, 0));

  const oldView = snapshotView(snap);
  assert.ok(index.candidates(5, 5, oldView).includes(node), "snapshot candidates keep old bbox");
  assert.ok(!index.candidates(105, 5, oldView).includes(node));
  assert.equal(index.hit(5, 5, oldView), node, "snapshot hit follows the old chain");
  assert.ok(!index.candidates(5, 5).includes(node), "live candidates moved");
  assert.ok(index.candidates(105, 5).includes(node));
});

test("index bbox intersects circle and rect clips along the path", () => {
  const root = new SceneNode({ name: "root" });
  const grand = new SceneNode({ clip: rectClip(-20, -20, 40, 40) });
  const parent = new SceneNode({ clip: circleClip(0, 0, 10) });
  const child = new SceneNode({ rect: { x: -100, y: -100, width: 200, height: 200 } });
  root.addChild(grand);
  grand.addChild(parent);
  parent.addChild(child);
  const scene = new Scene(root);
  const index = new SpatialIndex(scene);

  // Effective bbox: [-100,100]^2 ∩ circle bbox [-10,10]^2 ∩ [-20,20]^2 = [-10,10]^2.
  assert.ok(!index.candidates(50, 0).includes(child), "outside circle bbox");
  assert.ok(!index.candidates(15, 0).includes(child), "inside rect clip bbox, outside circle bbox");
  assert.ok(index.candidates(9, 9).includes(child), "inside the intersected bbox");

  // Candidate but not an exact hit: index is conservative, real hit uses geometry.
  assert.ok(index.candidates(8, 8).includes(child));
  assert.equal(index.hit(8, 8), null, "circle clip rejects (8,8): dist > r");
  assert.equal(index.hit(5, 5), child);
});

test("rotated node: world bbox is conservative, exact hit uses local geometry", () => {
  const root = new SceneNode({ name: "root" });
  const node = new SceneNode({
    rect: { x: 0, y: 0, width: 10, height: 10 },
    matrix: multiply(translate(100, 100), rotate(Math.PI / 4)),
  });
  root.addChild(node);
  const scene = new Scene(root);
  const index = new SpatialIndex(scene);

  // World bbox is x in [92.93,107.07], y in [100,114.14]. Point (93.2,100.2)
  // sits inside the bbox but maps to local x < 0: outside the rotated rect.
  assert.ok(index.candidates(93.2, 100.2).includes(node), "bbox corner is a candidate");
  assert.equal(index.hit(93.2, 100.2), null, "exact local geometry rejects it");
  assert.equal(index.hit(100, 100), node, "rect corner hits");
});

test("index candidates never miss a direct hit (randomized cross-check)", () => {
  const rand = mulberry32(0xc0ffee);
  const root = new SceneNode({ name: "root" });
  const all: SceneNode[] = [];

  const randomMatrix = (): Mat2D => {
    let m = translate(rand() * 200 - 100, rand() * 200 - 100);
    m = multiply(m, rotate(rand() * Math.PI * 2));
    const sx = rand() < 0.1 ? 0 : 0.3 + rand() * 2.7; // some singular nodes
    const sy = rand() < 0.1 ? 0 : 0.3 + rand() * 2.7;
    return multiply(m, scale(sx, sy));
  };
  const randomClip = (): ClipShape | null => {
    const r = rand();
    if (r < 0.6) return null;
    if (r < 0.8) return rectClip(rand() * 40 - 20, rand() * 40 - 20, 10 + rand() * 40, 10 + rand() * 40);
    return circleClip(rand() * 40 - 20, rand() * 40 - 20, 5 + rand() * 25);
  };

  const parents: SceneNode[] = [root];
  for (let i = 0; i < 40; i++) {
    const node = new SceneNode({
      rect: { x: rand() * 40 - 20, y: rand() * 40 - 20, width: 5 + rand() * 35, height: 5 + rand() * 35 },
      matrix: randomMatrix(),
      clip: randomClip(),
    });
    const parent = parents[Math.floor(rand() * parents.length)];
    parent.addChild(node);
    all.push(node);
    if (node.chain().length < 4) parents.push(node);
  }

  const scene = new Scene(root);
  const index = new SpatialIndex(scene);

  for (let i = 0; i < 1000; i++) {
    const x = rand() * 300 - 150;
    const y = rand() * 300 - 150;
    const direct = collectHits(root, x, y);
    const candidates = new Set(index.candidates(x, y));
    for (const node of direct) {
      assert.ok(
        candidates.has(node),
        `missed hit ${node.name} at (${x.toFixed(2)},${y.toFixed(2)})`,
      );
    }
    assert.equal(
      index.hit(x, y),
      hitTest(root, x, y),
      `topmost mismatch at (${x.toFixed(2)},${y.toFixed(2)})`,
    );
  }
});
