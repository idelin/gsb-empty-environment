import { test } from 'node:test';
import assert from 'node:assert/strict';
import { TreeController } from '../src/controller.ts';
import type { NodeData } from '../src/types.ts';

const base: NodeData[] = [
  {
    id: 'alpha', label: 'alpha',
    children: [
      { id: 'a1', label: 'a1' },
      { id: 'a3', label: 'a3', children: [{ id: 'a3x', label: 'a3x' }] },
    ],
  },
  { id: 'gamma', label: 'gamma', children: [{ id: 'g1', label: 'g1' }] },
  { id: 'delta', label: 'delta' },
];

test('structural updates are incremental splices, never full traversals', () => {
  const c = new TreeController(base);
  const v = c.visible;
  const check = (): void => {
    assert.deepEqual([...v.ids], v.computeAll(), 'incremental list matches reference');
  };
  check();
  const before = v.recomputeCount;
  c.expand('alpha'); check();
  c.expand('a3'); check();
  c.expand('gamma'); check();
  c.collapse('a3'); check();
  c.insertChildren('gamma', [{ id: 'g2', label: 'g2' }], 0); check();
  c.moveNode('delta', 'alpha', 1); check();
  c.removeNode('a1'); check();
  c.collapse('alpha'); check();
  assert.equal(v.recomputeCount, before, 'no full recomputation without a filter');
  assert.ok(v.spliceCount > 0);
});

test('filter shows matches plus ancestors and ignores expansion', () => {
  const c = new TreeController(base);
  c.expand('alpha');
  c.setFilter('a3');
  assert.deepEqual([...c.visible.ids], ['alpha', 'a3', 'a3x']);
  c.collapse('alpha'); // expansion ignored while filtering
  assert.deepEqual([...c.visible.ids], ['alpha', 'a3', 'a3x']);
  c.setFilter('');
  assert.deepEqual([...c.visible.ids], ['alpha', 'gamma', 'delta']);
});

test('filter mode recomputes on structural change and stays consistent', () => {
  const c = new TreeController(base);
  c.setFilter('g');
  assert.deepEqual([...c.visible.ids], ['gamma', 'g1']);
  c.insertChildren('gamma', [{ id: 'g9', label: 'g9' }]);
  assert.deepEqual([...c.visible.ids], ['gamma', 'g1', 'g9']);
  assert.deepEqual([...c.visible.ids], c.visible.computeAll());
});

test('move between parents keeps the visible list consistent', () => {
  const c = new TreeController(base);
  c.expand('alpha');
  c.expand('gamma');
  c.moveNode('a3', 'gamma', 0);
  assert.deepEqual([...c.visible.ids], ['alpha', 'a1', 'gamma', 'a3', 'g1', 'delta']);
  c.expand('a3');
  assert.deepEqual([...c.visible.ids], ['alpha', 'a1', 'gamma', 'a3', 'a3x', 'g1', 'delta']);
  // Move into a collapsed branch: the node disappears from view.
  // (a3 stays expanded under gamma, so a3x remains visible.)
  c.collapse('alpha');
  c.moveNode('g1', 'alpha', 0);
  assert.deepEqual([...c.visible.ids], ['alpha', 'gamma', 'a3', 'a3x', 'delta']);
  assert.deepEqual([...c.visible.ids], c.visible.computeAll());
});
