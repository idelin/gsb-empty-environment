import test from 'node:test';
import assert from 'node:assert/strict';
import {
  OutlineHistory,
  buildParentMap,
  flattenPreOrder,
  isAncestor,
  moveNodes,
  normalizeSelection,
} from '../src/index.ts';
import type { OutlineNode } from '../src/index.ts';

function node(id: string, children: OutlineNode[] = []): OutlineNode {
  return { id, children };
}

// A(A1, A2(A2a), A3, A4), B(B1), C
function fixture(): OutlineNode[] {
  return [
    node('A', [node('A1'), node('A2', [node('A2a')]), node('A3'), node('A4')]),
    node('B', [node('B1')]),
    node('C'),
  ];
}

function ids(tree: OutlineNode[]): string[] {
  return flattenPreOrder(tree).map((e) => e.id);
}

function childIds(tree: OutlineNode[], parentId: string | null): string[] {
  return flattenPreOrder(tree)
    .filter((e) => e.parentId === parentId)
    .map((e) => e.id);
}

function snapshot(tree: OutlineNode[]): string {
  return JSON.stringify(tree);
}

test('flattenPreOrder yields document order with parent links', () => {
  const flat = flattenPreOrder(fixture());
  assert.deepEqual(flat.map((e) => e.id), ['A', 'A1', 'A2', 'A2a', 'A3', 'A4', 'B', 'B1', 'C']);
  assert.deepEqual(flat.map((e) => e.parentId), [
    null, 'A', 'A', 'A2', 'A', 'A', null, 'B', null,
  ]);
  assert.equal(flat.find((e) => e.id === 'A3')!.index, 2);
});

test('isAncestor walks the parent chain', () => {
  const parents = buildParentMap(fixture());
  assert.equal(isAncestor(parents, 'A', 'A2a'), true);
  assert.equal(isAncestor(parents, 'A2', 'A2a'), true);
  assert.equal(isAncestor(parents, 'A2a', 'A'), false);
  assert.equal(isAncestor(parents, 'B', 'A1'), false);
  assert.equal(isAncestor(parents, 'A', 'A'), false);
});

test('normalizeSelection drops selected descendants of selected ancestors', () => {
  const tree = fixture();
  assert.deepEqual(normalizeSelection(tree, ['A2a', 'A', 'A1']), ['A']);
  assert.deepEqual(normalizeSelection(tree, ['A2a', 'A2']), ['A2']);
  // Result follows document order, not click order.
  assert.deepEqual(normalizeSelection(tree, ['B1', 'A3', 'A1']), ['A1', 'A3', 'B1']);
  assert.deepEqual(normalizeSelection(tree, []), []);
});

test('same-parent move of non-adjacent nodes, target after them', () => {
  const tree = fixture();
  // A children before: [A1, A2, A3, A4]; index 4 is pre-removal end.
  const res = moveNodes(tree, ['A1', 'A3'], { parentId: 'A', index: 4 });
  assert.ok(res.ok);
  assert.deepEqual(childIds(res.tree, 'A'), ['A2', 'A4', 'A1', 'A3']);
  // No node lost or duplicated.
  assert.deepEqual([...ids(res.tree)].sort(), [...ids(tree)].sort());
});

test('same-parent move with pre-removal index adjustment', () => {
  const tree = fixture();
  // Pre-removal index 3 means 'before A4'; A1 and A3 sit before it.
  const res = moveNodes(tree, ['A1', 'A3'], { parentId: 'A', index: 3 });
  assert.ok(res.ok);
  assert.deepEqual(childIds(res.tree, 'A'), ['A2', 'A1', 'A3', 'A4']);
});

test('same-parent move to the front', () => {
  const tree = fixture();
  const res = moveNodes(tree, ['A2', 'A4'], { parentId: 'A', index: 0 });
  assert.ok(res.ok);
  assert.deepEqual(childIds(res.tree, 'A'), ['A2', 'A4', 'A1', 'A3']);
  // Subtree travels with its parent.
  assert.deepEqual(childIds(res.tree, 'A2'), ['A2a']);
});

test('cross-parent move keeps source document order regardless of click order', () => {
  const tree = fixture();
  const res = moveNodes(tree, ['B1', 'A1'], { parentId: 'C', index: 0 });
  assert.ok(res.ok);
  assert.deepEqual(res.movedIds, ['A1', 'B1']);
  assert.deepEqual(childIds(res.tree, 'C'), ['A1', 'B1']);
  assert.deepEqual(childIds(res.tree, 'A'), ['A2', 'A3', 'A4']);
  assert.deepEqual(childIds(res.tree, 'B'), []);
  const parents = buildParentMap(res.tree);
  assert.equal(parents.get('A1'), 'C');
  assert.equal(parents.get('B1'), 'C');
});

test('move to root level and into a nested parent', () => {
  const tree = fixture();
  const res = moveNodes(tree, ['A2a', 'B'], { parentId: null, index: 0 });
  assert.ok(res.ok);
  assert.deepEqual(childIds(res.tree, null), ['A2a', 'B', 'A', 'C']);
  assert.deepEqual(childIds(res.tree, 'A2'), []);
  assert.deepEqual(childIds(res.tree, 'B'), ['B1']);
});

test('moving an ancestor silently absorbs its selected descendants', () => {
  const tree = fixture();
  const res = moveNodes(tree, ['A2a', 'A2', 'A4'], { parentId: 'B', index: 1 });
  assert.ok(res.ok);
  assert.deepEqual(res.movedIds, ['A2', 'A4']);
  assert.deepEqual(childIds(res.tree, 'B'), ['B1', 'A2', 'A4']);
  assert.deepEqual(childIds(res.tree, 'A2'), ['A2a']);
  assert.deepEqual(childIds(res.tree, 'A'), ['A1', 'A3']);
});

test('rejects moving a node into itself or its descendant', () => {
  const tree = fixture();
  const before = snapshot(tree);
  for (const target of [{ parentId: 'A', index: 0 }, { parentId: 'A2a', index: 0 }]) {
    const res = moveNodes(tree, ['A'], target);
    assert.ok(!res.ok);
    assert.equal(res.error, 'target-inside-selection');
    assert.equal(res.tree, tree);
  }
  assert.equal(snapshot(tree), before);
});

test('rejects a drop target that is itself selected', () => {
  const tree = fixture();
  const before = snapshot(tree);
  const res = moveNodes(tree, ['A1', 'A3'], { parentId: 'A3', index: 0 });
  assert.ok(!res.ok);
  assert.equal(res.error, 'target-inside-selection');
  assert.equal(snapshot(tree), before);
});

test('rejects unknown ids, unknown target parent and bad index', () => {
  const tree = fixture();
  const before = snapshot(tree);
  assert.equal((moveNodes(tree, ['ZZ'], { parentId: 'C', index: 0 }) as any).error, 'unknown-node');
  assert.equal((moveNodes(tree, [], { parentId: 'C', index: 0 }) as any).error, 'empty-selection');
  assert.equal(
    (moveNodes(tree, ['A1'], { parentId: 'ZZ', index: 0 }) as any).error,
    'unknown-target-parent',
  );
  assert.equal((moveNodes(tree, ['A1'], { parentId: 'C', index: 2 }) as any).error, 'index-out-of-range');
  assert.equal((moveNodes(tree, ['A1'], { parentId: 'C', index: -1 }) as any).error, 'index-out-of-range');
  assert.equal(snapshot(tree), before);
});

test('successful move does not mutate the input tree', () => {
  const tree = fixture();
  const before = snapshot(tree);
  const res = moveNodes(tree, ['A1', 'A3'], { parentId: 'C', index: 0 });
  assert.ok(res.ok);
  assert.equal(snapshot(tree), before);
  assert.notEqual(res.tree, tree);
});

test('history undo restores the exact previous tree, redo reapplies', () => {
  const history = new OutlineHistory(fixture());
  const original = history.tree;
  const before = snapshot(original);

  const first = history.move(['A1', 'A3'], { parentId: 'C', index: 0 });
  assert.ok(first.ok);
  const afterFirst = snapshot(history.tree);
  assert.notEqual(afterFirst, before);

  const second = history.move(['B1'], { parentId: 'A', index: 1 });
  assert.ok(second.ok);

  assert.equal(history.undo(), true);
  assert.equal(snapshot(history.tree), afterFirst);
  assert.equal(history.undo(), true);
  assert.equal(snapshot(history.tree), before);
  assert.equal(history.tree, original);
  assert.equal(history.undo(), false);

  assert.equal(history.redo(), true);
  assert.equal(snapshot(history.tree), afterFirst);
});

test('failed move through history leaves state untouched and non-undoable', () => {
  const history = new OutlineHistory(fixture());
  const before = snapshot(history.tree);
  const res = history.move(['A'], { parentId: 'A2a', index: 0 });
  assert.ok(!res.ok);
  assert.equal(snapshot(history.tree), before);
  assert.equal(history.canUndo, false);
});
