import { test } from 'node:test';
import assert from 'node:assert/strict';
import { diff } from '../src/diff.ts';
import { merge3 } from '../src/merge.ts';
import { applyPatch } from '../src/patch.ts';

const idArrays = { arrayMode: () => 'id' as const };

test('delete on one side vs modify on the other is a three-way conflict', () => {
  const base = { a: { x: 1 }, keep: 0 };
  const left = { keep: 0 }; // deleted a
  const right = { a: { x: 2 }, keep: 0 }; // modified a
  const result = merge3(base, left, right);
  assert.equal(result.conflicts.length, 1);
  const c = result.conflicts[0];
  assert.deepEqual(c.path, ['a']);
  assert.equal(c.reason, 'delete-vs-modify');
  assert.deepEqual(c.base, { x: 1 });
  assert.equal(c.left, undefined); // deleted on the left
  assert.deepEqual(c.right, { x: 2 });
  // Conservative resolution keeps the base value at the conflict path.
  assert.deepEqual(result.merged, { a: { x: 1 }, keep: 0 });
});

test('array move plus property fix on the other side preserves identity', () => {
  const base = { list: [{ id: 'a', v: 1 }, { id: 'b', v: 2 }, { id: 'c', v: 3 }] };
  const left = { list: [{ id: 'c', v: 3 }, { id: 'a', v: 1 }, { id: 'b', v: 2 }] }; // reordered
  const right = { list: [{ id: 'a', v: 1 }, { id: 'b', v: 22 }, { id: 'c', v: 3 }] }; // fixed b.v
  const result = merge3(base, left, right, idArrays);
  assert.equal(result.conflicts.length, 0);
  // Left's order wins (only side that reordered); right's property fix is merged in.
  assert.deepEqual(result.merged.list.map((e: any) => e.id), ['c', 'a', 'b']);
  assert.equal((result.merged.list as any[])[2].v, 22);
  // Identity preserved: diff(base, merged) reports moves, never remove+add pairs.
  const d = diff(base, result.merged, idArrays);
  assert.ok(d.changes.some((c) => c.kind === 'move'));
  assert.ok(!d.changes.some((c) => c.kind === 'remove'));
  const movedB = d.changes.find((c) => c.kind === 'move' && JSON.stringify(c.path) === '["list",2]');
  assert.deepEqual(movedB?.from, ['list', 1]);
  // Source map records the move provenance of element b.
  assert.deepEqual(result.sourceMap.get('$.list[2]'), { source: ['list', 1], kind: 'move' });
  // The merge patch applies atomically back onto base.
  const applied = applyPatch(base, result.patch, idArrays);
  assert.deepEqual(applied.root, result.merged);
});

test('a copied node is never reported as a move', () => {
  const base = { x: { v: 1 }, z: 0 };
  const target = { x: { v: 1 }, y: { v: 1 }, z: 0 }; // y is a copy of x; x still exists
  const d = diff(base, target);
  assert.ok(!d.changes.some((c) => c.kind === 'move'));
  const setY = d.changes.find((c) => c.kind === 'set');
  assert.deepEqual(setY?.path, ['y']);
  assert.deepEqual(setY?.copiedFrom, ['x']);
  assert.deepEqual(d.sourceMap.get('$.y'), { source: ['x'], kind: 'copy' });
  assert.deepEqual(d.sourceMap.get('$.x'), { source: ['x'], kind: 'same' });
});

test('a removed-and-readded subtree is reported as a move', () => {
  const base = { a: { deep: { v: 1 } }, z: 0 };
  const target = { c: { deep: { v: 1 } }, z: 0 };
  const d = diff(base, target);
  assert.equal(d.changes.length, 1);
  assert.equal(d.changes[0].kind, 'move');
  assert.deepEqual(d.changes[0].from, ['a']);
  assert.deepEqual(d.changes[0].path, ['c']);
  assert.deepEqual(d.sourceMap.get('$.c'), { source: ['a'], kind: 'move' });
});
