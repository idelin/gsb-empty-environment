import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ScrollEngine } from '../src/engine.ts';
import { loadScene, layerConservationError, transactionConservationError } from './helpers/reference.ts';

const HSTRIP_POINT = { x: 50, y: 250 }; // inside "hstrip" (axes: "x")
const ROTBOX_POINT = { x: 310, y: 50 }; // inside "rotbox" (transform: "swap")

test('horizontal-only strip consumes x without touching y', () => {
  const engine = new ScrollEngine(loadScene());
  const tx = engine.dispatchWheel(HSTRIP_POINT, { x: 120, y: 0 });
  const hstrip = engine.nodes.get('hstrip')!;
  assert.equal(hstrip.offset.x, 120);
  assert.equal(hstrip.offset.y, 0);
  assert.equal(engine.nodes.get('outer')!.offset.y, 0);
  assert.equal(transactionConservationError(tx, { x: 120, y: 0 }), 0);
});

test('horizontal strip never swallows vertical delta; it flows to the parent', () => {
  const engine = new ScrollEngine(loadScene());
  const tx = engine.dispatchWheel(HSTRIP_POINT, { x: 0, y: 80 });
  const hstrip = engine.nodes.get('hstrip')!;
  assert.equal(hstrip.offset.y, 0, 'axis-locked node must not consume y');
  assert.equal(engine.nodes.get('outer')!.offset.y, 80, 'vertical remainder reaches outer');
  const stripLayer = tx.layers[0]!;
  assert.equal(stripLayer.y.consumed, 0);
  assert.equal(stripLayer.y.passed, 80);
  assert.equal(layerConservationError(tx), 0);
});

test('axis transform swaps the delta exactly once and consumes it once', () => {
  const engine = new ScrollEngine(loadScene());
  const tx = engine.dispatchWheel(ROTBOX_POINT, { x: 0, y: 100 });
  const rotbox = engine.nodes.get('rotbox')!;
  assert.equal(rotbox.offset.x, 100, 'vertical wheel drives horizontal scroll after swap');
  assert.equal(rotbox.offset.y, 0);
  const layer = tx.layers[0]!;
  assert.equal(layer.transformApplied, 'swap');
  assert.equal(layer.x.received, 100);
  assert.equal(layer.y.received, 0);
  const totalConsumed = tx.layers.reduce((sum, l) => sum + Math.abs(l.x.consumed) + Math.abs(l.y.consumed), 0);
  assert.equal(totalConsumed, 100, 'no double consumption across the transform');
  assert.equal(transactionConservationError(tx, { x: 0, y: 100 }), 0);
});

test('transformed remainder keeps its new axis when propagating outward', () => {
  const engine = new ScrollEngine(loadScene());
  // rotbox x range is 500 - 80 = 420; 500 of dy becomes 500 of dx after swap
  const tx = engine.dispatchWheel(ROTBOX_POINT, { x: 0, y: 500 });
  const rotbox = engine.nodes.get('rotbox')!;
  assert.equal(rotbox.offset.x, 420);
  // remaining 80 arrives at outer as x; outer has no x range, so it is discarded
  assert.equal(tx.discarded.x, 80);
  assert.equal(tx.discarded.y, 0);
  assert.equal(transactionConservationError(tx, { x: 0, y: 500 }), 0);
});
