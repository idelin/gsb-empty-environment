import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  applyPatch,
  diffTrees,
  jsonEqual,
  TreeDiffError,
} from '../src/index.ts';
import type { JsonValue, Operation } from '../src/index.ts';

/** Recursively reverses object key order; arrays keep their order. */
function shuffleKeys(value: JsonValue): JsonValue {
  if (Array.isArray(value)) return value.map(shuffleKeys);
  if (value !== null && typeof value === 'object') {
    const out: Record<string, JsonValue> = {};
    for (const key of Object.keys(value).reverse()) {
      out[key] = shuffleKeys(value[key]);
    }
    return out;
  }
  return value;
}

function roundTrip(oldTree: JsonValue, newTree: JsonValue): Operation[] {
  const ops = diffTrees(oldTree, newTree);
  const result = applyPatch(oldTree, ops);
  assert.ok(
    jsonEqual(result, newTree),
    `patch did not reproduce target:\nops=${JSON.stringify(ops, null, 2)}\nresult=${JSON.stringify(result)}`,
  );
  return ops;
}

test('identical trees produce an empty patch', () => {
  const tree = { id: 'root', name: 'a', children: [{ id: 'c1', x: 1 }] };
  assert.deepEqual(diffTrees(tree, shuffleKeys(tree)), []);
});

test('inserts a subtree with parents before children', () => {
  const oldTree = { id: 'root', children: [] };
  const newTree = {
    id: 'root',
    children: [
      {
        id: 'a',
        label: 'A',
        children: [{ id: 'b', label: 'B' }],
      },
    ],
  };
  const ops = roundTrip(oldTree, newTree);
  const inserts = ops.filter((op) => op.op === 'insert');
  assert.deepEqual(
    inserts.map((op) => (op.op === 'insert' ? op.id : '')),
    ['a', 'b'],
  );
  const insertA = inserts.find((op) => op.op === 'insert' && op.id === 'a');
  assert.equal(insertA?.op === 'insert' && insertA.parentId, 'root');
  const insertB = inserts.find((op) => op.op === 'insert' && op.id === 'b');
  assert.equal(insertB?.op === 'insert' && insertB.parentId, 'a');
});

test('emits leaf-level attribute updates: set, add, unset', () => {
  const oldTree = {
    id: 'root',
    keep: 1,
    drop: 'x',
    nested: { a: 1, b: 2 },
    children: [{ id: 'c', tags: [1, 2] }],
  };
  const newTree = {
    id: 'root',
    keep: 1,
    added: true,
    nested: { a: 1, b: 3 },
    children: [{ id: 'c', tags: [1, 2, 3] }],
  };
  const ops = roundTrip(oldTree, newTree);
  assert.deepEqual(ops, [
    { op: 'update', id: 'c', path: ['tags'], value: [1, 2, 3] },
    { op: 'update', id: 'root', path: ['added'], value: true },
    { op: 'update', id: 'root', path: ['drop'], unset: true },
    { op: 'update', id: 'root', path: ['nested', 'b'], value: 3 },
  ]);
});

test('reorder uses a minimal number of moves (LIS)', () => {
  const oldTree = {
    id: 'root',
    children: [{ id: 'a' }, { id: 'b' }, { id: 'c' }],
  };
  const newTree = {
    id: 'root',
    children: [{ id: 'b' }, { id: 'c' }, { id: 'a' }],
  };
  const ops = roundTrip(oldTree, newTree);
  assert.deepEqual(ops, [
    { op: 'move', id: 'a', parentId: 'root', key: 'children', index: 2 },
  ]);
});

test('cross-parent move preserves node identity', () => {
  const oldTree = {
    id: 'root',
    left: [{ id: 'a', children: [{ id: 'b', v: 1 }] }],
    right: [{ id: 'c' }],
  };
  const newTree = {
    id: 'root',
    left: [{ id: 'a' }],
    right: [{ id: 'c' }, { id: 'b', v: 1 }],
  };
  const ops = roundTrip(oldTree, newTree);
  assert.deepEqual(ops, [
    { op: 'update', id: 'a', path: ['children'], unset: true },
    { op: 'move', id: 'b', parentId: 'root', key: 'right', index: 1 },
  ]);
  // no insert/remove for the moved node
  assert.ok(!ops.some((op) => (op.op === 'insert' || op.op === 'remove') && op.id === 'b'));
});

test('deleting a parent does not re-delete a child that moved away', () => {
  const oldTree = {
    id: 'root',
    children: [
      { id: 'p', children: [{ id: 'survivor', v: 1 }, { id: 'doomed' }] },
    ],
  };
  const newTree = {
    id: 'root',
    children: [{ id: 'survivor', v: 1 }],
  };
  const ops = roundTrip(oldTree, newTree);
  assert.deepEqual(ops, [
    { op: 'move', id: 'survivor', parentId: 'root', key: 'children', index: 1 },
    { op: 'remove', id: 'p' },
  ]);
});

test('deleting a subtree emits a single remove for the topmost node', () => {
  const oldTree = {
    id: 'root',
    children: [
      { id: 'keep' },
      { id: 'sub', children: [{ id: 'sub1', children: [{ id: 'sub2' }] }] },
    ],
  };
  const newTree = { id: 'root', children: [{ id: 'keep' }] };
  const ops = roundTrip(oldTree, newTree);
  assert.deepEqual(ops, [{ op: 'remove', id: 'sub' }]);
});

test('input object key order never affects the patch', () => {
  const oldTree = {
    id: 'root',
    a: 1,
    b: { x: 1, y: 2 },
    children: [{ id: 'c1', p: 1, q: 2 }, { id: 'c2' }],
  };
  const newTree = {
    id: 'root',
    a: 2,
    b: { x: 1, y: 3 },
    children: [{ id: 'c2' }, { id: 'c1', p: 9, q: 2 }, { id: 'c3', z: [1] }],
  };
  const plain = diffTrees(oldTree, newTree);
  const shuffled = diffTrees(shuffleKeys(oldTree), shuffleKeys(newTree));
  assert.equal(JSON.stringify(shuffled), JSON.stringify(plain));
});

test('root replacement works via insert(null parent) + remove', () => {
  const oldTree = { id: 'old-root', children: [{ id: 'a' }] };
  const newTree = { id: 'new-root', children: [{ id: 'a' }] };
  const ops = roundTrip(oldTree, newTree);
  assert.deepEqual(ops, [
    { op: 'insert', id: 'new-root', parentId: null, key: '', index: 1, node: { id: 'new-root' } },
    { op: 'move', id: 'a', parentId: 'new-root', key: 'children', index: 0 },
    { op: 'remove', id: 'old-root' },
  ]);
});

test('complex round-trip: updates + moves + inserts + removes', () => {
  const oldTree = {
    id: 'root',
    title: 'old',
    children: [
      { id: 'a', size: 1, children: [{ id: 'a1' }, { id: 'a2' }] },
      { id: 'b', children: [{ id: 'b1' }] },
      { id: 'c' },
    ],
  };
  const newTree = {
    id: 'root',
    title: 'new',
    children: [
      { id: 'c', fresh: true },
      { id: 'a', size: 2, children: [{ id: 'a2' }, { id: 'a3', n: 1 }] },
      { id: 'b1' },
    ],
  };
  const ops = roundTrip(oldTree, newTree);
  // b1 moved out of b (b removed), a1 removed with... a1 is gone from new tree
  assert.ok(ops.some((op) => op.op === 'move' && op.id === 'b1'));
  assert.ok(ops.some((op) => op.op === 'insert' && op.id === 'a3'));
  assert.ok(ops.some((op) => op.op === 'remove' && op.id === 'b'));
  assert.ok(ops.some((op) => op.op === 'remove' && op.id === 'a1'));
  assert.ok(ops.some((op) => op.op === 'update' && op.id === 'a'));
});

test('duplicate id reports both paths', () => {
  const tree = {
    id: 'root',
    children: [{ id: 'dup' }, { id: 'dup' }],
  };
  assert.throws(
    () => diffTrees(tree, tree),
    (err: unknown) => {
      assert.ok(err instanceof TreeDiffError);
      assert.equal(err.path, '/children/1');
      assert.match(err.message, /first seen at \/children\/0/);
      return true;
    },
  );
});

test('missing root id is located at the root path', () => {
  assert.throws(
    () => diffTrees({ name: 'x' }, { id: 'r' }),
    (err: unknown) => {
      assert.ok(err instanceof TreeDiffError);
      assert.equal(err.path, '');
      assert.match(err.message, /missing required string "id"/);
      return true;
    },
  );
});

test('invalid values are located by path', () => {
  const bad = { id: 'root', children: [{ id: 'c', score: Number.POSITIVE_INFINITY }] };
  assert.throws(
    () => diffTrees(bad, { id: 'root' }),
    (err: unknown) => {
      assert.ok(err instanceof TreeDiffError);
      assert.equal(err.path, '/children/0/score');
      return true;
    },
  );
});

test('dangling parent reference is located at the failing op', () => {
  const oldTree = { id: 'root', children: [{ id: 'a' }] };
  const ops: Operation[] = [
    { op: 'move', id: 'a', parentId: 'ghost', key: 'children', index: 0 },
  ];
  assert.throws(
    () => applyPatch(oldTree, ops),
    (err: unknown) => {
      assert.ok(err instanceof TreeDiffError);
      assert.equal(err.path, '/ops/0');
      assert.match(err.message, /dangling reference/);
      return true;
    },
  );
});

test('removing the root without a replacement is an error', () => {
  const oldTree = { id: 'root', children: [{ id: 'a' }] };
  assert.throws(
    () => applyPatch(oldTree, [{ op: 'remove', id: 'root' }]),
    /removed the root/,
  );
});
