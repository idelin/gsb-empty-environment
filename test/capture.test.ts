import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ScrollEngine } from '../src/engine.ts';
import { loadScene, transactionConservationError } from './helpers/reference.ts';

const INNER_POINT = { x: 50, y: 50 };

test('capture retargets the gesture chain to the capture container', () => {
  const engine = new ScrollEngine(loadScene());
  const inner = engine.nodes.get('inner')!;
  const outer = engine.nodes.get('outer')!;

  const tx1 = engine.dispatchWheel(INNER_POINT, { x: 0, y: 50 });
  assert.equal(inner.offset.y, 50);
  assert.equal(outer.offset.y, 0);

  engine.setCapture('outer'); // mid-gesture capture change
  const tx2 = engine.dispatchWheel(INNER_POINT, { x: 0, y: 50 });
  assert.equal(tx2.layers.length, 1, 'chain is just the capture container');
  assert.equal(tx2.layers[0]!.nodeId, 'outer');
  assert.equal(inner.offset.y, 50, 'inner untouched while captured');
  assert.equal(outer.offset.y, 50);

  engine.setCapture(null);
  const tx3 = engine.dispatchWheel(INNER_POINT, { x: 0, y: 50 });
  assert.equal(tx3.layers[0]!.nodeId, 'inner');

  // no delta consumed twice across the capture change
  const total = inner.offset.y + inner.residual.y + (outer.offset.y + outer.residual.y);
  assert.equal(total, 150);
  for (const tx of [tx1, tx2, tx3]) {
    assert.equal(transactionConservationError(tx, { x: 0, y: 50 }), 0);
  }
});

test('capture changes are recorded in the log and replayed', () => {
  const engine = new ScrollEngine(loadScene());
  engine.dispatchWheel(INNER_POINT, { x: 0, y: 50 });
  engine.setCapture('outer');
  engine.dispatchWheel(INNER_POINT, { x: 0, y: 50 });
  const replayed = ScrollEngine.replay(loadScene(), engine.log);
  assert.equal(replayed.nodes.get('inner')!.offset.y, 50);
  assert.equal(replayed.nodes.get('outer')!.offset.y, 50);
});
