import { test } from 'node:test';
import assert from 'node:assert/strict';
import { computeLayout, dropLocationAt, hitTestNode, pointForLocation } from '../src/layout.ts';
import { DragMachine } from '../src/stateMachine.ts';
import { makeTree, METRICS } from '../src/fixtures.ts';
import type { Location } from '../src/types.ts';

test('layout derives purely from fixture metrics', () => {
  const rows = computeLayout(makeTree(), METRICS);
  assert.equal(rows.length, 11);
  assert.deepEqual(rows[0], { id: 'fruits', depth: 0, x: 0, y: 0, width: 6 * 8, height: 20 });
  assert.deepEqual(rows[1], { id: 'apple', depth: 1, x: 16, y: 20, width: 16 + 5 * 8, height: 20 });
  assert.deepEqual(rows[3], { id: 'plantain', depth: 2, x: 32, y: 60, width: 32 + 8 * 8, height: 20 });
});

test('hitTestNode finds rows by point', () => {
  const rows = computeLayout(makeTree(), METRICS);
  assert.equal(hitTestNode(rows, 17, 25), 'apple');
  assert.equal(hitTestNode(rows, 33, 65), 'plantain');
  assert.equal(hitTestNode(rows, 5, 25), null); // left of the indented row
  assert.equal(hitTestNode(rows, 17, 500), null);
});

test('dropLocationAt maps points to parents and indices', () => {
  const t = makeTree();
  // on "carrot" (row 7) at depth 1 -> first child slot of veggies
  assert.deepEqual(dropLocationAt(t, METRICS, 16, 7 * 20 + 1), { parentId: 'veggies', index: 0, depth: 1 });
  // on "veggies" (row 6) at depth 1 -> end of fruits (insertion is before the row)
  assert.deepEqual(dropLocationAt(t, METRICS, 16, 6 * 20 + 1), { parentId: 'fruits', index: 3, depth: 1 });
  // top of the list at depth 0 -> root slot 0
  assert.deepEqual(dropLocationAt(t, METRICS, 0, 1), { parentId: 'root', index: 0, depth: 0 });
  // past the end at depth 0 -> appended to root
  assert.deepEqual(dropLocationAt(t, METRICS, 0, 9999), { parentId: 'root', index: 3, depth: 0 });
});

test('pointForLocation is the inverse of dropLocationAt (through the machine)', () => {
  const cases: Array<{ dragged: string; loc: Location }> = [
    { dragged: '__none__', loc: { parentId: 'root', index: 0 } },
    { dragged: '__none__', loc: { parentId: 'root', index: 3 } },
    { dragged: '__none__', loc: { parentId: 'fruits', index: 3 } },
    { dragged: '__none__', loc: { parentId: 'banana', index: 2 } },
    { dragged: '__none__', loc: { parentId: 'veggies', index: 0 } },
    { dragged: 'apple', loc: { parentId: 'fruits', index: 0 } }, // same-parent, before origin
    { dragged: 'apple', loc: { parentId: 'fruits', index: 2 } }, // same-parent, after origin
    { dragged: 'apple', loc: { parentId: 'veggies', index: 1 } }, // cross-parent
    { dragged: 'banana', loc: { parentId: 'root', index: 1 } },   // subtree dragged
  ];
  for (const { dragged, loc } of cases) {
    const tree = makeTree();
    const machine = new DragMachine(tree);
    if (dragged !== '__none__') assert.ok(machine.start(dragged));
    const p = pointForLocation(tree, METRICS, loc, dragged);
    const hit = dropLocationAt(tree, METRICS, p.x, p.y);
    assert.ok(hit, `no drop location for ${JSON.stringify(loc)}`);
    if (dragged === '__none__') {
      assert.deepEqual({ parentId: hit.parentId, index: hit.index }, loc);
    } else {
      assert.ok(machine.setTarget({ parentId: hit.parentId, index: hit.index }));
      assert.deepEqual(machine.currentTarget, loc, `round trip failed for ${JSON.stringify(loc)}`);
    }
  }
});
