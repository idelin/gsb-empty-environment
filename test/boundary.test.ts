import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ScrollEngine } from '../src/engine.ts';
import { EDGES, edgeOf } from '../src/types.ts';
import type { Axis } from '../src/types.ts';
import { loadScene, layerConservationError, transactionConservationError } from './helpers/reference.ts';

const INNER_POINT = { x: 50, y: 50 }; // inside "inner" (rect 40,40,240,160)

test('inner hits bottom, remainder propagates to outer', () => {
  const engine = new ScrollEngine(loadScene());
  const tx = engine.dispatchWheel(INNER_POINT, { x: 0, y: 500 });
  const inner = engine.nodes.get('inner')!;
  const outer = engine.nodes.get('outer')!;
  assert.equal(inner.offset.y, 400); // inner range exhausted: 560 - 160
  assert.equal(outer.offset.y, 100); // remainder handed outward
  assert.deepEqual(tx.discarded, { x: 0, y: 0 });
  assert.equal(tx.layers[0]!.nodeId, 'inner');
  assert.equal(tx.layers[1]!.nodeId, 'outer');
  assert.equal(layerConservationError(tx), 0);
  assert.equal(transactionConservationError(tx, { x: 0, y: 500 }), 0);
});

test('boundary locking is enumerated for every edge', () => {
  for (const edge of EDGES) {
    const engine = new ScrollEngine(loadScene());
    const inner = engine.nodes.get('inner')!;
    const axis: Axis = edge === 'top' || edge === 'bottom' ? 'y' : 'x';
    const dir = edge === 'bottom' || edge === 'right' ? 1 : -1;
    if (dir > 0) inner.offset[axis] = inner.maxScroll(axis); // sit on the edge
    const delta = { x: 0, y: 0 };
    delta[axis] = 30 * dir;
    const tx = engine.dispatchWheel(INNER_POINT, delta);
    const innerLayer = tx.layers[0]!;
    const ledger = innerLayer[axis];
    assert.equal(ledger.consumed, 0, `${edge}: locked edge consumes nothing`);
    assert.equal(ledger.passed, 30 * dir, `${edge}: full amount passed outward`);
    assert.equal(inner.offset[axis], dir > 0 ? inner.maxScroll(axis) : 0);
    assert.equal(layerConservationError(tx), 0);
  }
});

test('edge push respects the direction name mapping', () => {
  assert.equal(edgeOf('y', 1), 'bottom');
  assert.equal(edgeOf('y', -1), 'top');
  assert.equal(edgeOf('x', 1), 'right');
  assert.equal(edgeOf('x', -1), 'left');
});

test('delta that cannot be consumed anywhere is discarded at the root', () => {
  const engine = new ScrollEngine(loadScene());
  // outer has no horizontal range (content 400 wide, viewport 400)
  const tx = engine.dispatchWheel(INNER_POINT, { x: 30, y: 0 });
  assert.equal(engine.nodes.get('inner')!.offset.x, 30); // inner takes it
  const tx2 = engine.dispatchWheel(INNER_POINT, { x: 1000, y: 0 });
  assert.equal(engine.nodes.get('inner')!.offset.x, 360); // inner caps at 600-240
  assert.equal(tx2.discarded.x, 1000 - 330); // remainder drops off the root
  assert.equal(transactionConservationError(tx2, { x: 1000, y: 0 }), 0);
});

test('wheel outside a child only scrolls the container under the point', () => {
  const engine = new ScrollEngine(loadScene());
  engine.dispatchWheel({ x: 10, y: 10 }, { x: 0, y: 40 }); // outer, outside inner
  assert.equal(engine.nodes.get('outer')!.offset.y, 40);
  assert.equal(engine.nodes.get('inner')!.offset.y, 0);
});
