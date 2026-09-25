import assert from 'node:assert/strict';
import { test } from 'node:test';
import { AccNameEngine } from '../src/engine.ts';
import { metrics, node } from './fixtures.ts';

test('aria-labelledby chain across multiple nodes terminates on cycle', () => {
  // a -> [b, c]; b -> d; d -> b (cycle back). d falls back to its own text.
  const root = node({
    id: 'root',
    children: [
      node({ id: 'a', role: 'button', attrs: { 'aria-labelledby': 'b c' } }),
      node({ id: 'b', attrs: { 'aria-labelledby': 'd' } }),
      node({ id: 'c', text: 'C' }),
      node({ id: 'd', attrs: { 'aria-labelledby': 'b' }, text: 'D' }),
    ],
  });
  const engine = new AccNameEngine(root, metrics);
  assert.equal(engine.getName('a'), 'D C');
  assert.equal(engine.getName('b'), 'D');
  assert.equal(engine.getName('d'), 'D');
});

test('self-referencing aria-labelledby terminates and falls through to content', () => {
  const root = node({
    id: 'root',
    children: [
      node({
        id: 's',
        role: 'button',
        attrs: { 'aria-labelledby': 's' },
        text: 'SelfText',
      }),
    ],
  });
  const engine = new AccNameEngine(root, metrics);
  assert.equal(engine.getName('s'), 'SelfText');
});

test('mutual cycle between two referenced nodes terminates', () => {
  // x -> y, y -> x: both labelledby chains yield nothing, so each falls back
  // to its own content.
  const root = node({
    id: 'root',
    children: [
      node({ id: 'x', attrs: { 'aria-labelledby': 'y' }, text: 'XText' }),
      node({ id: 'y', attrs: { 'aria-labelledby': 'x' }, text: 'YText' }),
    ],
  });
  const engine = new AccNameEngine(root, metrics);
  assert.equal(engine.getName('x'), 'YText');
  assert.equal(engine.getName('y'), 'XText');
});

test('aria-describedby chain with cycle terminates', () => {
  const root = node({
    id: 'root',
    children: [
      node({ id: 'n1', role: 'textbox', attrs: { 'aria-describedby': 'd1' } }),
      node({ id: 'd1', attrs: { 'aria-labelledby': 'd2' } }),
      node({ id: 'd2', attrs: { 'aria-labelledby': 'd1' }, text: 'Deep' }),
    ],
  });
  const engine = new AccNameEngine(root, metrics);
  assert.equal(engine.getDescription('n1'), 'Deep');
});
