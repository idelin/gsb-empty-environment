import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ScrollEngine } from '../src/engine.ts';
import { loadScene, layerConservationError, transactionConservationError } from './helpers/reference.ts';

const INNER_POINT = { x: 50, y: 50 };

test('momentum tail is a linked chain of transactions with exact conservation', () => {
  const engine = new ScrollEngine(loadScene());
  const txs = engine.startInertia(INNER_POINT, { x: 0, y: 8 }, { decay: 0.5 });
  // dyadic decay: 8, 4, 2, 1, 0.5, 0.25, 0.125, 0.0625 -> exact in floats
  assert.equal(txs.length, 8);
  const origin = txs[0]!.originId;
  assert.ok(origin !== null);
  assert.ok(txs.every((tx) => tx.originId === origin && tx.kind === 'inertia'));
  const inner = engine.nodes.get('inner')!;
  const total = 8 + 4 + 2 + 1 + 0.5 + 0.25 + 0.125 + 0.0625;
  assert.equal(inner.offset.y + inner.residual.y, total, 'offset + residual reproduces the whole impulse');
  for (const [index, tx] of txs.entries()) {
    assert.equal(layerConservationError(tx), 0);
    const expectedDelta = { x: 0, y: 8 * 0.5 ** index };
    assert.equal(transactionConservationError(tx, expectedDelta), 0);
  }
});

test('inertia overflow crosses the boundary exactly once per delta', () => {
  const engine = new ScrollEngine(loadScene());
  const inner = engine.nodes.get('inner')!;
  inner.offset.y = 390; // 10px of room left, impulse total is 15.9375
  engine.startInertia(INNER_POINT, { x: 0, y: 8 }, { decay: 0.5 });
  const outer = engine.nodes.get('outer')!;
  assert.equal(inner.offset.y, 400, 'inner pinned at its boundary');
  assert.equal(outer.offset.y + outer.residual.y, 15.9375 - 10, 'remainder moved outward, once');
  const consumedTotal = inner.offset.y - 390 + outer.offset.y;
  const residualTotal = inner.residual.y + outer.residual.y;
  assert.equal(consumedTotal + residualTotal, 15.9375, 'no delta consumed twice');
});

test('tiny momentum tail still accumulates through residuals', () => {
  const engine = new ScrollEngine(loadScene());
  const inner = engine.nodes.get('inner')!;
  // 0.2 + 0.1 + 0.05 = 0.35 of sub-pixel input: nothing moves yet...
  engine.startInertia(INNER_POINT, { x: 0, y: 0.2 }, { decay: 0.5 });
  assert.equal(inner.offset.y, 0);
  assert.ok(Math.abs(inner.residual.y - 0.35) < 1e-9, 'sub-pixel momentum stored, not dropped');
  // ...until a small push tips it over the snap threshold
  engine.dispatchWheel(INNER_POINT, { x: 0, y: 0.4 });
  assert.equal(inner.offset.y, 1);
});
