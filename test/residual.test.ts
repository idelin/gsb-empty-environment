import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ScrollEngine, consumeAt } from '../src/engine.ts';
import { buildScene } from '../src/tree.ts';
import { Fraction } from '../src/rational.ts';
import { loadScene, mulberry32, RefNode, refConsume } from './helpers/reference.ts';

const INNER_POINT = { x: 50, y: 50 };

test('float engine matches the exact rational reference for dyadic input', () => {
  const rand = mulberry32(0x5eed);
  for (let trial = 0; trial < 200; trial += 1) {
    const max = 40 + Math.floor(rand() * 200);
    const snapDen = rand() < 0.5 ? 1 : 2;
    const scene = buildScene({
      glyphMetrics: { lineHeight: 20, advance: 10 },
      nodes: [
        {
          id: 'n',
          parent: null,
          rect: [0, 0, 100, 100],
          axes: 'both',
          snap: 1 / snapDen,
          transform: 'none',
          content: { lines: [{ chars: 10, repeat: 5 + Math.ceil((max + 100) / 20) }] },
        },
      ],
    });
    const node = scene.nodes.get('n')!;
    const ref = new RefNode(Fraction.of(node.maxScroll('y')), Fraction.of(1, snapDen));
    for (let step = 0; step < 40; step += 1) {
      const units = Math.floor(rand() * 25) - 12; // -12..12 eighths
      const delta = units / 8;
      consumeAt(node, 'y', delta);
      refConsume(ref, Fraction.of(units, 8));
      assert.equal(node.offset.y, ref.offset.toNumber(), `offset mismatch at trial ${trial} step ${step}`);
      assert.equal(node.residual.y, ref.residual.toNumber(), `residual mismatch at trial ${trial} step ${step}`);
    }
  }
});

test('small reverse increments accumulate and eventually move the offset', () => {
  const engine = new ScrollEngine(loadScene());
  const inner = engine.nodes.get('inner')!;
  for (let i = 0; i < 5; i += 1) engine.dispatchWheel(INNER_POINT, { x: 0, y: 0.4 });
  assert.equal(inner.offset.y, 2, 'five 0.4 nudges snap to exactly 2');
  assert.ok(Math.abs(inner.residual.y) < 1e-9, 'residual settles back to ~0');
  engine.dispatchWheel(INNER_POINT, { x: 0, y: -0.4 });
  assert.equal(inner.offset.y, 2, 'first reverse nudge is absorbed by the snap remainder');
  engine.dispatchWheel(INNER_POINT, { x: 0, y: -0.4 });
  assert.equal(inner.offset.y, 1, 'accumulated reverse motion moves the offset');
  for (let i = 0; i < 3; i += 1) engine.dispatchWheel(INNER_POINT, { x: 0, y: -0.4 });
  assert.equal(inner.offset.y, 0);
  assert.ok(Math.abs(inner.residual.y) < 1e-9);
});

test('dyadic micro-deltas accumulate with zero residual at exact checkpoints', () => {
  const engine = new ScrollEngine(loadScene());
  const inner = engine.nodes.get('inner')!;
  for (let i = 0; i < 8; i += 1) engine.dispatchWheel(INNER_POINT, { x: 0, y: 0.125 });
  assert.equal(inner.offset.y, 1);
  assert.equal(inner.residual.y, 0);
  for (let i = 0; i < 8; i += 1) engine.dispatchWheel(INNER_POINT, { x: 0, y: -0.125 });
  assert.equal(inner.offset.y, 0);
  assert.equal(inner.residual.y, 0);
});

test('non-dyadic input (0.1) keeps the residual instead of dropping it', () => {
  const engine = new ScrollEngine(loadScene());
  const inner = engine.nodes.get('inner')!;
  let input = 0;
  for (let i = 0; i < 10; i += 1) {
    engine.dispatchWheel(INNER_POINT, { x: 0, y: 0.1 });
    input += 0.1;
  }
  // exact reference: 10 * 1/10 = 1 pixel of travel
  assert.ok(Math.abs(inner.offset.y - 1) < 1e-9, `offset ${inner.offset.y} should be ~1`);
  // nothing lost: offset + residual reproduces the accumulated input
  assert.ok(Math.abs(inner.offset.y + inner.residual.y - input) < 1e-9);
});
