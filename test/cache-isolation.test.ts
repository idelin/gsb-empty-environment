import assert from 'node:assert/strict';
import { test } from 'node:test';
import { AccNameEngine, type ChangeNotification } from '../src/engine.ts';
import { metrics, node } from './fixtures.ts';

test('broken references stay isolated per engine instance', () => {
  const tree1 = node({
    id: 'root',
    children: [
      node({
        id: 'c1',
        role: 'button',
        attrs: { 'aria-labelledby': 'missing', 'aria-label': 'Fallback' },
      }),
    ],
  });
  const tree2 = node({
    id: 'root',
    children: [
      node({ id: 'c2', role: 'button', attrs: { 'aria-labelledby': 'missing' } }),
      node({ id: 'missing', text: 'Real' }),
    ],
  });
  const engine1 = new AccNameEngine(tree1, metrics);
  const engine2 = new AccNameEngine(tree2, metrics);
  const notifications1: ChangeNotification[] = [];
  const notifications2: ChangeNotification[] = [];
  engine1.onChange((n) => notifications1.push(n));
  engine2.onChange((n) => notifications2.push(n));

  assert.equal(engine1.getName('c1'), 'Fallback');
  assert.equal(engine2.getName('c2'), 'Real');

  engine2.setText('missing', 'Changed');
  assert.equal(engine2.getName('c2'), 'Changed');
  assert.equal(notifications2.length, 1);

  // The broken reference in tree1 is untouched by tree2's mutation.
  assert.equal(engine1.getName('c1'), 'Fallback');
  assert.equal(notifications1.length, 0);
});

test('healing a broken reference recomputes only that instance', () => {
  const tree1 = node({
    id: 'root',
    children: [
      node({
        id: 'c1',
        role: 'button',
        attrs: { 'aria-labelledby': 'missing', 'aria-label': 'Fallback' },
      }),
    ],
  });
  const engine = new AccNameEngine(tree1, metrics);
  const notifications: ChangeNotification[] = [];
  engine.onChange((n) => notifications.push(n));

  assert.equal(engine.getName('c1'), 'Fallback');
  const versionBefore = engine.treeVersion;

  engine.appendChild('root', node({ id: 'missing', text: 'Healed' }));
  assert.ok(engine.treeVersion > versionBefore);
  assert.equal(engine.getName('c1'), 'Healed');
  assert.equal(notifications.length, 1);
  assert.equal(notifications[0]?.newName, 'Healed');
});

test('cache entries are revalidated against tree version history', () => {
  const root = node({
    id: 'root',
    children: [
      node({ id: 'a', role: 'button', text: 'Alpha' }),
      node({ id: 'b', role: 'button', text: 'Beta' }),
    ],
  });
  const engine = new AccNameEngine(root, metrics);
  const notifications: ChangeNotification[] = [];
  engine.onChange((n) => notifications.push(n));

  assert.equal(engine.getName('a'), 'Alpha');
  assert.equal(engine.getName('b'), 'Beta');

  // Mutating b must not invalidate or notify a.
  engine.setText('b', 'Beta2');
  assert.equal(engine.getName('a'), 'Alpha');
  assert.equal(engine.getName('b'), 'Beta2');
  assert.deepEqual(
    notifications.map((n) => n.nodeId),
    ['b'],
  );
});
