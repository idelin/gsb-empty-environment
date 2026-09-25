import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ScrollEngine } from '../src/engine.ts';
import type { ScrollTransaction } from '../src/engine.ts';
import { loadScene, layerConservationError } from './helpers/reference.ts';

test('programmatic scroll is clamped, snapped and recorded as a transaction', () => {
  const engine = new ScrollEngine(loadScene());
  const tx = engine.scrollTo('inner', { x: 0, y: 99999 }, { source: 'panel' })!;
  const inner = engine.nodes.get('inner')!;
  assert.equal(inner.offset.y, 400);
  assert.equal(tx.kind, 'programmatic');
  assert.equal(tx.layers[0]!.y.received, 99999);
  assert.equal(tx.layers[0]!.y.consumed, 400);
  assert.equal(tx.discarded.y, 99999 - 400, 'clamped overflow is accounted, not dropped silently');
  assert.equal(layerConservationError(tx), 0);
});

test('listener echoing its own source token is blocked as feedback', () => {
  const engine = new ScrollEngine(loadScene());
  engine.onScroll((event) => {
    // naive mirror: re-issues whatever it sees, tagged with the same source
    engine.scrollTo('inner', { x: 0, y: event.offset.y + 10 }, { source: event.source });
  });
  const tx = engine.scrollTo('inner', { x: 0, y: 50 }, { source: 'panel' });
  assert.ok(tx !== null);
  assert.equal(engine.blockedFeedback, 1, 'echo with the same token is rejected');
  assert.equal(engine.nodes.get('inner')!.offset.y, 50, 'no feedback loop ran');
  assert.equal(engine.log.filter((e) => e.type === 'transaction').length, 1);
});

test('reentrant callback with a different token applies exactly once', () => {
  const engine = new ScrollEngine(loadScene());
  engine.onScroll((event) => {
    if (event.source === 'panel') return; // panel ignores its own events
    engine.scrollTo('inner', { x: 0, y: event.offset.y + 10 }, { source: 'panel' });
  });
  engine.scrollTo('inner', { x: 0, y: 50 }, { source: 'user-script' });
  assert.equal(engine.nodes.get('inner')!.offset.y, 60, 'one reentrant scroll applied, then it stopped');
  const txs = engine.log.filter((e) => e.type === 'transaction');
  assert.equal(txs.length, 2);
});

test('reentrant callback storms are capped by the emission depth guard', () => {
  const engine = new ScrollEngine(loadScene());
  let counter = 0;
  engine.onScroll((event) => {
    counter += 1;
    engine.scrollTo('inner', { x: 0, y: event.offset.y + 1 }, { source: `loop-${counter}` });
  });
  engine.scrollTo('inner', { x: 0, y: 1 }, { source: 'seed' });
  assert.ok(engine.blockedDepth >= 1, 'depth guard fired');
  const txs = engine.log.filter((e) => e.type === 'transaction');
  assert.ok(txs.length <= 9, `bounded recursion, got ${txs.length} transactions`);
});

test('wheel-originated events carry a token so mirrors do not fight the user', () => {
  const engine = new ScrollEngine(loadScene());
  const seen: string[] = [];
  engine.onScroll((event) => {
    seen.push(event.source);
    if (event.source === 'mirror') return;
    engine.scrollTo('inner', { x: 0, y: event.offset.y + 20 }, { source: 'mirror' });
  });
  engine.dispatchWheel({ x: 50, y: 50 }, { x: 0, y: 30 });
  const inner = engine.nodes.get('inner')!;
  assert.equal(inner.offset.y, 50, 'mirror reacted once, then its own token stopped the loop');
  assert.deepEqual(seen, ['user', 'mirror']);
  assert.equal(engine.blockedFeedback, 0);
});

test('programmatic transactions appear in the replayable log', () => {
  const engine = new ScrollEngine(loadScene());
  engine.scrollTo('inner', { x: 12, y: 34 }, { source: 'panel' });
  const replayed = ScrollEngine.replay(loadScene(), engine.log);
  assert.equal(replayed.nodes.get('inner')!.offset.y, 34);
  assert.equal(replayed.nodes.get('inner')!.offset.x, 12);
});

test('every programmatic transaction satisfies layer conservation', () => {
  const engine = new ScrollEngine(loadScene());
  const targets = [
    { x: 0, y: 100 },
    { x: 0, y: -50 },
    { x: 0, y: 450 },
    { x: 0, y: 0 },
  ];
  for (const target of targets) {
    const tx = engine.scrollTo('inner', target, { source: 'panel' }) as ScrollTransaction;
    assert.equal(layerConservationError(tx), 0);
  }
});
