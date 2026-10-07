import test from 'node:test';
import assert from 'node:assert/strict';
import {
  apply,
  hitTest,
  multiply,
  node,
  rotation,
  scaling,
  translation,
  type Mat,
  type Point,
} from '../src/index.ts';

/** Forward-transform a local point out through the ancestor chain (root first). */
function forwardPoint(chain: Mat[], local: Point): Point {
  return chain.reduceRight((p, m) => apply(m, p), local);
}

test('rotation: forward-generated points hit, outside points miss', () => {
  const target = node({ matrix: rotation(Math.PI / 2), rect: { x: 0, y: 0, width: 10, height: 4 } });
  const root = node({ children: [target] });
  const chain = [rotation(Math.PI / 2)];

  for (const local of [
    { x: 5, y: 2 },
    { x: 0, y: 0 }, // boundary corner, inclusive
    { x: 10, y: 4 }, // boundary corner, inclusive
    { x: 10, y: 0 },
    { x: 0, y: 4 },
  ]) {
    const hit = hitTest(root, forwardPoint(chain, local));
    assert.equal(hit?.node, target, `local ${JSON.stringify(local)} should hit`);
    assert.ok(Math.abs(hit!.localPoint.x - local.x) < 1e-9);
    assert.ok(Math.abs(hit!.localPoint.y - local.y) < 1e-9);
  }

  for (const local of [
    { x: -0.5, y: 2 },
    { x: 10.5, y: 2 },
    { x: 5, y: -0.5 },
    { x: 5, y: 4.5 },
  ]) {
    assert.equal(hitTest(root, forwardPoint(chain, local)), null, `local ${JSON.stringify(local)} should miss`);
  }
});

test('scaling: forward-generated points respect scaled geometry', () => {
  const m = scaling(2, 3);
  const target = node({ matrix: m, rect: { x: 0, y: 0, width: 10, height: 10 } });
  const root = node({ children: [target] });

  assert.equal(hitTest(root, forwardPoint([m], { x: 5, y: 5 }))?.node, target);
  assert.equal(hitTest(root, forwardPoint([m], { x: 10, y: 10 }))?.node, target); // boundary
  assert.equal(hitTest(root, forwardPoint([m], { x: 10.1, y: 5 })), null);
  assert.equal(hitTest(root, forwardPoint([m], { x: 5, y: 10.1 })), null);
});

test('nested transforms: translate+rotate parent, scaled child', () => {
  const parentM = multiply(translation(100, 50), rotation(0.3));
  const childM = scaling(2, 2);
  const child = node({ matrix: childM, rect: { x: -5, y: -5, width: 10, height: 10 } });
  const parent = node({ matrix: parentM, children: [child] });
  const root = node({ children: [parent] });
  const chain = [parentM, childM];

  for (const local of [
    { x: 0, y: 0 },
    { x: -5, y: -5 },
    { x: 5, y: 5 },
    { x: 3.25, y: -4.75 },
  ]) {
    assert.equal(hitTest(root, forwardPoint(chain, local))?.node, child);
  }
  for (const local of [
    { x: 5.5, y: 0 },
    { x: 0, y: -5.5 },
    { x: -6, y: -6 },
  ]) {
    assert.equal(hitTest(root, forwardPoint(chain, local)), null);
  }
});

test('singular matrix nodes and their subtrees are not hittable', () => {
  const grandchild = node({ rect: { x: 0, y: 0, width: 10, height: 10 } });
  const singular = node({ matrix: scaling(0, 1), rect: { x: 0, y: 0, width: 10, height: 10 }, children: [grandchild] });
  const root = node({ children: [singular] });

  assert.equal(hitTest(root, { x: 5, y: 5 }), null);
  assert.equal(hitTest(root, { x: 0, y: 0 }), null);
});

test('topmost-first order: later siblings and descendants win', () => {
  const below = node({ rect: { x: 0, y: 0, width: 10, height: 10 } });
  const above = node({ rect: { x: 5, y: 5, width: 10, height: 10 } });
  const child = node({ rect: { x: 2, y: 2, width: 2, height: 2 } });
  const parent = node({ rect: { x: 0, y: 0, width: 20, height: 20 }, children: [child] });
  const root = node({ children: [parent, below, above] });

  assert.equal(hitTest(root, { x: 7, y: 7 })?.node, above); // in parent/below/above: above wins
  assert.equal(hitTest(root, { x: 1, y: 1 })?.node, below); // in parent/below: below wins
  assert.equal(hitTest(root, { x: 3, y: 3 })?.node, below); // sibling below outranks parent's child
  assert.equal(hitTest(root, { x: 17, y: 17 })?.node, parent); // only parent covers
});

test('descendants render above their own ancestors', () => {
  const child = node({ rect: { x: 2, y: 2, width: 2, height: 2 } });
  const parent = node({ rect: { x: 0, y: 0, width: 20, height: 20 }, children: [child] });
  const root = node({ children: [parent] });

  assert.equal(hitTest(root, { x: 3, y: 3 })?.node, child);
  assert.equal(hitTest(root, { x: 10, y: 10 })?.node, parent);
});

test('nested clipping: ancestor rect clip prunes subtree', () => {
  const child = node({ rect: { x: -5, y: -5, width: 20, height: 20 } });
  const parent = node({
    clip: { kind: 'rect', rect: { x: 0, y: 0, width: 10, height: 10 } },
    children: [child],
  });
  const root = node({ children: [parent] });

  assert.equal(hitTest(root, { x: 5, y: 5 })?.node, child); // inside clip
  assert.equal(hitTest(root, { x: 0, y: 0 })?.node, child); // clip boundary inclusive
  assert.equal(hitTest(root, { x: 12, y: 5 }), null); // inside child rect, outside clip
  assert.equal(hitTest(root, { x: -3, y: 5 }), null);
});

test('circle and rect clips intersect along the ancestor path', () => {
  const leaf = node({ rect: { x: -20, y: -20, width: 40, height: 40 } });
  const mid = node({
    clip: { kind: 'rect', rect: { x: -8, y: -8, width: 6, height: 16 } }, // x in [-8,-2]
    children: [leaf],
  });
  const top = node({
    clip: { kind: 'circle', circle: { cx: 0, cy: 0, r: 10 } },
    children: [mid],
  });
  const root = node({ children: [top] });

  // inside circle AND inside rect clip
  assert.equal(hitTest(root, { x: -5, y: 0 })?.node, leaf);
  assert.equal(hitTest(root, { x: -5, y: 8 })?.node, leaf); // dist ~9.43 <= 10, y == 8 boundary
  // inside circle, outside rect clip
  assert.equal(hitTest(root, { x: 5, y: 0 }), null);
  // inside rect clip, outside circle
  assert.equal(hitTest(root, { x: -8, y: 8 }), null); // dist ~11.3 > 10
  // outside both
  assert.equal(hitTest(root, { x: 9, y: 9 }), null);
});

test('clip applies under rotation: clip is evaluated in owner-local space', () => {
  const m = rotation(Math.PI / 4);
  const leaf = node({ rect: { x: 0, y: 0, width: 10, height: 10 } });
  const spinner = node({ matrix: m, clip: { kind: 'circle', circle: { cx: 5, cy: 5, r: 5 } }, children: [leaf] });
  const root = node({ children: [spinner] });

  // local (5,5) is the clip center -> hit; local (9.9, 9.9) is in the rect but outside the circle clip
  assert.equal(hitTest(root, apply(m, { x: 5, y: 5 }))?.node, leaf);
  assert.equal(hitTest(root, apply(m, { x: 9.9, y: 9.9 })), null);
});
