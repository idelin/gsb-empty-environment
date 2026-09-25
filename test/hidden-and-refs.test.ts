import assert from 'node:assert/strict';
import { test } from 'node:test';
import { AccNameEngine, type ChangeNotification } from '../src/engine.ts';
import { metrics, node } from './fixtures.ts';

test('hidden node explicitly referenced by aria-labelledby contributes text', () => {
  const root = node({
    id: 'root',
    children: [
      node({ id: 'h', role: 'button', attrs: { 'aria-labelledby': 'hid1' } }),
      node({ id: 'hid1', hidden: true, text: 'HiddenRef' }),
    ],
  });
  const engine = new AccNameEngine(root, metrics);
  assert.equal(engine.getName('h'), 'HiddenRef');
});

test('hidden content is skipped during normal content traversal', () => {
  const root = node({
    id: 'root',
    children: [
      node({
        id: 'btn',
        role: 'button',
        children: [
          node({ id: 'no', hidden: true, text: 'No' }),
          node({ id: 'yes', text: 'Yes' }),
        ],
      }),
    ],
  });
  const engine = new AccNameEngine(root, metrics);
  assert.equal(engine.getName('btn'), 'Yes');
});

test('editing a shared label notifies every referencing node; unrelated text does not', () => {
  const root = node({
    id: 'root',
    children: [
      node({ id: 'n1', role: 'button', attrs: { 'aria-labelledby': 'L' } }),
      node({ id: 'n2', role: 'button', attrs: { 'aria-labelledby': 'L' } }),
      node({ id: 'L', text: 'Old' }),
      node({ id: 'other', text: 'Unrelated' }),
    ],
  });
  const engine = new AccNameEngine(root, metrics);
  const notifications: ChangeNotification[] = [];
  engine.onChange((n) => notifications.push(n));

  assert.equal(engine.getName('n1'), 'Old');
  assert.equal(engine.getName('n2'), 'Old');

  engine.setText('L', 'New');
  assert.equal(engine.getName('n1'), 'New');
  assert.equal(engine.getName('n2'), 'New');
  assert.equal(notifications.length, 2);
  assert.deepEqual(
    notifications.map((n) => [n.nodeId, n.oldName, n.newName]).sort(),
    [
      ['n1', 'Old', 'New'],
      ['n2', 'Old', 'New'],
    ],
  );

  const before = notifications.length;
  engine.setText('other', 'Changed');
  assert.equal(notifications.length, before);
  assert.equal(engine.getName('n1'), 'New');
  assert.equal(engine.getName('n2'), 'New');
});

test('zero-width glyphs from fixture metrics are treated as not rendered', () => {
  const zeroWidthMetrics = { charWidth: 1, overrides: { zw: 0 } };
  const root = node({
    id: 'root',
    children: [
      node({
        id: 'btn',
        role: 'button',
        children: [node({ id: 'zw', text: 'Ghost' }), node({ id: 'v', text: 'Solid' })],
      }),
    ],
  });
  const engine = new AccNameEngine(root, zeroWidthMetrics);
  assert.equal(engine.getName('btn'), 'Solid');
});

test('dependencies of a computed name are recorded', () => {
  const root = node({
    id: 'root',
    children: [
      node({ id: 'n1', role: 'button', attrs: { 'aria-labelledby': 'L' } }),
      node({ id: 'L', text: 'Label' }),
    ],
  });
  const engine = new AccNameEngine(root, metrics);
  engine.getName('n1');
  const deps = engine.getDependencies('n1');
  assert.ok(deps.includes('n1:attr:aria-labelledby'));
  assert.ok(deps.includes('@id:L'));
  assert.ok(deps.includes('L:text'));
  assert.ok(deps.includes('L:hidden'));
});
