import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ScrollEngine } from '../src/engine.ts';
import { loadScene } from './helpers/reference.ts';

function scriptedSession(): ScrollEngine {
  const engine = new ScrollEngine(loadScene());
  engine.dispatchWheel({ x: 50, y: 50 }, { x: 0, y: 120.5 }); // nested pass-through with a fraction
  engine.dispatchWheel({ x: 310, y: 50 }, { x: 0, y: 100 }); // axis transform
  engine.setCapture('outer');
  engine.dispatchWheel({ x: 50, y: 50 }, { x: 0, y: 30 }); // captured gesture
  engine.setCapture(null);
  engine.scrollTo('inner', { x: 0, y: 200 }, { source: 'panel' }); // programmatic
  engine.startInertia({ x: 50, y: 250 }, { x: 32, y: 0 }, { decay: 0.5 }); // momentum on hstrip
  engine.dispatchWheel({ x: 50, y: 250 }, { x: 0, y: 80 }); // axis-locked node passes y through
  return engine;
}

test('a recorded session replays to identical state and identical ledgers', () => {
  const engine = scriptedSession();
  const replayed = ScrollEngine.replay(loadScene(), engine.log);

  for (const id of ['outer', 'inner', 'hstrip', 'rotbox']) {
    assert.deepEqual(replayed.nodes.get(id)!.offset, engine.nodes.get(id)!.offset, `${id} offset`);
    assert.deepEqual(replayed.nodes.get(id)!.residual, engine.nodes.get(id)!.residual, `${id} residual`);
  }
  assert.equal(
    JSON.stringify(replayed.log),
    JSON.stringify(engine.log),
    'replayed transaction log is byte-identical',
  );
});

test('transactions are immutable records of received/consumed/passed/discarded', () => {
  const engine = scriptedSession();
  const txs = engine.log.filter((e) => e.type === 'transaction');
  assert.ok(txs.length >= 6);
  for (const entry of txs) {
    if (entry.type !== 'transaction') continue;
    const tx = entry.transaction;
    for (const layer of tx.layers) {
      for (const axis of [layer.x, layer.y]) {
        const lhs = axis.received + axis.residualBefore;
        const rhs = axis.consumed + axis.passed + axis.residualAfter;
        assert.ok(Math.abs(lhs - rhs) < 1e-12, `conservation in tx ${tx.id} at ${layer.nodeId}`);
      }
    }
  }
});
